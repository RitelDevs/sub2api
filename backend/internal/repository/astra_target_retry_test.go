package repository

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAccountBorrowEffectiveRetryUsesLongestConstraintAndEarliestDonor(t *testing.T) {
	wrapper, pool := accountBorrowFixture(t)
	now := time.Now()
	req := accountBorrowRequest(t, "gpt-6-astra", "")
	quota, negative := targetBorrowQuietKeys(req, 300, "gpt-6-astra", "exit", 299, nil)
	check := astraTargetValidation{sourceID: 299, quotaKey: quota, negativeKey: negative}
	wrapper.rememberTargetQualityFailure(negative, "target_quality_failed", now)
	wrapper.rememberTargetQuota(quota, "target_probe_rate_limited", now.Add(5*time.Minute))
	pool.targetMu.Lock()
	pool.mu.Lock()
	retry := wrapper.targetRetryDeadline(pool, check, 300, "gpt-6-astra", now)
	pool.mu.Unlock()
	pool.targetMu.Unlock()
	require.Equal(t, now.Add(5*time.Minute), retry.until, "quota outlasts source-specific quality wait")
	require.Equal(t, "target_probe_rate_limited", retry.reason)
	wrapper.resetTargetQualityFailure(quota)
	_, other := targetBorrowQuietKeys(req, 300, "gpt-6-astra", "exit", 298, nil)
	wrapper.rememberTargetQualityFailure(other, "target_quality_failed", now.Add(time.Minute))
	pool.targetMu.Lock()
	pool.mu.Lock()
	retry = wrapper.targetRetryDeadline(pool, check, 300, "gpt-6-astra", now)
	pool.mu.Unlock()
	pool.targetMu.Unlock()
	require.Equal(t, now.Add(3*time.Minute), retry.until, "the first donor to leave its complete quiet period can resume")
	require.Equal(t, "target_quality_failed", retry.reason)
}

func TestAccountBorrowIPAffinityUnknownExitRemainsExplorable(t *testing.T) {
	wrapper, pool := accountBorrowFixture(t)
	pool.config.IPAffinity = true
	req := accountBorrowRequest(t, "gpt-6-astra", "")
	quota, negative := targetBorrowQuietKeys(req, 300, "gpt-6-astra", "donor-exit", 299, nil)
	wrapper.rememberTargetQuota(quota, "target_probe_rate_limited", time.Now().Add(5*time.Minute))
	check := astraTargetValidation{sourceID: 299, quotaKey: quota, negativeKey: negative, route: codexGatewayRoute{cookie: http.Cookie{Value: "known"}, proxy: "donor-exit"}}
	pool.targetMu.Lock()
	pool.mu.Lock()
	retry := wrapper.targetRetryDeadline(pool, check, 300, "gpt-6-astra", time.Now())
	pool.mu.Unlock()
	pool.targetMu.Unlock()
	require.True(t, retry.until.IsZero(), "unknown donor actual exit must not inherit another exit's quota")
}
