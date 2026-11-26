//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Uses the existing container-only fixture and its own migrated database. No
// notification worker or network sender is constructed.
func TestUsageBillingDeliveryOverflowAtomicRecoveryAndConcurrentDedup(t *testing.T) {
	for _, failAt := range []string{"outbox", "usage"} {
		t.Run(failAt, func(t *testing.T) {
			ctx := context.Background()
			repo, cmd, usage, client := durableBillingFixture(t)
			db := repo.db
			source := mustCreateGroup(t, client, &service.Group{Name: "overflow-source-" + uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription})
			target := mustCreateGroup(t, client, &service.Group{Name: "overflow-target-" + uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard})
			sub := mustCreateSubscription(t, client, &service.UserSubscription{UserID: cmd.UserID, GroupID: source.ID})
			raw := 0.000078125
			cmd.BalanceCost, cmd.APIKeyQuotaCost, cmd.QuotedActualCost = raw, raw, &raw
			cmd.SubscriptionOverflow = &service.SubscriptionOverflowBillingEvidence{
				SubscriptionID: sub.ID, SourceGroupID: source.ID, TargetGroupID: target.ID,
				Dimension: "daily", WindowStart: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
				DailyUsageUSD: 10, DailyLimitUSD: 10, WeeklyUsageUSD: 20, WeeklyLimitUSD: 70,
				MonthlyUsageUSD: 30, MonthlyLimitUSD: 300, RateMultiplier: 0.6,
			}
			usage.GroupID, usage.ActualCost, usage.TotalCost = &target.ID, service.QuantizeUsageBillingAmount(raw), raw
			require.NoError(t, repo.StageUsageBilling(ctx, &cmd, &usage))
			key := service.UsageBillingEventKey{RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID}

			name := "overflow_fail_" + strings.ReplaceAll(uuid.NewString(), "-", "")
			table := "subscription_overflow_email_outbox"
			if failAt == "usage" {
				table = "usage_logs"
			}
			// Failure at the outbox must roll back earlier monetary effects;
			// failure at usage must also roll back the already inserted notice.
			_, err := db.ExecContext(ctx, fmt.Sprintf(
				"CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.user_id=%d THEN RAISE EXCEPTION 'injected durable overflow failure'; END IF; RETURN NEW; END; $$; CREATE TRIGGER %s BEFORE INSERT ON %s FOR EACH ROW EXECUTE FUNCTION %s()",
				name, cmd.UserID, name, table, name))
			require.NoError(t, err)
			cleanup := func() {
				_, err := db.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s; DROP FUNCTION IF EXISTS %s()", name, table, name))
				require.NoError(t, err)
			}
			t.Cleanup(cleanup)
			_, err = repo.ApplyStagedUsageBilling(ctx, key)
			require.ErrorContains(t, err, "injected durable overflow failure")

			assertState := func(balance, quota, state string, count int) {
				t.Helper()
				var actualBalance, actualQuota, actualState string
				var claims, quotes, logs, notices int
				require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text FROM users WHERE id=$1", cmd.UserID).Scan(&actualBalance))
				require.NoError(t, db.QueryRowContext(ctx, "SELECT quota_used::text FROM api_keys WHERE id=$1", cmd.APIKeyID).Scan(&actualQuota))
				require.NoError(t, db.QueryRowContext(ctx, "SELECT state FROM usage_billing_events WHERE request_id=$1 AND api_key_id=$2", cmd.RequestID, cmd.APIKeyID).Scan(&actualState))
				require.NoError(t, db.QueryRowContext(ctx, `SELECT
					(SELECT count(*) FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2),
					(SELECT count(*) FROM usage_billing_quotes WHERE request_id=$1 AND api_key_id=$2),
					(SELECT count(*) FROM usage_logs WHERE request_id=$1 AND api_key_id=$2),
					(SELECT count(*) FROM subscription_overflow_email_outbox WHERE user_id=$3)`,
					cmd.RequestID, cmd.APIKeyID, cmd.UserID).Scan(&claims, &quotes, &logs, &notices))
				require.Equal(t, balance, actualBalance)
				require.Equal(t, quota, actualQuota)
				require.Equal(t, state, actualState)
				require.Equal(t, []int{count, count, count, count}, []int{claims, quotes, logs, notices})
			}
			assertState("100.00000000", "0.00000000", "pending", 0)
			cleanup()

			var wg sync.WaitGroup
			errs := make(chan error, 2)
			results := make(chan *service.UsageBillingRecovery, 2)
			for i := 0; i < 2; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					recovered, err := (&usageBillingRepository{db: db}).ApplyStagedUsageBilling(ctx, key)
					errs <- err
					results <- recovered
				}()
			}
			wg.Wait()
			close(errs)
			close(results)
			for err := range errs {
				require.NoError(t, err)
			}
			applied := 0
			for recovered := range results {
				require.NotNil(t, recovered)
				require.Equal(t, cmd.SubscriptionOverflow, recovered.Event.Command.SubscriptionOverflow)
				if recovered.Result.Applied {
					applied++
				}
			}
			require.Equal(t, 1, applied)
			assertState("99.99992187", "0.00007813", "applied", 1)

			var payload []byte
			require.NoError(t, db.QueryRowContext(ctx, "SELECT payload FROM subscription_overflow_email_outbox WHERE user_id=$1", cmd.UserID).Scan(&payload))
			var notice service.SubscriptionOverflowNotificationPayload
			require.NoError(t, json.Unmarshal(payload, &notice))
			require.Equal(t, *cmd.SubscriptionOverflow, notice.SubscriptionOverflowBillingEvidence)
			require.Equal(t, cmd.UserID, notice.UserID)
			require.Equal(t, 0.00007813, notice.BalanceCost)
			require.Equal(t, 99.99992187, notice.BalanceAfter)
			require.Equal(t, source.Name, notice.SourceGroupName)
			require.Equal(t, target.Name, notice.TargetGroupName)
			require.False(t, notice.ChargedAt.IsZero())
			var quote, canonical string
			var version int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT quoted_actual_cost::text,canonical_actual_cost::text,quote_encoding_version FROM usage_billing_quotes WHERE request_id=$1 AND api_key_id=$2", cmd.RequestID, cmd.APIKeyID).Scan(&quote, &canonical, &version))
			require.Equal(t, "0.000078125", quote)
			require.Equal(t, "0.00007813", canonical)
			require.Equal(t, 1, version)
			require.NoError(t, repo.AcknowledgeUsageBillingCache(ctx, key))
			replayed, err := repo.ApplyStagedUsageBilling(ctx, key)
			require.NoError(t, err)
			require.False(t, replayed.Result.Applied)
			require.False(t, replayed.NeedsCache)
			assertState("99.99992187", "0.00007813", "complete", 1)
		})
	}
}

