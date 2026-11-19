package repository

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAdaptiveBorrowQualityBackoffIsBoundedAndIdentityScoped(t *testing.T) {
	wrapper := &astraRoutingUpstream{}
	req, _ := http.NewRequest("POST", "https://chatgpt.com/backend-api/codex/responses", nil)
	req.Header.Set("Authorization", "Bearer fixture")
	quota, key := targetBorrowQuietKeys(req, 300, "gpt-6-astra", "exit", 299, nil)
	now := time.Now()
	for _, want := range []time.Duration{180 * time.Second, 360 * time.Second, 720 * time.Second, 900 * time.Second, 900 * time.Second} {
		until := wrapper.rememberTargetQualityFailure(key, "target_probe_degraded", now)
		require.Equal(t, want, until.Sub(now))
		before := wrapper.quotaBackoff[key].failures
		_, waiting := wrapper.targetQuotaWait(key, now.Add(time.Second))
		require.True(t, waiting)
		require.Equal(t, before, wrapper.quotaBackoff[key].failures, "quiet cache read cannot increase tier")
		require.Equal(t, until, wrapper.rememberTargetQualityFailure(key, "target_probe_degraded", now.Add(time.Second)), "duplicate in quiet window cannot slide it")
		now = until.Add(time.Second)
	}
	wrapper.rememberTargetQuota(quota, "target_probe_rate_limited", now.Add(300*time.Second))
	require.Zero(t, wrapper.quotaBackoff[quota].failures)
	req.Header.Set("Authorization", "Bearer changed")
	_, newKey := targetBorrowQuietKeys(req, 300, "gpt-6-astra", "exit", 299, nil)
	require.Equal(t, 180*time.Second, wrapper.rememberTargetQualityFailure(newKey, "target_quality_failed", now).Sub(now))
	_, otherSource := targetBorrowQuietKeys(req, 300, "gpt-6-astra", "exit", 298, nil)
	_, waiting := wrapper.targetQuotaWait(otherSource, now)
	require.False(t, waiting)
	wrapper.resetTargetQualityFailure(key)
	require.Equal(t, 180*time.Second, wrapper.rememberTargetQualityFailure(key, "target_probe_degraded", now).Sub(now), "success resets source-specific failure history")
	_, waiting = wrapper.targetQuotaWait(key, now.Add(time.Hour+time.Second))
	require.False(t, waiting)
	_, exists := wrapper.quotaBackoff[key]
	require.False(t, exists, "old counts are collected after one hour")
	for i := 0; i < 4100; i++ {
		k := astraTargetQuotaKey{accountID: int64(i + 1), model: "gpt-6-astra"}
		wrapper.rememberTargetQualityFailure(k, "target_quality_failed", now)
	}
	require.LessOrEqual(t, len(wrapper.quotaBackoff), 4096)
}
