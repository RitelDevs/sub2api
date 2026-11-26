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
