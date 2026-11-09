package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestInactiveDegradedUsageNeverProbes(t *testing.T) {
	var calls atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer proxy.Close()
	proxyURL, err := url.Parse(proxy.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(proxyURL.Port())
	require.NoError(t, err)
	proxyID := int64(1)
	observed := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	account := Account{ID: 212, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: "inactive",
		Credentials: map[string]any{"access_token": "synthetic"}, ProxyID: &proxyID,
		Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: proxyURL.Hostname(), Port: port},
		Extra: map[string]any{"quality_candy": map[string]any{"state": "degraded"},
			"codex_usage_updated_at": observed.Format(time.RFC3339),
			"codex_5h_used_percent":  42.0, "codex_5h_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339)}}
	repo := &stubOpenAIAccountRepo{accounts: []Account{account}}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache()}
	for _, force := range []bool{false, true} {
		usage, err := svc.GetUsage(context.Background(), account.ID, force)
		require.NoError(t, err)
		require.Equal(t, "account_resting", usage.ErrorCode)
		require.Equal(t, "passive", usage.Source)
		require.Equal(t, observed, *usage.UpdatedAt)
		require.NotNil(t, usage.FiveHour)
		require.Equal(t, 42.0, usage.FiveHour.Utilization)
		require.Nil(t, usage.SevenDay)
		usage, err = svc.GetUsageForAccount(context.Background(), &account, force)
		require.NoError(t, err)
		require.Equal(t, "account_resting", usage.ErrorCode)
		usages, failures, err := svc.GetUsageBatch(context.Background(), []int64{account.ID}, force)
		require.NoError(t, err)
		require.Empty(t, failures)
		require.Equal(t, "account_resting", usages[account.ID].ErrorCode)
	}
	require.Zero(t, calls.Load(), "resting reads must not reach even the proxy")
	// Unrelated disabled accounts and enabled degraded accounts retain active reads.
	for _, control := range []struct{ status, state string }{{"active", "degraded"}, {"inactive", "healthy"}} {
		account.Status = control.status
		account.Extra["quality_candy"] = map[string]any{"state": control.state}
		usage, err := svc.GetUsageForAccount(context.Background(), &account, true)
		require.NoError(t, err)
		require.Empty(t, usage.ErrorCode)
	}
	require.EqualValues(t, 2, calls.Load())
}

func TestInactiveDegradedUsageWithoutSnapshotDoesNotInventZero(t *testing.T) {
	svc := &AccountUsageService{}
	for _, expired := range []bool{false, true} {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: "inactive", Extra: map[string]any{"quality_candy": map[string]any{"state": "degraded"}}}
		if expired {
			account.Extra["codex_5h_used_percent"] = 42.0
			account.Extra["codex_5h_reset_at"] = time.Now().Add(-time.Hour).Format(time.RFC3339)
		}
		usage, err := svc.GetUsageForAccount(context.Background(), account, true)
		require.NoError(t, err)
		require.Equal(t, "account_resting", usage.ErrorCode)
		require.Nil(t, usage.FiveHour)
		require.Nil(t, usage.SevenDay)
		require.Nil(t, usage.UpdatedAt)
	}
}

func TestInactiveDegradedModelsDiscoveryBlocked(t *testing.T) {
	// A nil gateway guarantees neither entry can reach dependencies or upstream.
	var gateway *OpenAIGatewayService
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: "inactive", Extra: map[string]any{"quality_candy": map[string]any{"state": "degraded"}}}
	_, err := gateway.FetchOpenAIModelsList(context.Background(), account)
	require.Equal(t, "OPENAI_ACCOUNT_RESTING", infraerrors.Reason(err))
	_, err = gateway.FetchCodexModelsManifest(context.Background(), account, "", "")
	require.Equal(t, "OPENAI_ACCOUNT_RESTING", infraerrors.Reason(err))
}
