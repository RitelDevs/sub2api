package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayErrorPrivacyPreservesInternalErrorCapture(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.GatewayErrorPrivacy(), OpsErrorLoggerMiddleware(nil))
	var captured []byte
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Header("X-Request-ID", "private-upstream-id")
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"message":    "Context window is full. (request id: private-upstream-id)",
			"request_id": "private-upstream-id",
		}})
		captureWriter, ok := c.Writer.(*opsCaptureWriter)
		require.True(t, ok)
		captured = append([]byte(nil), captureWriter.capturedBytes()...)
	})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Equal(t, http.StatusBadRequest, response.Code)
	require.Contains(t, string(captured), "private-upstream-id")
	require.NotContains(t, response.Body.String(), "private-upstream-id")
	require.Empty(t, response.Header().Get("X-Request-ID"))
	require.JSONEq(t, `{"error":{"message":"Context window is full."}}`, response.Body.String())
}
