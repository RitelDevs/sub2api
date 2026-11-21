package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAstraMembershipCannotRestoreReadiness(t *testing.T) {
	s := &astraRoutingUpstream{}
	ctx := t.Context()
	a := config.AstraRoutingSettings{Revision: "first", CookiePool: pinConfig(), AutoQuality: true}
	for _, enabled := range []bool{true, false, true} {
		b := a
		b.Revision += "-next"
		b.CookiePool.Enabled = enabled
		s.ApplyAstraRoutingSettings(a, b)
		snap := s.AstraGatewaySnapshot(ctx)
		require.Equal(t, b.Revision, snap.Revision)
		require.Zero(t, snap.ReadyRoutes)
		require.False(t, s.CodexGatewayPinWSBindingValidForRevision(ctx, 300, "gpt-6-astra", "old", a.Revision))
		require.Nil(t, s.current(ctx))
		a = b
	}
}
