//go:build unit

package service

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekBalance403UsesNativePolicy(t *testing.T) {
	repo := &scheduling429Repo{}
	limits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
	limits.SetOpenAI403CounterCache(&openAI403CounterCacheStub{counts: []int64{1}})
	account := &Account{ID: 1, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
	limits.HandleUpstreamError(t.Context(), account, http.StatusForbidden, nil, []byte(`{"error":{"code":"INSUFFICIENT_BALANCE","message":"Insufficient account balance"}}`))
	require.Empty(t, repo.values, "native 403 handling must not permanently toggle scheduling")
	require.NotContains(t, repo.lastExtraUpdates, "deepseek_balance_pause_reason")
	require.Equal(t, 1, repo.tempCalls)
}
