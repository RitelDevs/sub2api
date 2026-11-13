//go:build unit

package service

import (
	"math"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/upstreamstats"
	"github.com/stretchr/testify/require"
)

func performanceFixture(tps, ttft, degradation float64) PrioritySchedulingSignal {
	now := time.Now()
	metrics := &AccountPerformance{
		AvgTPS: &tps, AvgTTFTMs: &ttft, DegradationPercent: &degradation,
		TPSSamples: 20, TTFTSamples: 20, QualitySamples: 10,
		WindowStart: now.Add(-time.Hour), WindowEnd: now,
	}
	return PrioritySchedulingSignal{Performance: metrics, ScopedPerformance: cloneAccountPerformance(metrics), ProfitSamples: 20, Revenue: 10, BaseCost: 2}
}

func TestPriorityUnifiedPerformanceScoreAndLiveSelection(t *testing.T) {
	cfg := DefaultPrioritySchedulingConfig()
	cfg.Enabled, cfg.Mode = true, "custom"
	cfg.QualityWeight, cfg.LatencyWeight, cfg.LoadWeight, cfg.CostWeight = 0, 0, 100, 0
	slow, fast := performanceFixture(10, 4000, 0), performanceFixture(60, 4000, 0)
	account := priorityCandidate(1, .1, 0)
	low := scorePriorityCandidate(cfg, account, slow, time.Now())
	high := scorePriorityCandidate(cfg, account, fast, time.Now())
	require.InDelta(t, 25, low.Score, 1e-8)
	require.InDelta(t, 100.0*60/90, high.Score, 1e-8)
	require.Greater(t, high.Score, low.Score)
	reader := &priorityReaderStub{signal: map[int64]PrioritySchedulingSignal{1: slow, 2: fast}}
	gateway := priorityGateway(cfg, reader)
	request := OpenAIAccountScheduleRequest{RequestedModel: "fixture-model", Platform: PlatformOpenAI, UseUpstreamTokenCost: true}
	pool := []openAIAccountCandidateScore{priorityCandidate(1, .1, 0), priorityCandidate(2, .1, 0)}
	gateway.prioritySignals(request, cfg, pool)
	require.Eventually(t, func() bool { _, ready := gateway.prioritySignals(request, cfg, pool); return ready }, time.Second, time.Millisecond)
	scheduler := &defaultOpenAIAccountScheduler{service: gateway}
	plan := openAIAccountLoadPlan{candidates: pool, topK: 1}
	require.True(t, scheduler.applyPriorityScheduling(request, &plan))
	require.True(t, plan.priorityScheduling)
	require.Greater(t, plan.candidates[1].score, plan.candidates[0].score)
	snapshot := gateway.PrioritySchedulingSnapshot()
	require.Equal(t, "account_performance", snapshot.PerformanceSource)
	require.Equal(t, fast.Performance.AvgTPS, snapshot.Candidates[0].Performance.AvgTPS)
}

func TestPriorityUnifiedPerformanceIgnoresLegacyMeasurements(t *testing.T) {
	cfg := DefaultPrioritySchedulingConfig()
	signal := performanceFixture(30, 4000, 0)
	account := priorityCandidate(1, .1, 0)
	before := scorePriorityCandidate(cfg, account, signal, time.Now())
	signal.P90TTFTMs, signal.QualityPassed, signal.QualitySamples = 90000, 0, 500
	cfg.LatencyPercentile, cfg.LatencyMeasurement = 70, "attributed_v1"
	after := scorePriorityCandidate(cfg, account, signal, time.Now())
	require.Equal(t, before.Score, after.Score)
	require.Equal(t, 4000.0, *after.SelectedTTFTMs)
	require.Equal(t, "scoped_account_performance", after.LatencyMeasurement)
	require.NotContains(t, after.Reasons, "quality_below_target")
}

