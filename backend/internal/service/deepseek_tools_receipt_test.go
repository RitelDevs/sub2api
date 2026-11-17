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
