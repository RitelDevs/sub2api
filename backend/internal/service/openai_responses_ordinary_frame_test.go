//go:build unit

package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestNativeOrdinaryResponsesJoinBeforeTerminalRewrite(t *testing.T) {
	const delta = "\xef\xbb\xbfdata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\r\r"
	const terminal = "data: {\r: before-id\rdata: \"response\":{\"id\":\"resp_join\",\"status\":\"completed\",\"output\":[],\rdata: \"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\revent: response.completed\r"
	for _, tc := range []struct {
		name      string
		ending    string
		readError bool
	}{
		{"terminated", "\r", false},
		{"clean-eof", "", false},
		{"failed-read", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			var body io.Reader = strings.NewReader(delta + terminal + tc.ending)
			if tc.readError {
				body = io.MultiReader(body, iotest.ErrReader(io.ErrUnexpectedEOF))
			}
			up := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(body)}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			res, err := svc.handleStreamingResponse(c.Request.Context(), up, c, account, time.Now(), "model", "model")
			if tc.readError {
				require.Error(t, err)
				require.Equal(t, "resp_join", res.responseID)
				require.Equal(t, 3, res.usage.InputTokens)
				require.Equal(t, 4, res.usage.OutputTokens)
				require.NotContains(t, rec.Body.String(), `resp_join`)
				require.False(t, OpsStreamCompletedForAccount(c, account.ID))
				return
			}
			require.NoError(t, err)
			require.Equal(t, "resp_join", res.responseID)
			require.Equal(t, 3, res.usage.InputTokens)
			require.Equal(t, 4, res.usage.OutputTokens)
			require.True(t, OpsStreamCompletedForAccount(c, account.ID))
			var completions int
			forEachOpenAISSEFrame(rec.Body.String(), func(kind string, data []byte) {
				require.True(t, json.Valid(data), "invalid downstream JSON: %q", data)
				if kind == "response.completed" {
					completions++
					require.Equal(t, "hello", gjson.GetBytes(data, "response.output.0.content.0.text").String())
				}
			})
			require.Equal(t, 1, completions)
		})
	}
}
