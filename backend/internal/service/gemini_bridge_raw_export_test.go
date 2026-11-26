//go:build unit

package service

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// This test-only entry connects the real Chat pump to the public Gemini handler.
func ForwardGeminiRawFixture(c *gin.Context, resp *http.Response) (*OpenAIForwardResult, error) {
	s := &OpenAIGatewayService{}
	account := &Account{ID: 101}
	if resp.Header.Get("Content-Type") == "application/json" {
		return s.bufferRawChatCompletions(c, resp, account, "fixture", "fixture", "fixture", nil, nil, time.Now())
	}
	return s.streamRawChatCompletions(c, resp, account, "fixture", "fixture", "fixture", nil, nil, time.Now(), 64)
}
