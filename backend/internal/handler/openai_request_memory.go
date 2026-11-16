package handler

import (
	"errors"
	"net/http"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIGatewayHandler) rejectOpenAIProcessingMemory(c *gin.Context, result *service.OpenAIForwardResult, err error, streamStarted, anthropic bool) bool {
	if result != nil || !errors.Is(err, pkghttputil.ErrRequestBodyMemoryBudget) {
		return false
	}
	c.Header("Retry-After", "1")
	message := "Request memory capacity is exhausted; retry later"
	if anthropic {
		h.anthropicStreamingAwareError(c, http.StatusServiceUnavailable, "overloaded_error", message, streamStarted)
	} else {
		h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "overloaded_error", message, streamStarted)
	}
	return true
}
