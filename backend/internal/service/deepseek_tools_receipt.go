package service

import (
	"context"
	"errors"
	"strings"
	"time"
)

// RecordConfirmedEmptySearch preserves the existing no-charge policy for a
// successfully adapted, explicitly empty search result. It creates an atomic
// zero receipt so callers need not infer free use from an absent log.
func (s *OpenAIGatewayService) RecordConfirmedEmptySearch(ctx context.Context, in *OpenAIRecordUsageInput) error {
	if s == nil || s.usageBillingRepo == nil || in == nil || in.Result == nil || in.APIKey == nil || in.User == nil || in.Account == nil ||
		in.Result.Model != "deepseek-search" || in.Result.WebSearchCalls != 0 {
		return errors.New("invalid confirmed empty search receipt")
	}
	requestID := resolveUsageBillingRequestID(ctx, in.Result.RequestID)
	mode, inbound, upstream := string(BillingModePerRequest), in.InboundEndpoint, in.UpstreamEndpoint
	log := &UsageLog{UserID: in.User.ID, APIKeyID: in.APIKey.ID, AccountID: in.Account.ID, RequestID: requestID,
		Model: "deepseek-search", RequestedModel: "deepseek-search", GroupID: in.APIKey.GroupID,
		RateMultiplier: 1, BillingMode: &mode, CreatedAt: time.Now().UTC(),
		InboundEndpoint: &inbound, UpstreamEndpoint: &upstream}
	_, err := applyUsageBilling(ctx, requestID, log, &postUsageBillingParams{
		Cost: &CostBreakdown{BillingMode: string(BillingModePerRequest)}, User: in.User, APIKey: in.APIKey, Account: in.Account,
		RequestPayloadHash: in.RequestPayloadHash, APIKeyService: in.APIKeyService, AccountRateMultiplier: 1,
	}, s.billingDeps(), s.usageBillingRepo)
	if err != nil {
		return err
	}
	writeUsageLogBestEffort(ctx, s.usageLogRepo, log, "service.openai_gateway.empty_search")
	return nil
}

// RecordConfirmedUnbilledDeepSeek records the customer's final no-charge
// outcome, not the provider's cost. The coordinator calls this only after it
// has chosen no successful/partial billing branch; failed attempt diagnostics
// and unknown provider usage never independently authorize this receipt.
func (s *OpenAIGatewayService) RecordConfirmedUnbilledDeepSeek(ctx context.Context, in *OpenAIRecordUsageInput) error {
	if s == nil || s.usageBillingRepo == nil || in == nil || in.Result == nil || in.APIKey == nil || in.User == nil || in.Account == nil ||
		!strings.HasPrefix(strings.ToLower(in.Result.Model), "deepseek-") || in.Result.UsageUnavailable || in.Result.RequestID != "" || in.Result.Usage != (OpenAIUsage{}) {
		return errors.New("invalid confirmed unbilled DeepSeek receipt")
	}
	requestID := resolveUsageBillingRequestID(ctx, in.Result.RequestID)
	mode, inbound, upstream := string(BillingModePerRequest), in.InboundEndpoint, in.UpstreamEndpoint
	usage := &UsageLog{UserID: in.User.ID, APIKeyID: in.APIKey.ID, AccountID: in.Account.ID, RequestID: requestID,
		Model: in.Result.Model, RequestedModel: in.Result.Model, GroupID: in.APIKey.GroupID,
		RateMultiplier: 1, BillingMode: &mode, CreatedAt: time.Now().UTC(),
		InboundEndpoint: &inbound, UpstreamEndpoint: &upstream}
	_, err := applyUsageBilling(ctx, requestID, usage, &postUsageBillingParams{
		Cost: &CostBreakdown{BillingMode: string(BillingModePerRequest)}, User: in.User, APIKey: in.APIKey, Account: in.Account,
		RequestPayloadHash: in.RequestPayloadHash, APIKeyService: in.APIKeyService, AccountRateMultiplier: 1,
	}, s.billingDeps(), s.usageBillingRepo)
	if err != nil {
		return err
	}
	writeUsageLogBestEffort(ctx, s.usageLogRepo, usage, "service.openai_gateway.unbilled_deepseek")
	return nil
}
