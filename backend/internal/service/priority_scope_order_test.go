package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPriorityDefaultEffortOrderUsesRequestEvidence(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetOpenAIAdvancedSchedulerSettingCacheForTest)
	cfg := DefaultPrioritySchedulingConfig()
	cfg.Enabled = true
	reader := &priorityReaderStub{signal: map[int64]PrioritySchedulingSignal{1: {}, 2: {}}}
	gateway := priorityGateway(cfg, reader)
	gateway.cfg = newOpenAIWSV2TestConfig()
	gateway.cfg.Gateway.OpenAIWS.LBTopK = 1
	weights := &gateway.cfg.Gateway.OpenAIWS.SchedulerScoreWeights
	weights.ErrorRate, weights.TTFT, weights.SessionSticky, weights.PreviousResponse = 10, 0, 0, 0
	scheduler := &defaultOpenAIAccountScheduler{service: gateway, stats: newOpenAIAccountRuntimeStats()}
	accounts := []*Account{priorityCandidate(1, .1, 0).account, priorityCandidate(2, .1, 0).account}
	loads := map[int64]*AccountLoadInfo{1: {AccountID: 1}, 2: {AccountID: 2}}
	req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: "scope-model", UseUpstreamTokenCost: true, SessionHash: "scope-order"}
	freeBefore := scheduler.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, loads)
	require.True(t, freeBefore.priorityScheduling)
	require.Len(t, freeBefore.selectionOrder, 2)
	req.StickyWeighted, req.StickyAccountID = true, 1
	boundBefore := scheduler.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, loads)
	require.False(t, boundBefore.priorityScheduling, "preserve the existing weighted soft-binding path")
	require.Equal(t, int64(1), boundBefore.selectionOrder[0].account.ID)

	otherModel, otherGroup, otherEffort := req, req, req
	otherModel.RequestedModel = "other-model"
	group := int64(900)
	otherGroup.GroupID = &group
	otherEffort.ObservationEffort = "high"
	otherEffort.ObservationEffortScoped = true
	slow := 90000
	for i := 0; i < 20; i++ {
		scheduler.stats.report(1, false, &slow)
		for _, other := range []OpenAIAccountScheduleRequest{otherModel, otherGroup, otherEffort} {
			scheduler.stats.attempts.report(priorityRequestAttemptKey(other, 1), false, time.Now())
		}
	}
	boundAfter := scheduler.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, loads)
	require.Equal(t, boundBefore.selectionOrder[0].account.ID, boundAfter.selectionOrder[0].account.ID)
	require.Zero(t, boundAfter.candidates[0].errorRate)
	require.False(t, boundAfter.candidates[0].hasTTFT)
	req.StickyWeighted, req.StickyAccountID = false, 0
	freeAfter := scheduler.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, loads)
	require.True(t, freeAfter.priorityScheduling)
	for i := range freeBefore.selectionOrder {
		require.Equal(t, freeBefore.selectionOrder[i].account.ID, freeAfter.selectionOrder[i].account.ID, "cold scoped selection keeps its own score and tie ordering")
	}

	// Default effort is an exact scope even when no explicit effort was supplied.
	req.StickyWeighted, req.StickyAccountID = true, 1
	for i := 0; i < 20; i++ {
		scheduler.stats.attempts.report(priorityRequestAttemptKey(req, 1), false, time.Now())
	}
	current := scheduler.buildOpenAIAccountLoadPlan(context.Background(), req, accounts, loads)
	require.Equal(t, int64(2), current.selectionOrder[0].account.ID, "current-scope failures can change the actual TopK choice")
}
