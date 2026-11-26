package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiRejectsUnrepresentableToolTurnBeforeChat(t *testing.T) {
	const start = `{"contents":[{"role":"model","parts":[{"functionCall":{"id":"a","name":"chart"}},{"functionCall":{"id":"b","name":"chart"}}]},{"role":"user","parts":[`
	const a = `{"functionResponse":{"id":"a","name":"chart","response":{"output":"Paris"}}}`
	const b = `{"functionResponse":{"id":"b","name":"chart","response":{"output":"Tokyo"}}}`
	for _, tc := range []struct{ name, parts, want string }{
		{"interleaved", a + `,{"text":"first result is preliminary"},` + b, "part order"},
		{"nested-media", a + `,{"functionResponse":{"id":"b","name":"chart","response":{"output":"attached"},"parts":[{"inlineData":{"mimeType":"image/png","data":"cGlj"}}]}}`, "functionResponse.parts"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/model:generateContent", strings.NewReader(start+tc.parts+`]}]}`))
			c.Params = gin.Params{{Key: "modelAction", Value: "/model:generateContent"}}
			calls := 0
			ServeGeminiViaChatCompletions(c, func(*gin.Context) { calls++ })
			require.Zero(t, calls)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Contains(t, gjson.GetBytes(w.Body.Bytes(), "error.message").String(), tc.want)
		})
	}
}
