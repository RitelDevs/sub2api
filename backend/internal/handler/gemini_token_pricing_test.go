//go:build unit

package handler

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGeminiNativeMissingPricingRejectsBeforeUpstream(t *testing.T) {
	for _, model := range []string{"gemini-unpriced-regression-model", "gemini-2.5-pro"} {
		for _, action := range []string{"generateContent", "streamGenerateContent"} {
			t.Run(model+"/"+action, func(t *testing.T) {
				env := newCachedBillingEnv(t, 500, 900)
				upstream := &boundRetryUpstream{}
				env.h.geminiCompatService = service.NewGeminiMessagesCompatService(&cachedBillingAccountRepo{accounts: env.accounts}, nil, nil, nil, nil, nil, upstream, nil, &config.Config{})
				path := "/v1beta/models/" + model + ":" + action
				response := env.serve(http.MethodPost, path, `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`,
					gin.Params{{Key: "modelAction", Value: "/" + model + ":" + action}}, env.h.GeminiV1BetaModels)
				if model == "gemini-unpriced-regression-model" {
					require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
					require.Contains(t, response.Body.String(), "Model pricing is unavailable")
					require.Empty(t, upstream.requestBodies(), "missing pricing must not consume upstream tokens")
					require.Empty(t, env.usage.totals())
				} else {
					require.NotEmpty(t, upstream.requestBodies(), "a priced request must reach the existing forwarder")
				}
			})
		}
	}
}
