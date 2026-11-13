package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

func useGroupUsageRepositoryTestTimezone(t *testing.T, name string) configuredTimezone {
	t.Helper()
	loc, err := time.LoadLocation(name)
	require.NoError(t, err)
	return configuredTimezone{location: loc}
}
func TestUsageTimezoneUserContextOverridesConfiguredFallback(t *testing.T) {
	configured := useGroupUsageRepositoryTestTimezone(t, "Asia/Shanghai")
	require.Equal(t, "Asia/Shanghai", configured.userTimezone(context.Background()))
	ctx := timezone.WithUserDay(context.Background(), "America/New_York")
	require.Equal(t, "America/New_York", configured.userTimezone(ctx))
	require.Equal(t, "Asia/Shanghai", configured.timezoneName(), "request timezone must not change background rollup buckets")
}
