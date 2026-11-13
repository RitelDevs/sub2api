//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPriorityAccountCostMultiplierRoundTrip(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	billing, group := 1.0, 5.0
	account := mustCreateAccount(t, tx.Client(), &service.Account{Name: "cost-roundtrip", Platform: service.PlatformOpenAI, RateMultiplier: &billing, GroupRateMultiplier: &group, Extra: map[string]any{"untouched": true}})
	require.NoError(t, repo.Update(ctx, account))
	require.Equal(t, 0.1, account.CostMultiplier())
	for _, cost := range []float64{0.25, 0} {
		require.NoError(t, repo.UpdateExtra(ctx, account.ID, map[string]any{service.AccountCostMultiplierExtraKey: cost}))
		loaded, err := repo.GetByID(ctx, account.ID)
		require.NoError(t, err)
		require.Equal(t, cost, loaded.CostMultiplier())
		require.Equal(t, billing, loaded.BillingRateMultiplier())
		require.Equal(t, group, loaded.UserGroupRateMultiplier())
		require.Equal(t, true, loaded.Extra["untouched"])
	}
	require.True(t, shouldEnqueueSchedulerOutboxForExtraUpdates(map[string]any{service.AccountCostMultiplierExtraKey: 0.1}), "cost edits invalidate cached scheduler accounts")
}

// Multi-model plans save the executed model on each result. Plan.ModelID is
// only the legacy fallback, and a malformed legacy round cannot poison usage.

func TestPrioritySchedulingFinanceScopeAndMissingPerformance(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "priority-finance@test.invalid"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "synthetic-priority-finance", Name: "test"})
	account := mustCreateAccount(t, client, &service.Account{Name: "finance", Platform: service.PlatformOpenAI})
	now := time.Now()
	for _, model := range []string{"model-a", "model-b"} {
		_, err := repo.Create(ctx, &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: model, Model: model, InputTokens: 10, ActualCost: 2, TotalCost: 4, CreatedAt: now})
		require.NoError(t, err)
	}
	query := service.PrioritySchedulingQuery{AccountIDs: []int64{account.ID}, Model: "model-a", UsageSince: now.Add(-time.Hour), UsageUntil: now.Add(time.Second)}
	got, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.Equal(t, 1, got[account.ID].ProfitSamples)
	require.Equal(t, 2.0, got[account.ID].Revenue)
	require.NotNil(t, got[account.ID].Performance)
	require.Nil(t, got[account.ID].Performance.AvgTPS)
	require.Nil(t, got[account.ID].Performance.AvgTTFTMs)
	require.Nil(t, got[account.ID].Performance.DegradationPercent)
	query.Model = "missing-model"
	missing, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.Zero(t, missing[account.ID].ProfitSamples)
	require.Equal(t, got[account.ID].Performance, missing[account.ID].Performance)
}

