package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT62InheritsQualityProtectionWithoutChangingWireModel(t *testing.T) {
	for _, tc := range []struct{ model, protection string }{
		{"gpt-6.2-astra", "gpt-6-astra"},
		{"gpt-6.2-sol", "gpt-6.1-sol"},
		{"gpt-6.2-sol-high", "gpt-6.1-sol"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"model_mapping": map[string]any{"chosen": tc.model}},
				Extra:       map[string]any{QualityModelIsolationKey: map[string]any{tc.protection: map[string]any{"reason": "quality_rule:7"}}}}
			require.True(t, a.qualityModelBlocked(context.Background(), "chosen"))
			require.True(t, GatewayBorrowAccountQualityLinkedModel(tc.model))
			require.Equal(t, tc.model, a.GetMappedModel("chosen"))
			a.Extra = map[string]any{"model_rate_limits": map[string]any{tc.protection: map[string]any{"reason": "quality_rule:7"}}}
			require.True(t, a.qualityModelBlocked(context.Background(), "chosen"))
			a.Extra = map[string]any{GatewayBorrowModelsKey: []string{tc.protection}}
			require.True(t, a.RequiresGatewayBorrowUpstream(tc.model), "a new name cannot bypass the old fail-closed route requirement")
			delete(a.Extra, GatewayBorrowModelsKey)
			require.False(t, a.RequiresGatewayBorrowUpstream(tc.model), "new models do not enable borrowing")
			require.False(t, a.qualityModelBlocked(context.Background(), "chosen"))
			if tc.protection == "gpt-6-astra" {
				a.Extra["astra_model_disabled"] = true
				require.False(t, a.IsModelSupported("chosen"), "the existing Astra model-disable flag covers 6.2 aliases")
			}
		})
	}
}

func TestGPT62InheritsBPSRequirementAndInitialQualityGate(t *testing.T) {
	group := int64(12)
	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol"} {
		t.Run(model, func(t *testing.T) {
			a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6-astra": "gpt-6-astra", model: model}},
				Extra: map[string]any{
					ExcelBPSRequiredGroupIDsKey:           []int64{group},
					ExcelBPSRequiredModelsKey:             []string{openAIQualityProtectionModel(model)},
					"openai_excel_bps":                    false,
					GatewayBorrowAccountQualityModeKey:    GatewayBorrowAccountQualityMode,
					GatewayBorrowAccountQualityPendingKey: true,
				}}
			require.True(t, a.IsOpenAIGatewayAccountQualityPendingForModel(model))
			require.True(t, a.excelBPSRequiredForModel(model))
			require.False(t, a.excelBPSModelAllowedInGroup(&group, model), "BPS off remains fail closed")
			a.Extra["openai_excel_bps"] = true
			a.Extra["openai_excel_bps_models"] = []string{model}
			require.True(t, a.excelBPSModelAllowedInGroup(&group, model))
		})
	}
}

func TestGPT62AccountReconcilePreservesLegacyDegradedExclusions(t *testing.T) {
	cfg := reconcileModelConfig()
	cfg.ModelExclusions = nil
	template := cfg.Templates["prolite"]
	template.ModelMappings = append(template.ModelMappings,
		OAuthModelMappingRule{From: "gpt-6.2-astra", To: "gpt-6.2-astra"},
		OAuthModelMappingRule{From: "gpt-6.2-sol", To: "gpt-6.2-sol"})
	cfg.Templates["prolite"] = template
	a := reconcileModelAccount(map[string]any{"quality_model_results": map[string]any{"gpt-6-astra": "degraded", "gpt-6.1-sol": "degraded"}})
	got, ok := OAuthAccountModelMapping(cfg, a)
	require.True(t, ok)
	require.NotContains(t, got, "gpt-6.2-astra")
	require.NotContains(t, got, "gpt-6.2-sol")
	require.Equal(t, map[string]any{"gpt-6-astra": "degraded", "gpt-6.1-sol": "degraded"}, a.Extra["quality_model_results"], "reconciliation does not invent 6.2 test results")
}

func TestGPT62KeyReasoningCeilingStillApplies(t *testing.T) {
	for _, model := range []string{"gpt-6.2-astra", "gpt-6.2-sol"} {
		body := []byte(`{"model":"` + model + `","input":"hello"}`)
		updated, changed, err := ApplyAPIKeyOpenAIReasoningEffortPolicy(body, "high", "medium", nil, ReasoningEffortOverLimitDowngrade, model)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "medium", gjson.GetBytes(updated, "reasoning.effort").String())
		_, _, err = ApplyAPIKeyOpenAIReasoningEffortPolicy(body, "high", "medium", nil, ReasoningEffortOverLimitDeny, model)
		require.True(t, IsReasoningEffortPolicyDenied(err))
	}
}
