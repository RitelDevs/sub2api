//go:build unit

package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDeepSeekRecoveryHTTPBalanceManualPause(t *testing.T) {
	repo := &scheduling429Repo{}
	limits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	counter := &openAI403CounterCacheStub{counts: []int64{1, 2, 3}}
	limits.SetOpenAI403CounterCache(counter)
	blocker := &runtimeBlockRecorder{}
	limits.SetAccountRuntimeBlocker(blocker)
	gateway := &OpenAIGatewayService{rateLimitService: limits}
	account := &Account{ID: 122, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	body := `{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}`
	upstream := &deepSeekRecoveryUpstreamStub{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{"X-Request-Id": {"fixture-request-id"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
	}}
	gateway.httpUpstream = upstream
	ctx, a := NewDeepSeekRecoveryAttempt(t.Context())
	defer a.Close()
	_, err := gateway.doOpenAIUpstream(deepSeekRecoveryRequest(t, ctx, "/v1/chat/completions"), "", account)
	require.Error(t, err)
	require.Equal(t, []bool{false}, repo.values)
	require.False(t, account.Schedulable)
	require.Equal(t, StatusActive, account.Status)
	require.Zero(t, repo.setErrorCalls)
	require.Len(t, counter.counts, 3)
	require.Zero(t, repo.tempCalls)
	require.Contains(t, repo.lastExtraUpdates["deepseek_balance_pause_reason"], "administrator")
	require.Len(t, blocker.until, 1)
	require.True(t, blocker.until[0].IsZero())
	require.Equal(t, "balance_low", a.Outcome().HTTPErrorClass)
	require.Equal(t, "fixture-request-id", a.Outcome().UpstreamRequestID)
	// A healthy later balance observation must not re-enable the persistent switch.
	checker := &CNProviderBalanceCheckService{accountRepo: repo}
	checker.applyBalanceResult(context.Background(), account, &CNProviderBalanceResult{Available: true, Balance: 100, Currency: "USD"}, 1)
	require.Equal(t, []bool{false}, repo.values)
	require.False(t, account.Schedulable)
}
func TestDeepSeekBalanceClassificationDoesNotScanClientEcho(t *testing.T) {
	repo := &scheduling429Repo{}
	limits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	limits.SetOpenAI403CounterCache(&openAI403CounterCacheStub{counts: []int64{1}})
	account := &Account{ID: 122, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	limits.HandleUpstreamError(t.Context(), account, 403, http.Header{}, []byte(`{"error":{"message":"forbidden"},"request":{"code":"INSUFFICIENT_BALANCE"}}`))
	require.Empty(t, repo.values)
	require.Equal(t, 1, repo.tempCalls)
}

type deepSeekCooldownRepo struct {
	tempUnschedPendingRepo
	resetAt time.Time
	model   string
}

func (r *deepSeekCooldownRepo) SetModelRateLimit(ctx context.Context, id int64, model string, until time.Time, reason ...string) error {
	r.resetAt = until
	r.model = model
	return r.recordingTempUnschedRepo.SetModelRateLimit(ctx, id, model, until, reason...)
}
func TestDeepSeekRecovery503CountsPhysicalResponsesOnce(t *testing.T) {
	repo := &deepSeekCooldownRepo{}
	limits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	counter := &countingTempUnschedTrigger{inner: newMemoryTempUnschedTriggerCounter(time.Now)}
	limits.SetTempUnschedTriggerCounter(counter)
	account := tempUnschedTimingAccount(map[string]any{"error_code": float64(503), "keywords": []any{"Service temporarily unavailable"}, "scope": "model", "duration_minutes": float64(10), "trigger_count": float64(3), "trigger_window_minutes": float64(1), "stream_timing": "before_stream"})
	account.Platform = PlatformDeepseek
	gateway := &OpenAIGatewayService{rateLimitService: limits, httpUpstream: &deepSeekRecoveryUpstreamStub{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"Service temporarily unavailable"}}`))}, nil
	}}}
	for i := 1; i <= 3; i++ {
		ctx, a := NewDeepSeekRecoveryAttempt(t.Context())
		req := deepSeekRecoveryRequest(t, ctx, "/v1/chat/completions")
		_, err := gateway.doOpenAIUpstream(req, "", account)
		a.Close()
		require.Error(t, err)
		require.Equal(t, i, counter.hits)
		if i < 3 {
			require.Empty(t, repo.modelReasons)
		}
	}
	require.Len(t, repo.modelReasons, 1)
	require.Empty(t, repo.reasons)
	require.Zero(t, repo.setErrorCalls)
	require.WithinDuration(t, time.Now().Add(10*time.Minute), repo.resetAt, time.Second)
}
func TestDeepSeekObservedFinishSurvivesToolValidationFailure(t *testing.T) {
	resp, a := deepSeekRecoveryFixture(t, "/v1/chat/completions", "text/event-stream", deepSeekRecoverySSE(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"name":"lookup","arguments":"{"}}]},"finish_reason":null}]}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`))
	_, err := io.ReadAll(resp.Body)
	require.Error(t, err)
	o := a.Outcome()
	require.Equal(t, "length", o.ObservedFinishReason)
	require.Empty(t, o.FinishReason)
	require.False(t, o.Terminal)
}

func TestDeepSeekRecoveryHTTPHTML403DoesNotDisableAccount(t *testing.T) {
	repo := &scheduling429Repo{}
	limits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	limits.SetOpenAI403CounterCache(&openAI403CounterCacheStub{counts: []int64{3}})
	a := &Account{ID: 122, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	gateway := &OpenAIGatewayService{rateLimitService: limits, httpUpstream: &deepSeekRecoveryUpstreamStub{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 403, Header: http.Header{"Content-Type": {"text/html"}}, Body: io.NopCloser(strings.NewReader("<!DOCTYPE html><html><title>Access denied</title>error 1010 Cloudflare</html>"))}, nil
	}}}
	ctx, attempt := NewDeepSeekRecoveryAttempt(t.Context())
	defer attempt.Close()
	_, err := gateway.doOpenAIUpstream(deepSeekRecoveryRequest(t, ctx, "/v1/chat/completions"), "", a)
	require.Error(t, err)
	require.Zero(t, repo.setErrorCalls)
	require.Empty(t, repo.values)
	require.Zero(t, repo.tempCalls)
}
