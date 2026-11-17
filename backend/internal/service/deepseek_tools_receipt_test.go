package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestConfirmedEmptySearchProducesCanonicalZeroReceipt(t *testing.T) {
	usage := &openAIRecordUsageLogRepoStub{inserted: true}
	billing := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usage, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.usageBillingRepo = billing
	input := &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{Model: "deepseek-search", WebSearchCalls: 0},
		APIKey: &APIKey{ID: 82}, User: &User{ID: 7}, Account: &Account{ID: 9}, RequestPayloadHash: "empty-search"}
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "child-empty")
	require.NoError(t, svc.RecordConfirmedEmptySearch(ctx, input))
	require.Equal(t, "client:child-empty", billing.lastCmd.RequestID)
	require.Equal(t, int64(82), billing.lastCmd.APIKeyID)
	require.NotNil(t, billing.lastCmd.QuotedActualCost)
	require.Zero(t, *billing.lastCmd.QuotedActualCost)
	require.Zero(t, billing.lastCmd.BalanceCost)
	input.Result.WebSearchCalls = 1
	require.Error(t, svc.RecordConfirmedEmptySearch(ctx, input))
}

func TestConfirmedUnbilledDeepSeekProducesCanonicalZeroReceipt(t *testing.T) {
	usage := &openAIRecordUsageLogRepoStub{inserted: true}
	billing := &openAIRecordUsageBillingRepoStub{}
	svc := newOpenAIRecordUsageServiceForTest(usage, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{}, nil)
	svc.usageBillingRepo = billing
	in := &OpenAIRecordUsageInput{Result: &OpenAIForwardResult{Model: "deepseek-v4.1-flash"}, APIKey: &APIKey{ID: 667}, User: &User{ID: 1}, Account: &Account{ID: 258}, RequestPayloadHash: "confirmed-final-failed"}
	ctx := context.WithValue(context.Background(), ctxkey.ClientRequestID, "failed-final")
	require.NoError(t, svc.RecordConfirmedUnbilledDeepSeek(ctx, in))
	require.Equal(t, "client:failed-final", billing.lastCmd.RequestID)
	require.Zero(t, billing.lastCmd.BalanceCost)
	require.NotNil(t, billing.lastCmd.QuotedActualCost)
	require.Zero(t, *billing.lastCmd.QuotedActualCost)
	in.Result.Usage.InputTokens = 1
	require.Error(t, svc.RecordConfirmedUnbilledDeepSeek(ctx, in))
	in.Result.Usage = OpenAIUsage{}
	in.Result.UsageUnavailable = true
	require.Error(t, svc.RecordConfirmedUnbilledDeepSeek(ctx, in))
}
