//go:build unit

package handler

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekNativeMissingPricingRejectsBeforeUpstream(t *testing.T) {
	for _, protocol := range []string{"chat", "messages", "responses"} {
		for _, model := range []string{"deepseek-unpriced-regression-model", "native-unpriced-regression"} {
			t.Run(protocol+"/"+model, func(t *testing.T) {
				upstream := &deepSeekTestUpstream{response: func(_ int, _ *http.Request) (int, string) { return 200, deepSeekSuccess() }}
				h, c, response, usage := deepSeekHandlerFixture(t, protocol, upstream)
				// Make the intentionally unpriced model routable, so this tests
				// pricing admission rather than the scheduler's model allowlist. An empty
				// billing service also removes the broad DeepSeek family fallback.
				account := service.Account{ID: 1, Name: "native-pricing-test", Platform: service.PlatformDeepseek,
					Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, Priority: 1, Concurrency: 1, GroupIDs: []int64{17},
					Credentials: map[string]any{"api_key": "fixture", "base_url": "https://deepseek.example.test", "api_protocol": "chat_completions",
						"model_mapping": map[string]any{model: model}}}
				slots := service.NewConcurrencyService(&concurrencyCacheMock{acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }})
				h.gatewayService = service.NewOpenAIGatewayService(openAIImagesFailoverAccountRepo{accounts: []service.Account{account}}, nil, usage, nil, nil, nil, nil, nil,
					h.cfg, nil, slots, &service.BillingService{}, nil, h.billingCacheService, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)

				body := `{"model":"` + model + `","stream":true,"max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`
				c.Request.Body = io.NopCloser(strings.NewReader(body))
				c.Request.ContentLength = int64(len(body))
				if protocol == "chat" {
					h.ChatCompletions(c)
				} else if protocol == "messages" {
					h.Messages(c)
				} else {
					c.Request.URL.Path = "/v1/responses"
					h.Responses(c)
				}
				require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
				require.Contains(t, response.Body.String(), "model_pricing_unavailable")
				require.Empty(t, upstream.ids, "native requests must reject before upstream I/O")
				require.Empty(t, usage.created)
			})
		}
	}
}
