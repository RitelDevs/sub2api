package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutesLongContextBlocksBeforeHandler(t *testing.T) {
	for _, path := range []string{
		"/v1/responses", "/v1/responses/compact", "/responses", "/responses/compact",
		"/backend-api/codex/responses", "/backend-api/codex/responses/compact",
		"/v1/chat/completions", "/chat/completions", "/v1/messages", "/v1/systemone", "/antigravity/v1/messages",
	} {
		t.Run(path, func(t *testing.T) {
			router := newGatewayRoutesTestRouterWithGroup(&service.Group{Platform: service.PlatformOpenAI})
			request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5.4","input":"hi","previous_response_id":"resp_fixture"}`))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "context_length_exceeded")
		})
	}
}
