package service

import (
	"bufio"
	"context"
	"errors"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

type rawChatLines interface {
	Scan() bool
	Text() string
	Err() error
}

// Keep the original SSE fields and formatting for passthrough. Provider
// normalizers and accounting receive the joined data from one event only.
type rawChatFrame struct {
	lines []string
	data  []string
	name  string
}

func readRawChatFrame(src rawChatLines, limit int64) (f rawChatFrame, err error) {
	var size int64
	for src.Scan() {
		line := src.Text()
		if line == "" {
			return f, nil
		}
		// Budget the string headers retained in both slices as well as wire bytes.
		if int64(len(line))+1+int64(len(f.lines)+len(f.data)+2)*16 > limit-size {
			return f, bufio.ErrTooLong
		}
		size += int64(len(line)) + 1
		f.lines = append(f.lines, line)
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "data":
			f.data = append(f.data, value)
		case "event":
			f.name = value
		}
	}
	if err := src.Err(); err != nil {
		return f, err
	}
	// Compatible clean EOF without a final empty line.
	if len(f.lines) != 0 {
		return f, nil
	}
	return f, io.EOF
}

// An unchanged event is forwarded byte-for-byte apart from line endings. If
// the JSON was rewritten, encode each physical payload line as an SSE field.
func (f *rawChatFrame) wire(original, rewritten string) string {
	if len(f.lines) == 0 {
		return "\n"
	}
	if rewritten == original {
		return strings.Join(f.lines, "\n") + "\n\n"
	}
	var out strings.Builder
	replaced := false
	for _, line := range f.lines {
		field, _, _ := strings.Cut(line, ":")
		if field == "data" {
			if replaced {
				continue
			}
			line = "data: " + strings.ReplaceAll(rewritten, "\n", "\ndata: ")
			replaced = true
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	out.WriteByte('\n')
	return out.String()
}

type rawChatSink struct {
	writer  gin.ResponseWriter
	headers func()
	pending strings.Builder
	started bool
	dirty   bool
	err     error
}

func (w *rawChatSink) write(wire string, release bool) error {
	if w.err != nil {
		return nil
	}
	if !release && !w.started {
		if len(wire) > openAIFirstOutputStageMaxBytes-w.pending.Len() {
			return errors.New("raw Chat stream exceeded pre-output staging limit")
		}
		w.pending.WriteString(wire)
		return nil
	}
	if !w.started {
		w.headers()
		w.started = true
		if w.pending.Len() > 0 {
			w.writeString(w.pending.String())
			w.pending.Reset()
		}
	}
	if w.err == nil && wire != "" {
		w.writeString(wire)
	}
	return nil
}

func (w *rawChatSink) writeString(wire string) {
	var n int
	n, w.err = w.writer.WriteString(wire)
	w.dirty = w.dirty || n > 0
	if w.err == nil && n != len(wire) {
		w.err = io.ErrShortWrite
	}
}

func (w *rawChatSink) disconnected() bool {
	return w.err != nil && !errors.Is(w.err, apicompat.ErrGeminiResponseConversion)
}

func (w *rawChatSink) flush(ctx context.Context) {
	if !w.dirty || w.err != nil {
		return
	}
	w.dirty = false
	w.err = FlushGatewayResponse(w.writer)
	if w.err == nil {
		operationProtocolOutputFlushed(ctx)
	}
}

type rawChatObservation struct {
	terminal        openAIRawStreamTerminalState
	usage           OpenAIUsage
	usagePayload    []byte
	firstTokenMs    *int
	semanticFailure bool
}

func (st *rawChatObservation) observe(f *rawChatFrame, payload string, ctx context.Context, c *gin.Context, observer *upstreamResponseModelObserver, start time.Time) {
	if len(f.data) == 0 {
		return
	}
	st.terminal.sawDataLine = true
	if strings.TrimSpace(payload) == "[DONE]" {
		st.terminal.ObserveDataLine("[DONE]")
		regionalMeterObserveUsage(c.Request.Context(), st.usagePayload, "json")
		return
	}
	if !gjson.Valid(payload) {
		return
	}
	if u := extractCCStreamUsage(payload); u != nil {
		st.usagePayload = []byte(payload)
		mergeOpenAIProtectionUsageEvidence(st.usage, u)
		st.usage = *u
	}
	st.terminal.ObserveDataLine(payload)
	st.terminal.sawUsage = st.usagePayload != nil
	ObserveOperationProtocol(ctx, "chat", []byte(payload), f.name)
	observer.ObserveOpenAI([]byte(payload), gjson.Get(payload, "type").String())
	if st.firstTokenMs == nil && !isOpenAIChatUsageOnlyStreamChunk(payload) {
		ms := int(requesttiming.Now(c.Request.Context()).Sub(start).Milliseconds())
		st.firstTokenMs = &ms
	}
}

var openAIUsageCounterPaths = [...]string{
	"input_tokens", "output_tokens", "prompt_tokens", "completion_tokens",
	"cache_read_input_tokens", "cache_read_tokens", "cached_tokens", "prompt_cache_hit_tokens",
	"cache_creation_input_tokens", "cache_write_tokens", "cache_write_input_tokens", "cache_creation_tokens",
	"prompt_tokens_details.cached_tokens", "input_tokens_details.cached_tokens",
	"prompt_tokens_details.image_tokens", "input_tokens_details.image_tokens", "completion_tokens_details.image_tokens", "output_tokens_details.image_tokens",
	"prompt_tokens_details.cache_creation_input_tokens", "input_tokens_details.cache_creation_input_tokens",
	"prompt_tokens_details.cache_write_tokens", "input_tokens_details.cache_write_tokens",
	"prompt_tokens_details.cache_creation_tokens", "input_tokens_details.cache_creation_tokens",
}

// Empty objects and malformed counters are not measured zero usage. Apply the
// same validation to canonical counters, cached inputs and media outputs.
func rawChatUsageReported(value gjson.Result) bool {
	reported := false
	for _, key := range openAIUsageCounterPaths {
		v := value.Get(key)
		// Some providers use a negative cache-write sentinel. The cache parser
		// clamps it to zero ahead of the creation alias; other buckets still count.
		if (key == "prompt_tokens_details.cache_write_tokens" || key == "input_tokens_details.cache_write_tokens") && v.Type == gjson.Number && v.Num < 0 && math.Trunc(v.Num) == v.Num {
			continue
		}
		if v.Exists() && v.Type != gjson.Null && (v.Type != gjson.Number || v.Num < 0 || math.Trunc(v.Num) != v.Num || v.Num >= float64(int(^uint(0)>>1))) {
			return false
		}
		reported = reported || v.Type == gjson.Number
	}
	return reported
}

func (s *OpenAIGatewayService) streamRawChatCompletions(c *gin.Context, resp *http.Response, account *Account, originalModel, billingModel, upstreamModel string, reasoningEffort, serviceTier *string, start time.Time, requestBodyLen int) (*OpenAIForwardResult, error) {
	ctx := requesttiming.ResponseContext(c.Request.Context(), resp)
	if c.GetBool("ops_operation_multichoice") {
		requesttiming.ObserveOperationSource(ctx, requesttiming.OperationEvidence{Protocol: "chat", Unsupported: true})
	}
	observer := upstreamResponseModelObserverFromContext(c)
	if observer == nil {
		observer = beginUpstreamResponseModelObservation(c)
	}
	id := resp.Header.Get("x-request-id")
	maxLineSize := defaultMaxLineSize
	if s.cfg != nil && s.cfg.Gateway.MaxLineSize > 0 {
		maxLineSize = s.cfg.Gateway.MaxLineSize
	}
	maxFrame := SSEFrameLimit(maxLineSize)
	refusal := newOpenAIChatSilentRefusalDetector(requestBodyLen)
	sink := rawChatSink{writer: c.Writer, headers: s.newStreamHeaderWriter(c, resp.Header)}
	var obs rawChatObservation
	flush := func() { sink.flush(ctx) }
	scanner, afterWrite := s.rawChatFrameSource(resp.Body, account, upstreamModel, flush)
	var sourceErr error
	var drainTimer *time.Timer
	defer func() {
		if drainTimer != nil {
			drainTimer.Stop()
		}
	}()
	for {
		frame, err := readRawChatFrame(scanner, maxFrame)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				sourceErr = err
			}
			break
		}
		payload := strings.Join(frame.data, "\n")
		obs.observe(&frame, payload, ctx, c, observer, start)
		if obs.semanticFailure || sink.err != nil {
			if strings.TrimSpace(payload) == "[DONE]" {
				break
			}
			continue
		}
		line := "data: " + payload
		out := line
		if len(frame.data) > 0 {
			if frame.name == "error" || gjson.Get(payload, "error").IsObject() || gjson.Get(payload, "type").String() == "error" || (strings.Contains(payload, `"error"`) && gjson.Get(payload, `choices.#(finish_reason=="error")`).Exists()) {
				if !gjson.Valid(payload) {
					continue
				}
				obs.semanticFailure = true
			} else {
				out, err = normalizeMistralChatSSELine(account, line)
				if err != nil {
					sourceErr = err
					break
				}
				refusal.ObserveSSELine(out)
				out = applyOllamaCloudRawChatCompletionsSSELine(account, out)
				out = stripEmptyChatToolCallIdentityFromSSELine(out)
				out = s.replaceModelInSSELine(out, upstreamModel, originalModel)
			}
		}
		rewritten := payload
		if out != line {
			rewritten, _ = extractOpenAISSEDataLine(out)
		}
		if err = sink.write(frame.wire(payload, rewritten), obs.semanticFailure || refusal.ShouldReleaseClientOutput()); err != nil {
			sourceErr = err
			break
		}
		afterWrite()
		// Upstreams can send measured usage after the semantic error; cap the
		// drain when the provider never closes its stream.
		if (obs.semanticFailure || sink.err != nil) && drainTimer == nil {
			interval := 180 * time.Second
			if s.cfg != nil && s.cfg.Gateway.StreamDataIntervalTimeout > 0 {
				interval = time.Duration(s.cfg.Gateway.StreamDataIntervalTimeout) * time.Second
			}
			drainTimer = time.AfterFunc(interval, func() { _ = resp.Body.Close() })
		}
	}
	if sink.started {
		flush()
	}
	clientAborted := sink.disconnected() || errors.Is(sourceErr, context.Canceled) || errors.Is(sourceErr, context.DeadlineExceeded)
	if !clientAborted && !sink.started && !obs.terminal.Terminated() && obs.usagePayload == nil {
		if reset, handled := handleOpenAIHTTP2PeerReset(c, account, id, sourceErr); handled {
			return nil, reset
		}
		cause := sourceErr
		if cause == nil {
			cause = ErrOpenAIUpstreamStreamTruncated
		}
		return nil, newOpenAIRawStreamTruncatedFailoverError(c, account, id, cause)
	}
	if !sink.started && !clientAborted && sourceErr == nil {
		if refusal.IsSilentRefusal() {
			return nil, newOpenAISilentRefusalFailoverError(c, account, id)
		}
		if sink.pending.Len() > 0 {
			_ = sink.write("", true)
			flush()
		}
	}
	if sink.err == nil && (sourceErr == nil || obs.terminal.sawDone) && !obs.semanticFailure && !clientAborted {
		if final, ok := c.Writer.(interface{ FinalizeGatewayStream() error }); ok {
			sink.err = final.FinalizeGatewayStream()
		}
	}
	result := &OpenAIForwardResult{
		RequestID:                     id,
		UpstreamHeaders:               resp.Header,
		Usage:                         obs.usage,
		Model:                         originalModel,
		BillingModel:                  billingModel,
		UpstreamModel:                 upstreamModel,
		UpstreamResponseModel:         observedUpstreamResponseModel(c),
		UpstreamResponseModelConflict: observedUpstreamResponseModelConflict(c),
		UpstreamResponseServiceTier:   observedUpstreamResponseServiceTier(c),
		ReasoningEffort:               reasoningEffort,
		ServiceTier:                   resolvedOpenAIUpstreamServiceTier(c, serviceTier),
		Stream:                        true,
		ClientDisconnect:              sink.disconnected(),
		Duration:                      requesttiming.Now(c.Request.Context()).Sub(start),
		FirstTokenMs:                  obs.firstTokenMs,
	}
	if sourceErr != nil || sink.err != nil || obs.semanticFailure || (!obs.terminal.sawDone && !obs.terminal.sawFinishReason) {
		result.Usage.CacheReadKnown = false
	}
	measured := obs.usagePayload != nil
	result.UsageUnavailable = !measured
	var failure error
	switch {
	case errors.Is(sink.err, apicompat.ErrGeminiResponseConversion):
		MarkResponseCommitted(c)
		failure = sink.err
	case obs.semanticFailure:
		MarkResponseCommitted(c)
		failure = errors.New("upstream response failed")
	case !clientAborted && (sourceErr != nil && !obs.terminal.sawDone || obs.terminal.IsTruncated(sink.started)):
		if !obs.terminal.Terminated() {
			if reset, handled := handleOpenAIHTTP2PeerReset(c, account, id, sourceErr); handled {
				failure = reset
				break
			}
		}
		if sourceErr == nil {
			sourceErr = ErrOpenAIUpstreamStreamTruncated
		}
		requesttiming.ObserveOperationSource(ctx, requesttiming.OperationEvidence{Protocol: "chat", Terminal: "failed", Complete: true, FailureReason: "source_missing_terminal"})
		recordOpenAIRawStreamTruncation(c, account, id, sourceErr, "http_error")
		failure = newOpenAIUpstreamStreamReadError(sourceErr)
	}
	if failure != nil {
		result.ObservedUsageOnError = measured
	} else if !clientAborted && sink.err == nil {
		operationProtocolDelivered(ctx, nil)
	}
	return result, failure
}