func TestPrioritySchedulingPerformanceScope(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	suffix := uuid.NewString()
	user := mustCreateUser(t, client, &service.User{Email: "priority-scope-" + suffix + "@test.invalid"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "priority-scope-" + suffix, Name: "synthetic"})
	account := mustCreateAccount(t, client, &service.Account{Name: "priority-scope-" + suffix, Platform: service.PlatformOpenAI})
	group := mustCreateGroup(t, client, &service.Group{Name: "priority-group-" + suffix, Platform: service.PlatformOpenAI})
	otherGroup := mustCreateGroup(t, client, &service.Group{Name: "priority-other-" + suffix, Platform: service.PlatformOpenAI})
	now := time.Now().UTC().Truncate(time.Second)
	high, low := "high", "low"
	query := service.PrioritySchedulingQuery{
		AccountIDs: []int64{account.ID}, Model: "public-model", QualityModel: "quality-a",
		GroupID: &group.ID, ObservationEffort: &high,
		UsageSince: now.Add(-15 * time.Minute), UsageUntil: now,
		QualitySince: now.Add(-time.Hour),
	}
	add := func(model string, groupID *int64, effort *string, event, recorded time.Time, ttft, output, cache int) {
		t.Helper()
		duration := 4000
		row := &service.UsageLog{
			UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: uuid.NewString(),
			Model: "account-wire-model", RequestedModel: model, GroupID: groupID, ReasoningEffort: effort,
			InputTokens: 100 - cache, CacheReadTokens: cache, OutputTokens: output,
			DurationMs: &duration, FirstTokenMs: &ttft, Stream: true,
			ActualCost: 1, TotalCost: 2, CreatedAt: recorded,
		}
		inserted, err := repo.Create(ctx, row)
		require.NoError(t, err)
		require.True(t, inserted)
		trace, err := json.Marshal(map[string]any{
			"version": 1, "started_at": event.Format(time.RFC3339Nano),
			"outcome": "success", "status": 200, "body_complete": true,
			"canceled": false, "truncated": false, "client_disconnect": false,
			"downstream_error": false, "terminal": "completed",
		})
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO request_timing_details(usage_log_id,trace_id,created_at,detail) VALUES($1,$2::uuid,$3,$4::jsonb)`, row.ID, uuid.NewString(), recorded, string(trace))
		require.NoError(t, err)
	}
	// Both request-window boundaries participate in the exact public-model scope.
	add(query.Model, query.GroupID, &high, query.UsageSince, query.UsageSince, 100, 40, 0)
	add(query.Model, query.GroupID, &high, now, now, 300, 120, 50)

	// Validate the complete two-model round before selecting one model's verdict.
	config := `{"quality":{},"parallel_count":1,"model_ids":["quality-a","quality-b"]}`
	rows, err := tx.QueryContext(ctx, `INSERT INTO scheduled_test_plans(account_id,model_id,cron_expression,enabled,pelican_config,updated_at) VALUES($1,'quality-a','*/30 * * * *',true,$2::jsonb,$3) RETURNING id`, account.ID, config, now.Add(-2*time.Hour))
	require.NoError(t, err)
	require.True(t, rows.Next())
	var plan int64
	require.NoError(t, rows.Scan(&plan))
	require.NoError(t, rows.Close())
	for _, result := range []struct{ model, status, verdict, message string }{
		{"quality-a", "success", "correct", ""},
		{"quality-b", "failed", "incorrect", "answer_mismatch"},
	} {
		probe, err := json.Marshal(map[string]any{"model_id": result.model, "parallel_count": 1, "model_ids": []string{"quality-a", "quality-b"}})
		require.NoError(t, err)
		judgment, err := json.Marshal(map[string]string{"verdict": result.verdict})
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_results(plan_id,status,started_at,finished_at,created_at,pelican_config,quality_round_id,quality_action,quality_judgment,error_message) VALUES($1,$2,$3,$4,$4,$5::jsonb,'scope-round','passed',$6::jsonb,$7)`, plan, result.status, now.Add(-5*time.Minute), now.Add(-4*time.Minute), string(probe), string(judgment), result.message)
		require.NoError(t, err)
	}
	baseline, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	scoped := baseline[account.ID].ScopedPerformance
	require.NotNil(t, scoped)
	require.Equal(t, 2, scoped.TTFTSamples)
	require.Equal(t, 200.0, *scoped.AvgTTFTMs)
	require.Equal(t, 20.0, *scoped.AvgTPS)
	require.Equal(t, 25.0, *scoped.AvgCachePercent)
	require.Equal(t, 1, scoped.QualitySamples)
	require.Equal(t, 0.0, *scoped.DegradationPercent)
	require.Equal(t, 100.0, *baseline[account.ID].Performance.DegradationPercent)
	require.Equal(t, query.UsageSince, scoped.WindowStart)
	require.Equal(t, query.UsageUntil, scoped.WindowEnd)

	recent := now.Add(-time.Minute)
	add("other-model", query.GroupID, &high, recent, recent, 1000, 1000, 90)
	add(query.Model, &otherGroup.ID, &high, recent, recent, 1500, 1000, 90)
	add(query.Model, nil, &high, recent, recent, 1200, 1000, 90)
	add(query.Model, query.GroupID, &low, recent, recent, 2000, 1000, 90)
	add(query.Model, query.GroupID, nil, recent, recent, 2500, 1000, 90)
	add(query.Model, query.GroupID, &high, query.UsageSince.Add(-time.Second), query.UsageSince.Add(-time.Second), 3000, 1000, 90)
	add(query.Model, query.GroupID, &high, query.UsageSince.Add(-time.Second), recent, 3500, 1000, 90)
	add(query.Model, query.GroupID, &high, now.Add(time.Second), now.Add(time.Second), 3900, 1000, 90)
	got, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.Equal(t, *scoped, *got[account.ID].ScopedPerformance)
	require.Greater(t, got[account.ID].Performance.TTFTSamples, scoped.TTFTSamples, "the account display still includes other request populations")
	require.Zero(t, got[account.ID].ScopedPerformance.UpstreamError.Total)
	require.Empty(t, got[account.ID].ScopedPerformance.UpstreamError.Source)

	query.ObservationEffort = nil
	defaultEffort, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.Equal(t, 1, defaultEffort[account.ID].ScopedPerformance.TTFTSamples)
	require.Equal(t, 2500.0, *defaultEffort[account.ID].ScopedPerformance.AvgTTFTMs)
	query.ObservationEffort, query.GroupID = &high, nil
	nilGroup, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.Equal(t, 1, nilGroup[account.ID].ScopedPerformance.TTFTSamples)
	require.Equal(t, 1200.0, *nilGroup[account.ID].ScopedPerformance.AvgTTFTMs)
	query.GroupID, query.QualityModel = &group.ID, "quality-b"
	otherQuality, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.Equal(t, 100.0, *otherQuality[account.ID].ScopedPerformance.DegradationPercent)
	query.Model = ""
	unknown, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.NotNil(t, unknown[account.ID].Performance)
	require.Nil(t, unknown[account.ID].ScopedPerformance)
	require.Zero(t, unknown[account.ID].ProfitSamples)
	query.Model, query.UsageSince = "public-model", time.Time{}
	unknown, err = repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.Nil(t, unknown[account.ID].ScopedPerformance)
}
