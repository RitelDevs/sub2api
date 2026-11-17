package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// The canonical quote is committed in the same transaction as customer charging.
// Usage logs alone (including zero-cost error rows) are not settlement authority.
const keyRequestBillingSQL = `SELECT q.canonical_actual_cost::text,q.request_fingerprint,e.state,e.request_fingerprint
    FROM (SELECT $1::bigint AS api_key_id,$2::text AS request_id) k
    LEFT JOIN usage_billing_quotes q ON q.api_key_id=k.api_key_id AND q.request_id=k.request_id
    LEFT JOIN usage_billing_events e ON e.api_key_id=k.api_key_id AND e.request_id=k.request_id`

func (r *usageLogRepository) GetAPIKeyRequestBilling(ctx context.Context, keyID int64, requestID string) (*service.APIKeyRequestBilling, error) {
	var cost, quoteFingerprint, state, eventFingerprint sql.NullString
	if err := scanSingleRow(ctx, r.sql, keyRequestBillingSQL, []any{keyID, requestID}, &cost, &quoteFingerprint, &state, &eventFingerprint); err != nil {
		return nil, err
	}
	result := &service.APIKeyRequestBilling{Status: "unknown"}
	if state.String == "blocked" || (quoteFingerprint.Valid && eventFingerprint.Valid && quoteFingerprint.String != eventFingerprint.String) {
		result.Status = "blocked"
		return result, nil
	}
	if state.String == "pending" {
		result.Status = "pending"
		return result, nil
	}
	if cost.Valid && (!state.Valid || state.String == "applied" || state.String == "complete" || state.String == "legacy") {
		result.Status = "final"
		result.ActualCost = cost.String
		return result, nil
	}
	if state.Valid && state.String != "applied" && state.String != "complete" && state.String != "legacy" {
		return nil, errors.New("unsupported request billing state")
	}
	// Legacy/incomplete journals without a canonical receipt require reconciliation, not a guessed zero.
	return result, nil
}
