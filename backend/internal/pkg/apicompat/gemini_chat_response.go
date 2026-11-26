package apicompat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/sse"
)

// ErrGeminiResponseConversion is an upstream protocol failure. A client that
// successfully received its error response is still connected.
var ErrGeminiResponseConversion = errors.New("invalid upstream Chat response")

func geminiFinish(reason string) (string, error) {
	switch reason {
	case "stop", "tool_calls", "function_call":
		return "STOP", nil
	case "length":
		return "MAX_TOKENS", nil
	case "content_filter":
		return "SAFETY", nil
	default:
		return "", fmt.Errorf("%w: missing or unrecognized finish reason", ErrGeminiResponseConversion)
	}
}

func geminiToolPart(id, name, args, reason string) (map[string]any, error) {
	args = strings.TrimSpace(args)
	if args == "" && reason != "length" {
		args = "{}"
	}
	if strings.TrimSpace(name) == "" || !strings.HasPrefix(args, "{") || !json.Valid([]byte(args)) {
		if reason == "length" {
			return nil, nil
		}
		return nil, fmt.Errorf("%w: invalid tool call", ErrGeminiResponseConversion)
	}
	call := map[string]any{"name": name, "args": json.RawMessage(args)}
	if id != "" {
		call["id"] = id
	}
	return map[string]any{"functionCall": call}, nil
}

func geminiTextParts(thought, text, refusal string) []any {
	var parts []any
	if thought != "" {
		parts = append(parts, map[string]any{"thought": true, "text": thought})
	}
	if text != "" {
		parts = append(parts, map[string]any{"text": text})
	}
	if refusal != "" {
		parts = append(parts, map[string]any{"text": refusal})
	}
	return parts
}

