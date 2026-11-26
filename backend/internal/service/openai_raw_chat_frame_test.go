//go:build unit

package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rawChatTestSink struct {
	gin.ResponseWriter
	err   error
	short bool
}

func TestRawChatFrameUsageAndDeliveryCacheWriteSentinel(t *testing.T) {
	for _, details := range []string{"prompt_tokens_details", "input_tokens_details"} {
		t.Run(details, func(t *testing.T) {
			usage := extractCCStreamUsage(`{"usage":{"prompt_tokens":10,"` + details + `":{"cached_tokens":3,"cache_write_tokens":-2,"cache_creation_tokens":4}}}`)
			require.NotNil(t, usage)
			require.Equal(t, 10, usage.InputTokens)
			require.Equal(t, 3, usage.CacheReadInputTokens)
			require.Zero(t, usage.CacheCreationInputTokens)
			require.Nil(t, extractCCStreamUsage(`{"usage":{"`+details+`":{"cache_write_tokens":-2}}}`))
			require.Nil(t, extractCCStreamUsage(`{"usage":{"prompt_tokens":-2,"`+details+`":{"cached_tokens":3}}}`))
		})
	}
}

func (w *rawChatTestSink) WriteString(p string) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if w.short {
		return len(p) - 1, nil
	}
	return w.ResponseWriter.WriteString(p)
}

func TestRawChatFrameUsageAndDelivery(t *testing.T) {
	const text = `data: {"choices":[{"index":0,"delta":{"content":"ready"}}]}` + "\n\n"
	const usage = "id: receipt\ndata: {\"choices\":[],\"usage\":\ndata: {\"prompt_tokens\":4,\"completion_tokens\":3,\"total_tokens\":7}}\n\n"
	const errorEvent = "event: error\ndata: {\"error\":{\"message\":\"fixture\"},\"choices\":[{\"finish_reason\":\"error\"}]}\n\n"
	const done = "data: [DONE]\n\n"
	for _, tc := range []struct {
		name, source                          string
		sinkErr, readErr                      error
		short, disconnected, failed, measured bool
		input, output                         int
	}{
		{name: "multiline-CR-BOM", source: "\xef\xbb\xbf" + strings.ReplaceAll(text+usage+done, "\n", "\r"), measured: true, input: 4, output: 3},
		{name: "conversion-error", source: text + usage + done, sinkErr: fmt.Errorf("bad output: %w", apicompat.ErrGeminiResponseConversion), failed: true, measured: true, input: 4, output: 3},
		{name: "short-write", source: text + usage + done, short: true, disconnected: true, measured: true, input: 4, output: 3},
		{name: "semantic-error-with-trailing-meter", source: text + errorEvent + usage + done, failed: true, measured: true, input: 4, output: 3},
		{name: "malformed-meter-at-reset", source: text + strings.TrimSuffix(usage, "\n"), readErr: io.ErrUnexpectedEOF, failed: true},
		{name: "malformed-counter", source: text + errorEvent + `data: {"choices":[],"usage":{"prompt_tokens":-3,"completion_tokens":4}}` + "\n\n" + done, failed: true},
		{name: "explicit-zero", source: text + errorEvent + `data: {"choices":[],"usage":{"prompt_tokens":0,"completion_tokens":0}}` + "\n\n" + done, failed: true, measured: true},
		{name: "closed-after-done", source: text + usage + done, readErr: io.ErrUnexpectedEOF, measured: true, input: 4, output: 3},
		{name: "empty-meter-no-terminal", source: text + "data: {\"choices\":[],\"usage\":{}}\n\n", failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			c.Writer = &rawChatTestSink{ResponseWriter: c.Writer, err: tc.sinkErr, short: tc.short}
			body := io.NopCloser(strings.NewReader(tc.source))
			if tc.readErr != nil {
				body = &openAIStreamReadThenErrorCloser{reader: strings.NewReader(tc.source), err: tc.readErr}
			}
			res, err := (&OpenAIGatewayService{}).streamRawChatCompletions(c, &http.Response{Header: http.Header{}, Body: body}, rawChatCompletionsTestAccount(), "model", "model", "model", nil, nil, time.Now(), 0)
			require.NotNil(t, res)
			require.Equal(t, tc.failed, err != nil)
			require.Equal(t, tc.disconnected, res.ClientDisconnect)
			require.Equal(t, tc.measured, !res.UsageUnavailable)
			require.Equal(t, tc.measured && tc.failed, res.ObservedUsageOnError)
			require.Equal(t, tc.input, res.Usage.InputTokens)
			require.Equal(t, tc.output, res.Usage.OutputTokens)
			if errors.Is(tc.sinkErr, apicompat.ErrGeminiResponseConversion) {
				require.ErrorIs(t, err, apicompat.ErrGeminiResponseConversion)
			}
			if tc.name == "multiline-CR-BOM" {
				require.Equal(t, text+usage+done, rec.Body.String())
			}
			if tc.name == "semantic-error-with-trailing-meter" {
				require.Equal(t, text+errorEvent, rec.Body.String())
			}
		})
	}
}

func TestRawChatMultilineFrameLimits(t *testing.T) {
	const lineLimit = 1024 * 1024
	const text = `data: {"choices":[{"index":0,"delta":{"content":"ready"}}]}` + "\n\n"
	const usage = `data: {"choices":[],"usage":{"prompt_tokens":4,"completion_tokens":3}}` + "\n\n"
	// Put usage on the last physical line so a partial frame cannot count it.
	pad := strings.Repeat("x", lineLimit/2)
	multiline := `data: {"choices":[{"index":0,"delta":{"reasoning_content":"` + pad + `",` + "\n" +
		`data: "content":"` + pad + `"},"finish_reason":"stop"}],` + "\n" +
		`data: "usage":{"prompt_tokens":4,"completion_tokens":3}}`
	for _, tc := range []struct {
		name, tail string
		fail       bool
	}{
		{"clean-EOF", multiline, false},
		{"delimited", multiline + "\n\ndata: [DONE]\n\n", false},
		{"oversize-line-after-usage", usage + "data: " + strings.Repeat("x", lineLimit) + "\n\n", true},
		{"oversize-event-after-usage", usage + strings.Repeat(":"+pad+"\n", 19) + "\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
			svc.cfg.Gateway.MaxLineSize = lineLimit
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(text + tc.tail))}
			result, err := svc.streamRawChatCompletions(c, resp, rawChatCompletionsTestAccount(), "model", "model", "model", nil, nil, time.Now(), 0)
			if tc.fail {
				require.Error(t, err)
				var retry *UpstreamFailoverError
				require.NotErrorAs(t, err, &retry)
				require.Equal(t, text+usage, rec.Body.String())
			} else {
				require.NoError(t, err)
				require.True(t, strings.HasPrefix(rec.Body.String(), text+multiline+"\n\n"))
			}
			require.NotNil(t, result)
			require.Equal(t, 4, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.False(t, result.UsageUnavailable)
			require.Equal(t, tc.fail, result.ObservedUsageOnError)
		})
	}
}
