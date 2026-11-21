package service

import (
	"context"
	"time"
)

func gatewayBorrowSourceAccountBlock(account *Account, now time.Time) (string, *time.Time) {
	reason, retry := gatewayBorrowAccountBlock(account, "gpt-6-astra", now)
	switch reason {
	case "target_account_rate_limited":
		return "source_account_rate_limited", retry
	case "target_account_unavailable":
		return "source_account_unavailable", retry
	}
	if account != nil && account.RequiresGatewayBorrowUpstream("gpt-6-astra") {
		return "source_account_unavailable", nil
	}
	return "", nil
}

type gatewayBorrowSourceRetry struct {
	credential [32]byte
	until      time.Time
}

func (s *AccountTestService) gatewayBorrowSourceBlock(account *Account, now time.Time) (string, *time.Time) {
	if reason, retry := gatewayBorrowSourceAccountBlock(account, now); reason != "" {
		return reason, retry
	}
	if raw, found := s.gatewayBorrowSourceRetry.Load(account.ID); found {
		retry, ok := raw.(gatewayBorrowSourceRetry)
		if ok && retry.credential == gatewayBorrowCredentialFingerprint(account) && now.Before(retry.until) {
			return "source_account_rate_limited", &retry.until
		}
		s.gatewayBorrowSourceRetry.Delete(account.ID)
	}
	return "", nil
}

// Legacy callers must not look up source credentials or open a connection.
func (*AccountTestService) prepareAutomaticGatewayBorrowSource(context.Context, int64) error {
	return denyOpenAITurn("gateway_borrow_retired")
}
