//go:build unit

package migrations

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigration251AddsExpiredFloorAndNonBlockingBuildMarks(t *testing.T) {
	content, err := FS.ReadFile("251_group_usage_rollup_nonblocking_build.sql")
	require.NoError(t, err)

	sql := string(content)
	require.Contains(t, sql, "SET LOCAL lock_timeout")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS expired_before TIMESTAMPTZ NOT NULL")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS building_until DATE")
	require.Contains(t, sql, "ADD COLUMN IF NOT EXISTS build_dirty_from DATE")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION apply_group_usage_rollup_invalidation")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION invalidate_group_usage_rollups_at")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state()")
	require.Contains(t, sql, "CREATE OR REPLACE FUNCTION invalidate_group_usage_rollup_state_after_insert()")
	require.Contains(t, sql, "created_at >= state_expired_before")
	// 源记录变更只对状态行持 KEY SHARE，与 usage_logs 写入互不阻塞。
	require.Contains(t, sql, "FOR KEY SHARE")
	require.NotContains(t, sql, "FOR UPDATE;")
}
