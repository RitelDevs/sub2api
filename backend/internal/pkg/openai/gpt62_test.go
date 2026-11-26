package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPT62ModelSpellingKeepsVersionIdentity(t *testing.T) {
	for _, base := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, model := range []string{base, "openai/" + base, base + "-max", base + "-openai-compact", base + "-2026-11-17"} {
			require.Equal(t, base, CanonicalGPT62Model(model))
			require.True(t, RequiresResponsesForToolCalls(model))
			require.False(t, IsGPT61SolModelSpelling(model))
			require.False(t, IsGPT6SolOrLunaModelSpelling(model))
		}
	}
	require.Equal(t, "gpt-6.2-astra", CanonicalGPT62Model("openai/GPT_6.2_ASTRA"))
	for _, model := range []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6.2-astra-pro", "gpt-6.2-solar", "gpt-6.2-sol-2026-xx-17"} {
		require.False(t, IsGPT62ModelSpelling(model))
	}
}

func TestGPT62PublishedReasoningScale(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, effort := range []string{"low", "medium", "high", "xhigh", "max"} {
			require.NoError(t, ValidateReasoningEffortForModel(model, effort))
		}
		for _, effort := range []string{"none", "minimal", "ultra"} {
			require.Error(t, ValidateReasoningEffortForModel(model, effort))
		}
	}
	require.NoError(t, ValidateReasoningEffortForModel("gpt-6.1-terra", "none"))
}
