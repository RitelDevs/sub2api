//go:build unit

package service_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiRawChatResponseLifecycle(t *testing.T) {
	const text = `data: {"choices":[{"index":0,"delta":{"content":"first"}}]}` + "\n\n"
	const stop = `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}` + "\n\n"
	const azure = `data: {"id":"","choices":[{"index":0,"finish_reason":null,"content_filter_results":{"hate":{"filtered":false}},"content_filter_offsets":{"check_offset":15}}]}` + "\n\n"
	const vendorErr = `data: {"error":{"code":502,"message":"fixture"},"choices":[{"index":0,"delta":{},"finish_reason":"error"}]}` + "\n\n"
	const invalidTool = `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"function":{"name":"lookup","arguments":"{"}}]},"finish_reason":"tool_calls"}]}` + "\n\n"
	const usage = "data: {\"choices\":[],\"usage\":\ndata: {\"prompt_tokens\":4,\"completion_tokens\":3,\"total_tokens\":7}}\n\n"
	const done = "data: [DONE]\n\n"
	for _, tc := range []struct {
		name, wire                       string
		status                           int
		stops, errors, usageIn, usageOut int
	}{
		{"annotated stop and late counts", text + stop + azure + usage + done, 200, 1, 0, 4, 3},
		{"upstream error after text", text + vendorErr, 200, 0, 1, 0, 0},
		{"conversion before output", invalidTool + usage + done, 502, 0, 1, 4, 3},
		{"conversion after text", text + invalidTool + usage + done, 200, 0, 1, 4, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1beta/models/fixture:streamGenerateContent", strings.NewReader(`{"contents":[{"parts":[{"text":"test"}]}]}`))
			c.Params = gin.Params{{Key: "modelAction", Value: "/fixture:streamGenerateContent"}}
			var forward *service.OpenAIForwardResult
			var rawErr error
			handler.ServeGeminiViaChatCompletions(c, func(c *gin.Context) {
				resp := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.wire))}
				forward, rawErr = service.ForwardGeminiRawFixture(c, resp)
			})
			require.Equal(t, tc.status, rec.Code)
			wire := rec.Body.String()
			require.Equal(t, tc.stops, strings.Count(wire, `"STOP"`))
			require.Equal(t, tc.errors, strings.Count(wire, `"error"`))
			require.Equal(t, tc.errors > 0, rawErr != nil)
			require.NotNil(t, forward)
			require.False(t, forward.ClientDisconnect)
			require.Equal(t, tc.usageIn, forward.Usage.InputTokens)
			require.Equal(t, tc.usageOut, forward.Usage.OutputTokens)
			if tc.errors == 0 {
				require.Contains(t, wire, `"totalTokenCount":7`)
			} else if tc.status == 502 {
				require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
			} else {
				require.Contains(t, rec.Header().Get("Content-Type"), "text/event-stream")
			}
		})
	}
}