func ChatCompletionsJSONToGemini(body []byte, fallbackModel string, expected ...int) ([]byte, error) {
	var resp struct {
		ChatCompletionsResponse
		Error json.RawMessage  `json:"error"`
		Usage *geminiChatUsage `json:"usage"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGeminiResponseConversion, err)
	}
	if hasChatError(resp.Error) || len(resp.Choices) == 0 || (len(expected) > 0 && expected[0] > 0 && len(resp.Choices) != expected[0]) {
		return nil, fmt.Errorf("%w: absent or incomplete candidate set", ErrGeminiResponseConversion)
	}
	candidates := make([]any, 0, len(resp.Choices))
	seen := make(map[int]bool, len(resp.Choices))
	for _, ch := range resp.Choices {
		if ch.Index < 0 || ch.Index >= len(resp.Choices) || seen[ch.Index] {
			return nil, fmt.Errorf("%w: invalid candidate index", ErrGeminiResponseConversion)
		}
		seen[ch.Index] = true
		finish, err := geminiFinish(ch.FinishReason)
		if err != nil {
			return nil, err
		}
		parts := geminiTextParts(ch.Message.reasoningText(), chatMessageText(ch.Message), ch.Message.Refusal)
		for _, call := range ch.Message.ToolCalls {
			part, err := geminiToolPart(call.ID, call.Function.Name, call.Function.Arguments, ch.FinishReason)
			if err != nil {
				return nil, err
			}
			if part != nil {
				parts = append(parts, part)
			}
		}
		if ch.Message.FunctionCall != nil {
			fn := ch.Message.FunctionCall
			part, err := geminiToolPart("", fn.Name, fn.Arguments, ch.FinishReason)
			if err != nil {
				return nil, err
			}
			if part != nil {
				parts = append(parts, part)
			}
		}
		if len(parts) == 0 {
			parts = []any{map[string]any{"text": ""}}
		}
		candidates = append(candidates, geminiStreamCandidate(ch.Index, parts, finish))
	}
	out := map[string]any{"candidates": candidates}
	if model := strings.TrimSpace(resp.Model); model != "" {
		out["modelVersion"] = model
	} else if fallbackModel != "" {
		out["modelVersion"] = fallbackModel
	}
	if resp.Usage != nil {
		usage, err := resp.Usage.metadata()
		if err != nil {
			return nil, err
		}
		if len(usage) > 0 {
			out["usageMetadata"] = usage
		}
	}
	return json.Marshal(out)
}

func hasChatError(raw json.RawMessage) bool { return len(raw) > 0 && !bytes.Equal(raw, []byte("null")) }

// Missing counters and counters explicitly reported as zero differ. Keep the
// three Chat usage totals wire-compatible and preserve optional cached tokens.
type geminiChatUsage struct {
	Prompt     *int64 `json:"prompt_tokens"`
	Completion *int64 `json:"completion_tokens"`
	Total      *int64 `json:"total_tokens"`
	Details    struct {
		Cached *int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u *geminiChatUsage) metadata() (map[string]int64, error) {
	counts := make(map[string]int64, 4)
	for _, item := range []struct {
		name  string
		value *int64
	}{{"promptTokenCount", u.Prompt}, {"candidatesTokenCount", u.Completion}, {"totalTokenCount", u.Total}, {"cachedContentTokenCount", u.Details.Cached}} {
		if item.value == nil {
			continue
		}
		if *item.value < 0 {
			return nil, fmt.Errorf("%w: negative usage counter", ErrGeminiResponseConversion)
		}
		counts[item.name] = *item.value
	}
	return counts, nil
}

type geminiCall struct {
	id, name string
	args     strings.Builder
}
type geminiChoiceState struct {
	finished bool
	tools    map[int]*geminiCall
}
type geminiStreamChoice struct {
	Index        int     `json:"index"`
	FinishReason *string `json:"finish_reason"`
	Delta        struct {
		ChatDelta
		FunctionCall *ChatFunctionCall `json:"function_call"`
	} `json:"delta"`
	ContentFilterResults json.RawMessage `json:"content_filter_results"`
	ContentFilterOffsets json.RawMessage `json:"content_filter_offsets"`
}

type GeminiSSETranslator struct {
	model                      string
	decoder                    *sse.Decoder
	choices                    map[int]*geminiChoiceState
	expected, limit, toolBytes int
	done, closed               bool
	err                        error
}

func NewGeminiSSETranslator(model string, maxFrameBytes ...int) *GeminiSSETranslator {
	limit := 500 << 20
	if len(maxFrameBytes) > 0 && maxFrameBytes[0] > 0 {
		limit = maxFrameBytes[0]
	}
	return &GeminiSSETranslator{model: model, decoder: sse.NewDecoder(limit), limit: limit, choices: map[int]*geminiChoiceState{}}
}

// ExpectCandidates sets the Chat request's n. Standalone decoders without
// request context still check every choice they actually receive.
func (t *GeminiSSETranslator) ExpectCandidates(n int) { t.expected = n }
func (t *GeminiSSETranslator) Err() error             { return t.err }

func (t *GeminiSSETranslator) checkComplete() error {
	if len(t.choices) == 0 || (t.expected > 0 && len(t.choices) != t.expected) {
		return errors.New("missing requested candidate")
	}
	for _, st := range t.choices {
		if !st.finished {
			return errors.New("missing candidate terminal")
		}
	}
	return nil
}

func writeGeminiFrame(out *bytes.Buffer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	out.Grow(len(data) + 8)
	out.WriteString("data: ")
	out.Write(data)
	out.WriteString("\n\n")
	return nil
}

func (t *GeminiSSETranslator) readEvent(e sse.Event, out *bytes.Buffer) error {
	if len(e.Comment) > 0 {
		out.Write(e.Comment)
		out.WriteString("\n\n")
		return nil
	}
	data := bytes.TrimSpace(e.Data)
	if e.Name == "error" {
		return errors.New("upstream error event")
	}
	if len(data) == 0 {
		return nil
	}
	if bytes.Equal(data, []byte("[DONE]")) {
		t.done = true
		return t.checkComplete()
	}
	if t.done {
		return errors.New("upstream data after [DONE]")
	}
	var chunk struct {
		Error   json.RawMessage      `json:"error"`
		Model   string               `json:"model"`
		Choices []geminiStreamChoice `json:"choices"`
		Usage   *geminiChatUsage     `json:"usage"`
	}
	if err := json.Unmarshal(data, &chunk); err != nil {
		return err
	}
	if hasChatError(chunk.Error) {
		return errors.New("upstream error event")
	}
	if chunk.Model != "" {
		t.model = chunk.Model
	}
	for _, c := range chunk.Choices {
		if err := t.readChoice(c, out); err != nil {
			return err
		}
	}
	if chunk.Usage != nil {
		usage, err := chunk.Usage.metadata()
		if err != nil {
			return err
		}
		if len(usage) == 0 {
			return nil
		}
		v := map[string]any{"usageMetadata": usage}
		if t.model != "" {
			v["modelVersion"] = t.model
		}
		return writeGeminiFrame(out, v)
	}
	return nil
}

func optionalText(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func (t *GeminiSSETranslator) readChoice(c geminiStreamChoice, out *bytes.Buffer) error {
	if c.Index < 0 || (t.expected > 0 && c.Index >= t.expected) {
		return errors.New("unexpected candidate index")
	}
	d := c.Delta
	if d.FunctionCall != nil {
		d.ToolCalls = append(d.ToolCalls, ChatToolCall{Function: *d.FunctionCall})
	}
	parts := geminiTextParts(optionalText(d.reasoningText()), optionalText(d.Content), optionalText(d.Refusal))
	reason := optionalText(c.FinishReason)
	// Azure async filtering emits annotations even after the candidate ended.
	if reason == "" && d.Role == "" && len(parts) == 0 && len(d.ToolCalls) == 0 && (hasChatError(c.ContentFilterResults) || hasChatError(c.ContentFilterOffsets)) {
		return nil
	}
	st := t.choices[c.Index]
	if st == nil {
		st = &geminiChoiceState{}
		t.choices[c.Index] = st
	}
	if st.finished {
		return errors.New("candidate data after terminal")
	}
	for _, call := range d.ToolCalls {
		idx := 0
		if call.Index != nil {
			idx = *call.Index
		}
		if idx < 0 {
			return errors.New("invalid tool index")
		}
		if st.tools == nil {
			st.tools = map[int]*geminiCall{}
		}
		acc := st.tools[idx]
		if acc == nil {
			acc = &geminiCall{}
			st.tools[idx] = acc
		}
		if call.ID != "" {
			if acc.id != "" && acc.id != call.ID {
				return errors.New("tool ID changed")
			}
			acc.id = call.ID
		}
		if name := call.Function.Name; name != "" {
			if acc.name != "" && name != acc.name {
				return errors.New("tool name changed")
			}
			acc.name = name
		}
		if len(call.Function.Arguments) > t.limit-t.toolBytes {
			return sse.ErrFrameTooLarge
		}
		t.toolBytes += len(call.Function.Arguments)
		acc.args.WriteString(call.Function.Arguments)
	}
	finish := ""
	if reason != "" {
		var err error
		finish, err = geminiFinish(reason)
		if err != nil {
			return err
		}
		indexes := make([]int, 0, len(st.tools))
		for idx := range st.tools {
			indexes = append(indexes, idx)
		}
		sort.Ints(indexes)
		for _, idx := range indexes {
			a := st.tools[idx]
			p, err := geminiToolPart(a.id, a.name, a.args.String(), reason)
			if err != nil {
				return err
			}
			if p != nil {
				parts = append(parts, p)
			}
			t.toolBytes -= a.args.Len()
		}
		st.finished = true
		st.tools = nil
		if len(parts) == 0 {
			parts = []any{map[string]any{"text": ""}}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return writeGeminiFrame(out, map[string]any{"candidates": []any{geminiStreamCandidate(c.Index, parts, finish)}})
}

// Convert emits completed Gemini frames preceding a failure, allowing the HTTP
// adapter to choose a plain 502 or a stream error based on actual delivery.
func (t *GeminiSSETranslator) Convert(data []byte, eof bool) ([]byte, error) {
	if t.err != nil || t.closed {
		return nil, t.err
	}
	var out bytes.Buffer
	consume := func(e sse.Event) error { return t.readEvent(e, &out) }
	err := t.decoder.Push(data, consume)
	if err == nil && eof {
		t.closed = true
		err = t.decoder.Finish(consume)
		if err == nil {
			err = t.checkComplete()
		}
	}
	if err != nil {
		t.err = fmt.Errorf("%w: %w", ErrGeminiResponseConversion, err)
	}
	if t.closed || t.err != nil {
		t.choices = nil
	}
	return out.Bytes(), t.err
}

func (t *GeminiSSETranslator) Push(data []byte) []byte { return t.wire(data, false) }
func (t *GeminiSSETranslator) Flush() []byte           { return t.wire(nil, true) }
func (t *GeminiSSETranslator) wire(data []byte, eof bool) []byte {
	if t == nil || t.err != nil || t.closed {
		return nil
	}
	out, err := t.Convert(data, eof)
	if err != nil {
		out = append(out, `data: {"error":{"code":502,"status":"BAD_GATEWAY","message":"Invalid upstream response stream"}}`...)
		out = append(out, '\n', '\n')
	}
	return out
}
