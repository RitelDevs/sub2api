//go:build unit

package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// The catalog and offline fallback must charge Gemini token partitions equally.
func TestGemini4ModelsChargeTokens(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "resources", "model-pricing", "model_prices_and_context_window.json"))
	require.NoError(t, err)
	ps := &PricingService{}
	ps.pricingData, err = ps.parsePricingData(raw)
	require.NoError(t, err)
	for _, dynamic := range []bool{false, true} {
		var svc *BillingService
		if dynamic {
			svc = NewBillingService(&config.Config{}, ps)
		} else {
			svc = NewBillingService(&config.Config{}, nil)
		}
		for _, tc := range []struct {
			model               string
			input, output, read float64
		}{
			{"gemini-4-carbon", 2, 10, .1},
			{"gemini-4-carbon-high", 2, 10, .1},
			{"models/gemini-4-carbon-low", 2, 10, .1},
			{"gemini-4-hydrogen", 1.5, 7.5, .075},
			{"gemini-4-hydrogen-xhigh", 1.5, 7.5, .075},
		} {
			mode := "offline"
			if dynamic {
				mode = "catalog"
			}
			t.Run(mode+"/"+tc.model, func(t *testing.T) {
				cost, err := svc.CalculateCost(tc.model, UsageTokens{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadTokens: 1_000_000}, .6)
				require.NoError(t, err)
				require.InDelta(t, tc.input, cost.InputCost, 1e-12)
				require.InDelta(t, tc.output, cost.OutputCost, 1e-12)
				require.InDelta(t, tc.read, cost.CacheReadCost, 1e-12)
				require.InDelta(t, (tc.input+tc.output+tc.read)*.6, cost.ActualCost, 1e-12)
			})
		}
		for _, model := range []string{"kimi-k3.1-pro", "hy3", "hy4", "hy4-preview", "mimo-v2.6-pro", "mimo-v2.6-flash"} {
			_, err := svc.CalculateCost(model, UsageTokens{InputTokens: 100}, 1)
			require.ErrorIs(t, err, ErrModelPricingUnavailable, model)
		}
	}
}

func TestNewGeminiTierKeepsExplicitCatalogPrice(t *testing.T) {
	base := &LiteLLMModelPricing{InputCostPerToken: 2e-6, OutputCostPerToken: 10e-6}
	specific := &LiteLLMModelPricing{InputCostPerToken: 3e-6, OutputCostPerToken: 15e-6}
	ps := &PricingService{pricingData: map[string]*LiteLLMModelPricing{"gemini-4-carbon": base, "gemini-4-carbon-high": specific}}
	require.Same(t, specific, ps.GetModelPricing("gemini-4-carbon-high"))
	require.Same(t, base, ps.GetModelPricing("gemini-4-carbon-low"))
	svc := NewBillingService(&config.Config{}, nil)
	for _, model := range []string{"hy4-unknown", "kimi-k3.1-unknown", "mimo-v2.6-unknown", "gemini-4-carbon-unknown"} {
		_, err := svc.CalculateCost(model, UsageTokens{InputTokens: 100}, 1)
		require.ErrorIs(t, err, ErrModelPricingUnavailable)
	}
}