func TestPriorityUnifiedPerformanceHTTPAndUnknownEvidence(t *testing.T) {
	cfg := DefaultPrioritySchedulingConfig()
	cfg.ErrorRateWeight = 50
	signal := performanceFixture(30, 4000, 0)
	percent := 25.0
	signal.Performance.UpstreamError = upstreamstats.Evidence{Source: "process_local_http_responses", Failures: 1, Total: 4, Percent: &percent}
	account := priorityCandidate(1, .1, 0)
	account.priorityErrors = PriorityErrorEvidence{ErrorRate: float64Ptr(.25), SmoothedRate: .25, Failures: 1, Successes: 3}
	score := scorePriorityCandidate(cfg, account, signal, time.Now())
	require.InDelta(t, .875, score.ErrorPenalty, 1e-8)
	require.InDelta(t, score.BaseScore*.875, score.Score, 1e-8)
	require.Equal(t, int64(1), score.Failures)
	cfg.ErrorRateWeight = 0
	require.Equal(t, 1.0, scorePriorityCandidate(cfg, priorityCandidate(1, .9, 0), signal, time.Now()).ErrorPenalty)
	signal.ScopedPerformance.AvgTPS = nil
	require.Contains(t, scorePriorityCandidate(cfg, priorityCandidate(1, .1, 0), signal, time.Now()).Reasons, "tps_insufficient")
	signal.Performance.UpstreamError.Total = 0
	require.Nil(t, scorePriorityCandidate(cfg, priorityCandidate(1, .1, 0), signal, time.Now()).ErrorRate)
	missing := scorePriorityCandidate(cfg, priorityCandidate(1, .1, 0), PrioritySchedulingSignal{}, time.Now())
	require.Contains(t, missing.Reasons, "quality_unknown")
	require.Contains(t, missing.Reasons, "latency_insufficient")
	require.Contains(t, missing.Reasons, "tps_insufficient")
}

func TestPriorityUnifiedPerformanceAverageControlsAdmission(t *testing.T) {
	cfg := DefaultPrioritySchedulingConfig()
	cfg.LatencyPenaltyMode = PriorityLatencyStop
	slow := performanceFixture(30, 20000, 0)
	item := priorityCandidate(1, .1, 0)
	require.True(t, priorityLatencyStopped(cfg, item, slow, time.Now()))
	require.True(t, priorityLatencyStopped(cfg, item, slow, time.Now().Add(24*time.Hour)), "wall time does not manufacture recovery")
	fast := performanceFixture(30, 0, 0)
	score := scorePriorityCandidate(cfg, item, fast, time.Now())
	require.True(t, priorityLatencyMeasured(cfg, score), "measured zero TTFT is valid")
	require.False(t, score.LatencyStopped)
	invalid := math.NaN()
	fast.ScopedPerformance.AvgTTFTMs = &invalid
	require.Contains(t, scorePriorityCandidate(cfg, item, fast, time.Now()).Reasons, "latency_insufficient")
}

// Keep the interface contract explicit: callers share the repository signal path.
var _ PrioritySchedulingSignalReader = (*priorityReaderStub)(nil)

func TestPriorityUnifiedPerformanceSnapshotOwnsMetricPointers(t *testing.T) {
	cfg := DefaultPrioritySchedulingConfig()
	signal := performanceFixture(30, 4000, 0)
	cachePercent := 70.0
	signal.Performance.AvgCachePercent = &cachePercent
	signal.ScopedPerformance.AvgCachePercent = float64Ptr(cachePercent)
	score := scorePriorityCandidate(cfg, priorityCandidate(1, .1, 0), signal, time.Now())
	*score.Performance.AvgCachePercent = 99
	require.Equal(t, 70.0, *signal.Performance.AvgCachePercent)
	*score.Performance.AvgTPS = 999
	*score.Performance.AvgTTFTMs = 999
	require.Equal(t, 30.0, *signal.Performance.AvgTPS)
	require.Equal(t, 4000.0, *signal.Performance.AvgTTFTMs)
	*score.ScopedPerformance.AvgTPS = 1
	*score.ScopedPerformance.AvgTTFTMs = 2
	require.Equal(t, 30.0, *signal.ScopedPerformance.AvgTPS)
	require.Equal(t, 4000.0, *signal.ScopedPerformance.AvgTTFTMs)
}

