package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGPT62AdaptersKeepToolsAndNormalizeReasoning(t *testing.T) {
	temperature := 0.7
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, effort := range []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"} {
			chat, err := ChatCompletionsToResponses(&ChatCompletionsRequest{
				Model: model, ReasoningEffort: effort, Temperature: &temperature, TopP: &temperature,
				Tools: []ChatTool{{Type: "function", Function: &ChatFunction{Name: "lookup"}}},
			})
			require.NoError(t, err)
			require.Equal(t, model, chat.Model)
			require.Nil(t, chat.Temperature)
			require.Nil(t, chat.TopP)
			require.Len(t, chat.Tools, 1)
			expected := effort
			if effort == "none" || effort == "minimal" {
				expected = "low"
			}
			require.Equal(t, expected, chat.Reasoning.Effort)
			anthropic, err := AnthropicToResponses(&AnthropicRequest{
				Model: model, Temperature: &temperature, TopP: &temperature,
				OutputConfig: &AnthropicOutputConfig{Effort: effort},
			})
			require.NoError(t, err)
			require.Equal(t, expected, anthropic.Reasoning.Effort)
			require.Nil(t, anthropic.Temperature)
			require.Nil(t, anthropic.TopP)
		}
	}
}
