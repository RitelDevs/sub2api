//go:build unit

package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"regexp"
	"testing"
)

func TestKeyRequestBillingCanonicalReceiptStates(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		cost, quote, state, event any
		expected, costExpected    string
	}{
		{"complete", "0.00007813", "fingerprint", "complete", "fingerprint", "final", "0.00007813"},
		{"zero is explicit final", "0.00000000", "fingerprint", "applied", "fingerprint", "final", "0.00000000"},
		{"direct atomic charge", "0.25000000", "fingerprint", nil, nil, "final", "0.25000000"},
		{"not yet journaled", nil, nil, nil, nil, "unknown", ""},
		{"journal pending", nil, nil, "pending", "fingerprint", "pending", ""},
		{"failed billing", nil, nil, "blocked", "fingerprint", "blocked", ""},
		{"identity conflict", "0.1", "before", "complete", "after", "blocked", ""},
		{"legacy log without canonical money", nil, nil, "legacy", "fingerprint", "unknown", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectQuery(regexp.QuoteMeta(keyRequestBillingSQL)).WithArgs(int64(82), "client:only-this-request").
				WillReturnRows(sqlmock.NewRows([]string{"cost", "quote", "state", "event"}).AddRow(tc.cost, tc.quote, tc.state, tc.event))
			receipt, err := (&usageLogRepository{sql: db}).GetAPIKeyRequestBilling(context.Background(), 82, "client:only-this-request")
			require.NoError(t, err)
			require.Equal(t, tc.expected, receipt.Status)
			require.Equal(t, tc.costExpected, receipt.ActualCost)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
