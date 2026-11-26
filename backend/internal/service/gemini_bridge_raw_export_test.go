//go:build unit

package service

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// This test-only entry connects the real Chat pump to the public Gemini handler.
func ForwardGeminiRawFixture(c *gin.Context, resp *http.Response) (*OpenAIForwardResult, error) {
	return (&OpenAIGatewayService{}).streamRawChatCompletions(c, resp, &Account{ID: 101}, "fixture", "fixture", "fixture", nil, nil, time.Now(), 64)
}
