//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekNativeMissingPricingRejectsBeforeUpstream(t *testing.T) {
	for _, protocol := range []string{"chat", "messages", "responses"} {
		for _, model := range []string{"deepseek-unpriced-regression-model", "native-unpriced-regression"} {
			t.Run(protocol+"/"+model, func(t *testing.T) {
				upstream := &openAIImagesFailoverHTTPUpstream{}
				usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 1)}
				cfg := &config.Config{RunMode: config.RunModeSimple}
				cfg.Default.RateMultiplier = 1
				cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, cfg, nil)
				t.Cleanup(cache.Stop)
				// Make the intentionally unpriced model routable, so this tests
				// pricing admission rather than the scheduler's model allowlist. An empty
				// billing service also removes the broad DeepSeek family fallback.
				account := service.Account{ID: 1, Name: "native-pricing-test", Platform: service.PlatformDeepseek,
					Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, GroupIDs: []int64{17},
					Credentials: map[string]any{"api_key": "fixture", "base_url": "https://deepseek.example.test", "api_protocol": "chat_completions",
						"model_mapping": map[string]any{model: model}}}
				slots := service.NewConcurrencyService(&concurrencyCacheMock{acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }})
				svc := service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}, nil, usage, nil, nil, nil, nil, nil,
					cfg, nil, slots, &service.BillingService{}, nil, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
				h := NewOpenAIGatewayHandler(svc, slots, cache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)

				body := `{"model":"` + model + `","stream":true,"max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`
				response := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(response)
				path := map[string]string{"chat": "/v1/chat/completions", "messages": "/v1/messages", "responses": "/v1/responses"}[protocol]
				c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				groupID := account.GroupIDs[0]
				c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 99, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: service.PlatformDeepseek, RateMultiplier: 1}, User: &service.User{ID: 100}})
				c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100})
				if protocol == "chat" {
					h.ChatCompletions(c)
				} else if protocol == "messages" {
					h.Messages(c)
				} else {
					h.Responses(c)
				}
				require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "model_pricing_unavailable")
				require.Empty(t, upstream.calls(), "pricing admission rejects before upstream I/O")
				require.Empty(t, usage.created)
			})
		}
	}
}