func TestPriorityStickyUsesCurrentRequestErrorEvidence(t *testing.T) {
	cfg := DefaultPrioritySchedulingConfig()
	cfg.Enabled = true
	signal := performanceFixture(30, 4000, 0)
	percent := 50.0
	signal.Performance.UpstreamError = upstreamstats.Evidence{Source: "process_local_http_responses", Total: 4, Failures: 2, Percent: &percent}
	reader := &priorityReaderStub{signal: map[int64]PrioritySchedulingSignal{1: signal}}
	gateway := priorityGateway(cfg, reader)
	scheduler := &defaultOpenAIAccountScheduler{service: gateway, stats: newOpenAIAccountRuntimeStats()}
	req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, RequestedModel: "fixture", UseUpstreamTokenCost: true}
	pool := []openAIAccountCandidateScore{priorityCandidate(1, .1, 0)}
	require.Eventually(t, func() bool { return gateway.priorityHistory(req, cfg, pool).ready() }, time.Second, time.Millisecond)
	_, _, _, escape := scheduler.shouldEscapeStickyForRequest(req, 1, openAIStickyEscapeConfig{enabled: true, errorRate: .2})
	require.False(t, escape, "account-wide HTTP failures leave the current binding intact")
	other := req
	other.RequestedModel = "other-model"
	for i := 0; i < 20; i++ {
		scheduler.stats.attempts.report(priorityRequestAttemptKey(other, 1), false, time.Now())
	}
	_, _, _, escape = scheduler.shouldEscapeStickyForRequest(req, 1, openAIStickyEscapeConfig{enabled: true, errorRate: .2})
	require.False(t, escape, "another model keeps its own error evidence")
	for i := 0; i < 20; i++ {
		scheduler.stats.attempts.report(priorityRequestAttemptKey(req, 1), i < 10, time.Now())
	}
	reason, rate, _, escape := scheduler.shouldEscapeStickyForRequest(req, 1, openAIStickyEscapeConfig{enabled: true, errorRate: .2})
	require.True(t, escape)
	require.Equal(t, "error_rate", reason)
	require.InDelta(t, .4, rate, 1e-9)
	req.PreserveStickyBinding = true
	_, _, _, escape = scheduler.shouldEscapeStickyForRequest(req, 1, openAIStickyEscapeConfig{enabled: true, errorRate: .2})
	require.False(t, escape)
}

func TestPriorityGlobalMetricsCannotChangeScopedScoreOrLatencyStop(t *testing.T) {
	cfg := DefaultPrioritySchedulingConfig()
	cfg.LatencyPenaltyMode = PriorityLatencyStop
	cfg.CacheWeight = 20
	signal := performanceFixture(30, 4000, 0)
	signal.ScopedPerformance.AvgCachePercent = float64Ptr(80)
	signal.ScopedPerformance.CacheSamples = 20
	item := priorityCandidate(1, .1, 0)
	now := time.Now()
	before := scorePriorityCandidate(cfg, item, signal, now)

	mixed := signal
	mixed.Performance = performanceFixture(1, 90000, 100).Performance
	mixed.Performance.AvgCachePercent = float64Ptr(0)
	mixed.Performance.CacheSamples = 1000
	mixed.Performance.UpstreamError = upstreamstats.Evidence{
		Source: "process_local_http_responses", Total: 100, Failures: 90, Percent: float64Ptr(90),
	}
	item.errorRate = .9
	after := scorePriorityCandidate(cfg, item, mixed, now)
	require.Equal(t, before.Score, after.Score)
	require.Equal(t, before.Tier, after.Tier)
	require.Equal(t, before.Reasons, after.Reasons)
	require.Equal(t, before.SelectedTTFTMs, after.SelectedTTFTMs)
	require.False(t, after.LatencyStopped)
	require.Equal(t, 90000.0, *after.Performance.AvgTTFTMs, "the dashboard retains the global display value")

	slow := mixed
	slow.ScopedPerformance = cloneAccountPerformance(signal.ScopedPerformance)
	slow.ScopedPerformance.AvgTTFTMs = float64Ptr(20000)
	require.True(t, priorityLatencyStopped(cfg, item, slow, now), "current request evidence controls the latency stop")

	missing := mixed
	missing.ScopedPerformance = nil
	unknown := scorePriorityCandidate(cfg, item, missing, now)
	require.Contains(t, unknown.Reasons, "latency_insufficient")
	require.Contains(t, unknown.Reasons, "tps_insufficient")
	require.False(t, unknown.LatencyStopped)
}