func TestStagedBillingBatchOverflowNoticeIsAtomicAndDeduplicated(t *testing.T) {
	ctx := context.Background()
	repo, cmd, usage, client := durableBillingFixture(t)
	source := mustCreateGroup(t, client, &service.Group{Name: "overflow-source-" + uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeSubscription})
	target := mustCreateGroup(t, client, &service.Group{Name: "overflow-target-" + uuid.NewString(), Platform: service.PlatformOpenAI, SubscriptionType: service.SubscriptionTypeStandard})
	sub := mustCreateSubscription(t, client, &service.UserSubscription{UserID: cmd.UserID, GroupID: source.ID})
	start := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	quote := 0.9
	cmd.BalanceCost, cmd.APIKeyQuotaCost, cmd.QuotedActualCost = quote, quote, &quote
	usage.GroupID, usage.ActualCost, usage.TotalCost = &target.ID, quote, quote
	owner := strconv.FormatInt(cmd.UserID, 10)
	var keys []service.UsageBillingEventKey
	for _, day := range []int{0, 0, 1} {
		cmd.RequestID, cmd.RequestFingerprint = uuid.NewString(), ""
		cmd.SubscriptionOverflow = &service.SubscriptionOverflowBillingEvidence{
			SubscriptionID: sub.ID, SourceGroupID: source.ID, TargetGroupID: target.ID,
			Dimension: "daily", WindowStart: start.AddDate(0, 0, day),
			DailyUsageUSD: 10, DailyLimitUSD: 10, RateMultiplier: 1,
		}
		usage.RequestID = cmd.RequestID
		require.NoError(t, repo.StageUsageBilling(ctx, &cmd, &usage))
		keys = append(keys, service.UsageBillingEventKey{RequestID: cmd.RequestID, APIKeyID: cmd.APIKeyID})
	}

	assertState := func(wantBalance string, wantPending, wantDebits, wantNotices int) {
		t.Helper()
		var balance string
		var pending, debits, notices int
		require.NoError(t, repo.db.QueryRowContext(ctx, `SELECT balance::text,
			(SELECT count(*) FROM usage_billing_events WHERE api_key_id=$2 AND state='pending'),
			(SELECT count(*) FROM usage_billing_quotes WHERE api_key_id=$2 AND wallet_debit=0.9),
			(SELECT count(*) FROM subscription_overflow_email_outbox WHERE user_id=$1)
			FROM users WHERE id=$1`, cmd.UserID, cmd.APIKeyID).Scan(&balance, &pending, &debits, &notices))
		require.Equal(t, wantBalance, balance)
		require.Equal(t, wantPending, pending)
		require.Equal(t, wantDebits, debits)
		require.Equal(t, wantNotices, notices)
	}

	name := "overflow_batch_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := repo.db.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN RAISE EXCEPTION 'injected batched notice failure'; END; $$;
		CREATE TRIGGER %s BEFORE INSERT ON subscription_overflow_email_outbox FOR EACH ROW EXECUTE FUNCTION %s()`, name, name, name))
	require.NoError(t, err)
	_, err = repo.applyStagedUsageBillingBatch(ctx, owner, keys)
	require.ErrorContains(t, err, "injected batched notice failure")
	assertState("100.00000000", 3, 0, 0)
	_, err = repo.db.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER %s ON subscription_overflow_email_outbox; DROP FUNCTION %s()", name, name))
	require.NoError(t, err)

	settled, err := repo.applyStagedUsageBillingBatch(ctx, owner, keys)
	require.NoError(t, err)
	for _, record := range settled {
		require.True(t, record.Result.Applied)
		require.True(t, record.Settled)
	}
	assertState("97.30000000", 0, 3, 2)
	var firstPayload []byte
	require.NoError(t, repo.db.QueryRowContext(ctx, `SELECT payload FROM subscription_overflow_email_outbox
		WHERE user_id=$1 AND window_start=$2`, cmd.UserID, start).Scan(&firstPayload))
	var first service.SubscriptionOverflowNotificationPayload
	require.NoError(t, json.Unmarshal(firstPayload, &first))
	require.Equal(t, 0.9, first.BalanceCost)
	require.Equal(t, 99.1, first.BalanceAfter)

	// Restarted recovery acknowledges the original charges without repeating effects.
	freshProcess := &usageBillingRepository{db: repo.db}
	for _, key := range keys {
		result, err := freshProcess.RecoverStagedUsageBilling(ctx, key)
		require.NoError(t, err)
		require.True(t, result.Settled)
		require.False(t, result.Result.Applied)
		require.NoError(t, freshProcess.AcknowledgeUsageBillingCache(ctx, key))
	}
	assertState("97.30000000", 0, 3, 2)
}
