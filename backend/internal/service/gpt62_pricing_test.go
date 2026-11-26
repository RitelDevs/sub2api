package service

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestGPT62PricingCatalogAndFallback(t *testing.T) {
	data, err := os.ReadFile("../../resources/model-pricing/model_prices_and_context_window.json")
	require.NoError(t, err)
	catalog := &PricingService{}
	catalog.pricingData, err = catalog.parsePricingData(data)
	require.NoError(t, err)
	for source, svc := range map[string]*BillingService{
		"billing fallback": NewBillingService(&config.Config{}, nil),
		"pricing fallback": NewBillingService(&config.Config{}, &PricingService{pricingData: map[string]*LiteLLMModelPricing{}}),
		"catalog":          NewBillingService(&config.Config{}, catalog),
	} {
		t.Run(source, func(t *testing.T) {
			for model, factor := range map[string]float64{"gpt-6.2-sol": 1, "gpt-6.2-astra": 4} {
				for _, count := range []int{272000, 272001} {
					for tier, multiplier := range map[string]float64{"": 1, "fast": 2, "priority": 2, "flex": .5, "ultrafast": 3} {
						tokens := UsageTokens{InputTokens: count - 3000, OutputTokens: 500, CacheReadTokens: 2000,
							CacheCreationTokens: 1000, CacheCreation30mTokens: 400, CacheCreation12hTokens: 600}
						cost, err := svc.CalculateCostWithServiceTier(model, tokens, .7, tier)
						require.NoError(t, err, model)
						inputMultiplier, outputMultiplier := 1.0, 1.0
						if count > 272000 {
							inputMultiplier, outputMultiplier = 2, 1.5
						}
						require.InDelta(t, float64(count-3000)*2e-6*factor*inputMultiplier*multiplier, cost.InputCost, 1e-10, model)
						require.InDelta(t, 500*10e-6*factor*outputMultiplier*multiplier, cost.OutputCost, 1e-10, model)
						require.InDelta(t, 2000*.1e-6*factor*inputMultiplier*multiplier, cost.CacheReadCost, 1e-10, model)
						require.InDelta(t, (400*2.5e-6+600*5e-6)*factor*inputMultiplier*multiplier, cost.CacheCreationCost, 1e-10, model)
						require.InDelta(t, cost.TotalCost*.7, cost.ActualCost, 1e-10)
						require.Equal(t, count > 272000, cost.LongContextBillingApplied)
					}
				}
				alias, err := svc.GetModelPricing("openai/" + model + "-max")
				require.NoError(t, err)
				require.InDelta(t, 2e-6*factor, alias.InputPricePerToken, 1e-12)
			}
		})
	}
}

func TestGPT62UltrafastPromotionUsesRequestPricingTime(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	resolver := NewModelPricingResolver(nil, svc)
	boundary := time.Date(2027, time.March, 1, 8, 0, 0, 0, time.UTC)
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		factor := 1.0
		if model == "gpt-6.2-astra" {
			factor = 4
		}
		for _, tc := range []struct {
			at         time.Time
			multiplier float64
		}{
			{boundary.Add(-time.Nanosecond), 3}, {boundary, 6}, {boundary.Add(-time.Hour), 3},
		} {
			cost, err := svc.CalculateCostUnified(CostInput{
				Ctx: context.Background(), Model: model, RateMultiplier: 1, ServiceTier: "ultrafast",
				PricingAt: tc.at, Resolver: resolver,
				Tokens: UsageTokens{InputTokens: 1000, OutputTokens: 100, CacheCreationTokens: 1000, CacheCreation12hTokens: 1000},
			})
			require.NoError(t, err)
			require.InDelta(t, .008*factor*tc.multiplier, cost.TotalCost, 1e-12, model)
		}
	}
	// The 6.2 promotion must not change the existing Astra contract.
	price, err := svc.getModelPricingAt("gpt-6-astra", boundary.Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, 6.0, price.UltrafastMultiplier)
}

func TestGPT62PricingPreservesOperatorCards(t *testing.T) {
	svc := NewBillingService(&config.Config{}, nil)
	price := svc.applyModelSpecificPricingPolicyEx("gpt-6.2-astra", &ModelPricing{
		InputPricePerToken: 3e-6, OutputPricePerToken: 4e-6,
		CacheCreationPricePerToken: 0, CacheCreationPriceExplicit: true,
		CacheCreation12hPricePerToken: float64Ptr(7e-6), CacheReadPricePerToken: 0,
	}, false, time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC))
	cost := svc.computeTokenBreakdown(price, UsageTokens{InputTokens: 1000, OutputTokens: 1000,
		CacheCreationTokens: 1000, CacheCreation30mTokens: 500, CacheCreation12hTokens: 500}, 1, "ultrafast", true)
	require.InDelta(t, (.003+.004+.0035)*3, cost.TotalCost, 1e-12)
	require.Zero(t, price.CacheCreationPricePerToken)
	require.Equal(t, 7e-6, *price.CacheCreation12hPricePerToken)
}

func TestGPT62SubscriptionLongContextExemptionByCredential(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, typ := range []string{AccountTypeOAuth, AccountTypeSetupToken, AccountTypeAPIKey} {
			require.Equal(t, typ != AccountTypeAPIKey, openAISubscriptionLongContextExempt(&Account{Platform: PlatformOpenAI, Type: typ},
				&OpenAIForwardResult{Model: model, UpstreamModel: model}, UsageTokens{InputTokens: 500001}))
		}
	}
}
