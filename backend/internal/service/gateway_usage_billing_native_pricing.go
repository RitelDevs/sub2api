package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

// GeminiRequestPricingModel exposes the same final mapping used by ForwardGemini.
func (s *AntigravityGatewayService) GeminiRequestPricingModel(account *Account, model string, body []byte) string {
	return s.getMappedModelForThinkingLevel(account, model, geminiThinkingLevelFromBody(body))
}

// CheckGeminiTokenRequestPricing validates the final token billing model before
// sending a native request. Metadata and image generation keep their own paths.
func (s *GatewayService) CheckGeminiTokenRequestPricing(ctx context.Context, key *APIKey, model, upstreamModel, action string, body []byte, fields ChannelUsageFields, pricingAt time.Time) error {
	if action != "generateContent" && action != "streamGenerateContent" {
		return nil
	}
	if isGeminiImageGenerationIntent(body, model, upstreamModel) {
		return nil
	}
	if s == nil || s.billingService == nil {
		return ErrModelPricingUnavailable
	}
	result := &ForwardResult{Model: model, UpstreamModel: upstreamModel}
	billingModel := s.recordUsageBillingModel(ctx, key, result, fields)
	// Resolve/calculate even with zero tokens: missing prices are errors, while an
	// explicitly configured zero price or zero customer multiplier remains valid.
	_, err := s.calculateTokenCost(ctx, result, key, billingModel, 1, pricingAt)
	return err
}

// CheckDeepSeekTokenRequestPricing follows the native OpenAI-compatible token
// candidate order after account mapping. It performs no billing or routing.
func (s *OpenAIGatewayService) CheckDeepSeekTokenRequestPricing(ctx context.Context, key *APIKey, account *Account, model, forwardModel string, fields ChannelUsageFields, pricingAt time.Time) error {
	if account.Platform != PlatformDeepseek {
		return nil
	}
	mappedBillingModel, upstreamModel := resolveOpenAIForwardMappedModels(account, forwardModel, false)
	requestedModel := fields.OriginalModel
	if requestedModel == "" {
		requestedModel = model
	}
	if fields.APIKeyMappedModel != "" {
		requestedModel = fields.APIKeyMappedModel
	}
	channelModel := fields.ChannelMappedModel
	if channelModel == "" {
		channelModel = forwardModel
	}
	if !fields.ChannelMappingApplied && channelModel == fields.OriginalModel {
		channelModel = requestedModel
	}
	resultModel := forwardModel
	if !fields.ChannelMappingApplied && resultModel == fields.OriginalModel {
		resultModel = requestedModel
	}
	billingModel := mappedBillingModel
	if billingModel == "" {
		billingModel = forwardResultBillingModel(resultModel, upstreamModel)
	}
	if fields.BillingModelSource == BillingModelSourceChannelMapped && fields.ChannelMappedModel != "" &&
		((fields.APIKeyMappedModel != "" && fields.ChannelMappingApplied) || fields.ChannelMappedModel != fields.OriginalModel) {
		billingModel = fields.ChannelMappedModel
	}
	if fields.BillingModelSource == BillingModelSourceRequested && fields.OriginalModel != "" {
		billingModel = requestedModel
	}
	candidates := usageBillingModelCandidates(billingModel, mappedBillingModel, channelModel, requestedModel, upstreamModel, resultModel)
	var lastErr error = ErrModelPricingUnavailable
	for _, candidate := range candidates {
		_, err := s.calculateOpenAIRecordUsageTokenCost(ctx, key, candidate, 1, pricingAt, UsageTokens{}, "", "", nil, false)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return fmt.Errorf("check native DeepSeek token pricing: %w", lastErr)
}

// MessagesRequestPricingModel shares Forward's Claude thinking-model mapping.
func (s *AntigravityGatewayService) MessagesRequestPricingModel(account *Account, model string, body []byte) (string, error) {
	var request antigravity.ClaudeRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return "", err
	}
	mapped := s.getMappedModelForThinkingLevel(account, model, geminiThinkingLevelFromClaudeThinking(request.Thinking))
	enabled := request.Thinking != nil && (request.Thinking.Type == "enabled" || request.Thinking.Type == "adaptive")
	return applyThinkingModelSuffix(mapped, enabled), nil
}
