package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAstraRotationCacheHostIsolationAndDeadline(t *testing.T) {
	c := &astraRotationCache{}
	now := time.Now()
	until := now.Add(time.Hour)
	c.observe("node-a", "host-1", now)
	c.observe("node-b", "host-1", now)
	c.reject(300, "node-a", "host-1", until, now)
	require.Equal(t, until, c.blockedUntil(300, "node-b", "", now), "another exit with same known host must not refresh that target")
	require.True(t, c.blockedUntil(301, "node-b", "", now).IsZero(), "another target is independent")
	c.reject(300, "node-a", "host-1", until.Add(time.Hour), now.Add(time.Minute))
	require.Equal(t, until, c.blockedUntil(300, "node-a", "", now.Add(time.Minute)), "duplicate failure does not slide quiet window")
	require.True(t, c.blockedUntil(300, "node-b", "", until.Add(time.Second)).IsZero())
}

// These regressions exercise mandatory route quality, rather than an
// optional upstream transport diagnostic. The request scope is service-owned.

func TestAstraSourceCooldownDoesNotBlockOtherDonorsOrTargets(t *testing.T) {
	c := &astraRotationCache{}
	now := time.Now()
	c.rejectSource(299, "node", now)
	require.True(t, c.sourceCooling(299, "node", now))
	require.False(t, c.sourceCooling(298, "node", now))
	require.False(t, c.sourceCooling(299, "different-node", now))
	require.True(t, c.blockedUntil(299, "node", "", now).IsZero())
	c.rejectSource(299, "node", now.Add(30*time.Second))
	require.False(t, c.sourceCooling(299, "node", now.Add(time.Minute)))
}

func TestSourceSearchPauseDoesNotSlide(t *testing.T) {
	c := &astraRotationCache{}
	now := time.Now()
	c.pauseSourceSearch(300, now)
	c.pauseSourceSearch(300, now.Add(4*time.Minute))
	require.True(t, c.sourceSearchPaused(300, now.Add(4*time.Minute)))
	require.False(t, c.sourceSearchPaused(300, now.Add(5*time.Minute)))
	require.False(t, c.sourceSearchPaused(343, now))
}
