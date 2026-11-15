//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNativeGeminiTokenPricingChecksResolvedSources(t *testing.T) {
	const unknown = "gemini-unpriced-regression-model"
	zero, paid := 0.0, 2e-6
	for _, tc := range []struct {
		name, model, upstream, action string
		body                          []byte
		cards                         []ChannelModelPricing
		wantMissing                   bool
	}{
		{name: "missing", model: unknown, action: "generateContent", wantMissing: true},
		{name: "missing_stream", model: unknown, action: "streamGenerateContent", wantMissing: true},
		{name: "mapped_default", model: unknown, upstream: "gemini-4-argon", action: "generateContent"},
		{name: "group_paid_alias", model: unknown, action: "generateContent", cards: []ChannelModelPricing{{Models: []string{unknown}, InputPrice: &paid, OutputPrice: &paid}}},
		{name: "group_explicit_free", model: unknown, action: "generateContent", cards: []ChannelModelPricing{{Models: []string{unknown}, InputPrice: &zero, OutputPrice: &zero, CacheReadPrice: &zero}}},
		{name: "group_wildcard_free", model: unknown + "-high", action: "generateContent", cards: []ChannelModelPricing{{Models: []string{unknown + "*"}, InputPrice: &zero, OutputPrice: &zero}}},
		{name: "metadata_unchanged", model: unknown, action: "countTokens"},
		{name: "image_model_unchanged", model: "gemini-unpriced-image-preview", action: "generateContent"},
		{name: "image_modality_unchanged", model: unknown, action: "generateContent", body: []byte(`{"generationConfig":{"responseModalities":["IMAGE"]}}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			billing := NewBillingService(&config.Config{}, nil)
			svc := &GatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
			gid := int64(21)
			key := &APIKey{GroupID: &gid, Group: &Group{ID: gid, Platform: PlatformGemini, ModelPricing: tc.cards}}
			err := svc.CheckGeminiTokenRequestPricing(context.Background(), key, tc.model, tc.upstream, tc.action, tc.body, ChannelUsageFields{}, time.Time{})
			if tc.wantMissing {
				require.ErrorIs(t, err, ErrModelPricingUnavailable)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestNativeTokenMissingPricingDoesNotWriteSuccessfulZeroUsage(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	users := &openAIRecordUsageUserRepoStub{}
	subs := &openAIRecordUsageSubRepoStub{}
	charges := &openAIRecordUsageBillingRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(logs, users, subs)
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	svc.usageBillingRepo = charges
	gid := int64(21)
	key := &APIKey{ID: 1, GroupID: &gid, Group: &Group{ID: gid, Platform: PlatformGemini, RateMultiplier: 1}}
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		APIKey: key, User: &User{ID: 2}, Account: &Account{ID: 3, Platform: PlatformGemini, Type: AccountTypeAPIKey},
		Result: &ForwardResult{RequestID: "missing-native-price", Model: "gemini-unpriced-regression-model", Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 50}, SearchCount: 1},
	})
	require.ErrorIs(t, err, ErrModelPricingUnavailable)
	require.Zero(t, logs.calls, "an unresolved bill must not look like successful free usage")
	require.Zero(t, charges.calls, "do not consume the billing idempotency key on missing pricing")
	require.Zero(t, users.deductCalls)
}

func TestNativeTokenExplicitFreeStillRecordsUsage(t *testing.T) {
	logs := &openAIRecordUsageLogRepoStub{inserted: true}
	users := &openAIRecordUsageUserRepoStub{}
	svc := newGatewayRecordUsageServiceForTest(logs, users, &openAIRecordUsageSubRepoStub{})
	svc.resolver = NewModelPricingResolver(nil, svc.billingService)
	gid := int64(21)
	zero := 0.0
	key := &APIKey{ID: 1, GroupID: &gid, Group: &Group{ID: gid, Platform: PlatformGemini, RateMultiplier: 1,
		ModelPricing: []ChannelModelPricing{{Models: []string{"gemini-unpriced-regression-model"}, InputPrice: &zero, OutputPrice: &zero}}}}
	err := svc.RecordUsage(context.Background(), &RecordUsageInput{
		APIKey: key, User: &User{ID: 2}, Account: &Account{ID: 3, Platform: PlatformGemini, Type: AccountTypeAPIKey},
		Result: &ForwardResult{RequestID: "explicit-free-native-price", Model: "gemini-unpriced-regression-model", Usage: ClaudeUsage{InputTokens: 100, OutputTokens: 50}},
	})
	require.NoError(t, err)
	require.Equal(t, 1, logs.calls)
	require.Equal(t, 100, logs.lastLog.InputTokens)
	require.Zero(t, logs.lastLog.ActualCost)
	require.Zero(t, users.deductCalls)
}

func TestNativeDeepSeekTokenPricingAfterMapping(t *testing.T) {
	billing := NewBillingService(&config.Config{}, nil)
	svc := &OpenAIGatewayService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
	gid := int64(1)
	zero := 0.0
	key := &APIKey{GroupID: &gid, Group: &Group{ID: gid, Platform: PlatformDeepseek}}
	account := &Account{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey}
	fields := ChannelUsageFields{OriginalModel: "native-unpriced-regression"}
	require.ErrorIs(t, svc.CheckDeepSeekTokenRequestPricing(context.Background(), key, account, fields.OriginalModel, fields.OriginalModel, fields, time.Time{}), ErrModelPricingUnavailable)
	account.Credentials = map[string]any{"model_mapping": map[string]any{fields.OriginalModel: "deepseek-v4-pro"}}
	require.NoError(t, svc.CheckDeepSeekTokenRequestPricing(context.Background(), key, account, fields.OriginalModel, fields.OriginalModel, fields, time.Time{}))
	account.Credentials = nil
	key.Group.ModelPricing = []ChannelModelPricing{{Models: []string{fields.OriginalModel}, InputPrice: &zero, OutputPrice: &zero}}
	require.NoError(t, svc.CheckDeepSeekTokenRequestPricing(context.Background(), key, account, fields.OriginalModel, fields.OriginalModel, fields, time.Time{}))
}
