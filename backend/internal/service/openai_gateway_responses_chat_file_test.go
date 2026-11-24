//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestForwardResponsesChatFallbackInputFile(t *testing.T) {
	for _, tc := range []struct{ name, fields, problem string }{
		{"inline", `"filename":"notes.pdf","file_data":"data:application/pdf;base64,JVBERi0="`, ""},
		{"external URL", `"file_url":"https://example.org/notes.pdf"`, "file_url"},
		{"unknown owner", `"file_id":"file-upload-1"`, "file_id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-5.4","input":[{"role":"user","content":[{"type":"input_text","text":"read"},{"type":"input_file",` + tc.fields + `},{"type":"input_text","text":"summarize"}]}]}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"fixture","object":"chat.completion","model":"gpt-5.4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`)),
			}}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			result, err := svc.Forward(context.Background(), c, forceChatResponsesFallbackAccount(), body)
			if tc.problem != "" {
				require.ErrorContains(t, err, tc.problem)
				require.Nil(t, result)
				require.Nil(t, upstream.lastReq)
				require.Equal(t, http.StatusBadRequest, rec.Code)
				require.Contains(t, rec.Body.String(), tc.problem)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			require.JSONEq(t, `[{"type":"text","text":"read"},{"type":"file","file":{"filename":"notes.pdf","file_data":"data:application/pdf;base64,JVBERi0="}},{"type":"text","text":"summarize"}]`, gjson.GetBytes(upstream.lastBody, "messages.0.content").Raw)
		})
	}
}
