//go:build unit

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGPT62ModelIdentityAndDescriptor(t *testing.T) {
	for _, base := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, model := range []string{base, "openai/" + base + "-max", base + "-2026-11-17"} {
			require.Equal(t, base, normalizeCodexModel(model))
			require.Equal(t, base, normalizeKnownOpenAICodexModel(model))
			require.Equal(t, base, NormalizeOpenAICompatRequestedModel(model))
		}
		descriptor := newConfiguredCodexModelDescriptor(base)
		require.EqualValues(t, 1_050_000, descriptor.ContextWindow)
		require.EqualValues(t, 1_050_000, descriptor.MaxContextWindow)
		require.EqualValues(t, 128_000, descriptor.MaxOutputTokens)
		require.Equal(t, []string{"text", "image"}, descriptor.InputModalities)
		require.Empty(t, descriptor.officialMetadata)
		var levels []string
		for _, level := range descriptor.SupportedReasoningLevels {
			levels = append(levels, level.Effort)
		}
		require.Equal(t, []string{"low", "medium", "high", "xhigh", "max"}, levels)
		require.True(t, configuredCodexSupportsUltrafastServiceTier(base))
	}
	require.Equal(t, "gpt-6-astra", normalizeCodexModel("gpt-6-astra"))
	require.Equal(t, "gpt-6.1-sol", normalizeCodexModel("gpt-6.1-sol"))
}

func TestGPT62MappedResponsesRemovesUnsupportedParameters(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		body := []byte(`{"model":"public-alias","reasoning":{"effort":"none"},"temperature":0.8,"top_p":0.9,"top_logprobs":2,"logprobs":true,"include":["reasoning.encrypted_content","message.output_text.logprobs"],"prompt_cache_retention":"24h"}`)
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"public-alias": model}}}
		normalized, changed, err := normalizeOpenAIResponsesCompatibilityBodyWithOptions(body, account, openAIResponsesCompatibilityOptions{})
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "low", gjson.GetBytes(normalized, "reasoning.effort").String())
		require.Equal(t, "30m", gjson.GetBytes(normalized, "prompt_cache_options.ttl").String())
		for _, key := range []string{"temperature", "top_p", "top_logprobs", "logprobs", "prompt_cache_retention"} {
			require.False(t, gjson.GetBytes(normalized, key).Exists(), key)
		}
		require.Equal(t, []any{"reasoning.encrypted_content"}, gjson.GetBytes(normalized, "include").Value())
		_, _, err = normalizeGPT6ResponsesSampling([]byte(`{"reasoning":{"effort":"ultra"}}`), model)
		require.ErrorContains(t, err, "does not support reasoning effort")
	}
}

func TestGPT62RawChatFallbackRejectsTools(t *testing.T) {
	for _, model := range []string{"gpt-6.2-sol", "gpt-6.2-astra"} {
		for _, protocol := range []string{"chat", "responses", "messages"} {
			body := `{"model":"public","messages":[{"role":"user","content":"hi"}],"input":"hi","max_tokens":100,"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"}}}]}`
			if protocol == "responses" {
				body = `{"model":"public","input":"hi","tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`
			}
			if protocol == "messages" {
				body = `{"model":"public","max_tokens":100,"messages":[{"role":"user","content":"hi"}],"tools":[{"name":"lookup","input_schema":{"type":"object"}}]}`
			}
			recorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(recorder)
			ginContext.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, strings.NewReader(body))
			account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"model_mapping": map[string]any{"public": model}}}
			service := &OpenAIGatewayService{cfg: &config.Config{}}
			var err error
			switch protocol {
			case "chat":
				_, err = service.forwardAsRawChatCompletions(context.Background(), ginContext, account, []byte(body), "")
			case "responses":
				_, err = service.forwardResponsesViaRawChatCompletions(context.Background(), ginContext, account, []byte(body), time.Now())
			case "messages":
				_, err = service.forwardAnthropicViaRawChatCompletions(context.Background(), ginContext, account, []byte(body), "")
			}
			require.ErrorContains(t, err, model+" requires Responses")
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		}
	}
}
