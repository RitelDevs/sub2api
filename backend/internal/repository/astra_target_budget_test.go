package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAstraTargetProbeDeadlineHonorsAccountRoundSourceLeaseAndCaller(t *testing.T) {
	for _, tc := range []struct {
		name, mode          string
		caller, lease, want time.Duration
	}{
		{"account_four_shots", config.GatewayBorrowAccountQualityMode, 300 * time.Second, 230 * time.Second, 180 * time.Second},
		{"legacy_two_shots", "", 300 * time.Second, 230 * time.Second, 90 * time.Second},
		{"short_actual_cookie", config.GatewayBorrowAccountQualityMode, 300 * time.Second, 70 * time.Second, 70 * time.Second},
		{"short_caller", config.GatewayBorrowAccountQualityMode, 25 * time.Second, 230 * time.Second, 25 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			caller, stopCaller := context.WithDeadline(t.Context(), now.Add(tc.caller))
			defer stopCaller()
			round, stopRound := astraTargetProbeContext(caller, now.Add(tc.lease), tc.mode)
			defer stopRound()
			deadline, exists := round.Deadline()
			require.True(t, exists)
			require.WithinDuration(t, now.Add(tc.want), deadline, 100*time.Millisecond)
			require.False(t, deadline.After(now.Add(tc.lease)), "new budget cannot extend the source Cookie")
			stopCaller()
			require.ErrorIs(t, round.Err(), context.Canceled, "caller cancellation remains immediate")
		})
	}
}
