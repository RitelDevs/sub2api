package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func targetSourceOrderFixture(t *testing.T, sources []int64) (*astraRoutingUpstream, *codexGatewayPinUpstream) {
	t.Helper()
	wrapper, _ := accountBorrowFixture(t)
	settings := wrapper.cfg.AstraRouting(t.Context())
	settings.CookiePool.SourceAccountIDs = sources
	wrapper.cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
	settings.CookiePool.QualityMode = settings.QualityMode
	pool := &codexGatewayPinUpstream{config: settings.CookiePool, routingSettings: settings, rotationCache: &wrapper.rotationCache}
	for _, source := range sources {
		accountBorrowAdd(t, pool, source, fmt.Sprintf("cookie-%d", source), 220*time.Second)
	}
	return wrapper, pool
}

func TestAutomaticBorrowSourceOrderScopeAndBoundedMemory(t *testing.T) {
	wrapper, pool := targetSourceOrderFixture(t, []int64{299, 298})
	now := time.Now()
	request := accountBorrowRequest(t, "gpt-6-astra", "")
	_, key := targetBorrowQuietKeys(request, 300, "gpt-6-astra", "exit", 299, nil)
	pool.rememberTargetSourceAttempt(key, now)
	require.Equal(t, []int64{298, 299}, wrapper.automaticTargetSourceOrder(request, "exit", 300, "gpt-6.1-sol", nil, pool, astraTargetValidation{}, false), "linked Sol exploration reuses only this account's Astra attempt scope")
	require.Equal(t, []int64{299, 298}, wrapper.automaticTargetSourceOrder(request, "new-exit", 300, "gpt-6-astra", nil, pool, astraTargetValidation{}, false))
	require.Equal(t, []int64{299, 298}, wrapper.automaticTargetSourceOrder(request, "exit", 301, "gpt-6-astra", nil, pool, astraTargetValidation{}, false))
	legacy := &codexGatewayPinUpstream{config: pool.config, routes: pool.routes, targetSourceAttempts: pool.targetSourceAttempts}
	legacy.config.QualityMode = ""
	require.Equal(t, []int64{299, 298}, wrapper.automaticTargetSourceOrder(request, "exit", 300, "gpt-6.1-sol", nil, legacy, astraTargetValidation{}, false), "legacy models retain independent exploration")
	for i := range 4097 {
		next := key
		next.accountID = int64(1000 + i)
		pool.rememberTargetSourceAttempt(next, now.Add(time.Duration(i)*time.Nanosecond))
	}
	require.LessOrEqual(t, len(pool.targetSourceAttempts), 4096)
	pool.rememberTargetSourceAttempt(key, now.Add(2*time.Hour))
	require.Len(t, pool.targetSourceAttempts, 1, "old exploration timestamps are pruned without keeping credentials")
}

func TestAutomaticBorrowSourceOrderRejectsStaleProvenIdentity(t *testing.T) {
	wrapper, pool := targetSourceOrderFixture(t, []int64{299, 298})
	request := accountBorrowRequest(t, "gpt-6-astra", "")
	_, oldKey := targetBorrowQuietKeys(request, 300, "gpt-6-astra", "exit", 298, nil)
	old := astraTargetValidation{passed: true, sourceID: 298, negativeKey: oldKey}
	pool.targetMu.Lock()
	pool.rememberTargetSourcePreferenceLocked(oldKey, time.Now())
	pool.targetMu.Unlock()
	require.Equal(t, []int64{298, 299}, wrapper.automaticTargetSourceOrder(request, "exit", 300, "gpt-6-astra", nil, pool, old, true))
	pool.config.IPAffinity = true
	pool.routes[299] = codexGatewayRoute{cookie: pool.routes[299].cookie, proxy: "native-source-exit", expires: pool.routes[299].expires}
	pool.routes[298] = codexGatewayRoute{cookie: pool.routes[298].cookie, proxy: "changed-proven-source-exit", expires: pool.routes[298].expires}
	require.Equal(t, []int64{299, 298}, wrapper.automaticTargetSourceOrder(request, "exit", 300, "gpt-6-astra", nil, pool, old, true), "changed donor exit cannot inherit proven preference")
	pool.config.IPAffinity = false
	request.Header.Set("Authorization", "Bearer replacement-target")
	require.Equal(t, []int64{299, 298}, wrapper.automaticTargetSourceOrder(request, "exit", 300, "gpt-6-astra", nil, pool, old, true))
	request.Header.Set("Authorization", "Bearer fixture-target")
	require.Equal(t, []int64{299, 298}, wrapper.automaticTargetSourceOrder(request, "another-exit", 300, "gpt-6-astra", nil, pool, old, true))
}
