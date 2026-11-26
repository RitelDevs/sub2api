package service

import (
	"context"
	"sort"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	DefaultReloginMaxFailures = 3
	DefaultReloginBackoffMax  = 3600
	// A lost claim cannot tell us whether the login reached the provider.
	ReloginOutcomeUnknown = "re-login outcome unknown; reconcile before retry"
)

type AccountReloginOutcome struct {
	ID         int64
	Status     string
	FinishedAt *time.Time
	Error      string
}

type AccountReloginBudgetRepository interface {
	ReconcileReloginBudget(context.Context, int64, string, AccountTokenGuardV2Rules, time.Time, string) (*AccountTokenGuardV2Record, bool, error)
	CreateAutomaticRelogin(context.Context, int64, string, string, AccountTokenGuardV2Rules, time.Time) (*OpenAIOAuthReauthTaskRecord, error)
	ResumeReloginBudget(context.Context, int64, AccountTokenGuardV2Rules, time.Time) error
}

func NormalizeReloginBudgetRules(rules AccountTokenGuardV2Rules) AccountTokenGuardV2Rules {
	if rules.ReloginMaxFailures == 0 {
		rules.ReloginMaxFailures = DefaultReloginMaxFailures
	}
	if rules.ReloginBackoffMaxSeconds == 0 {
		rules.ReloginBackoffMaxSeconds = max(DefaultReloginBackoffMax, rules.ReloginCooldownSeconds)
	}
	return rules
}

// ReloginBackoff doubles without overflow, capped by the configured maximum.
func ReloginBackoff(rules AccountTokenGuardV2Rules, failures int) time.Duration {
	rules = NormalizeReloginBudgetRules(rules)
	seconds := rules.ReloginCooldownSeconds
	for failures > 1 && seconds < rules.ReloginBackoffMaxSeconds {
		seconds = min(rules.ReloginBackoffMaxSeconds, seconds*2)
		failures--
	}
	return time.Duration(seconds) * time.Second
}

// AdvanceAccountReloginBudget is clock-independent. A lower pending task holds
// the watermark, so later completions cannot hide an out-of-order failure.
// The repository serializes this transition with the guard row and task locks.
func AdvanceAccountReloginBudget(record AccountTokenGuardV2Record, outcomes []AccountReloginOutcome, rules AccountTokenGuardV2Rules, probeAt time.Time, probeState string) (AccountTokenGuardV2Record, bool) {
	return AdvanceAccountReloginBudgetPage(record, outcomes, rules, probeAt, probeState, true)
}

// A partial page can advance the accounting watermark using the real probe
// result, but cannot prove that its last success is the latest task overall.
// Keep the budget until a complete page confirms there is no later outcome.
func AdvanceAccountReloginBudgetPage(record AccountTokenGuardV2Record, outcomes []AccountReloginOutcome, rules AccountTokenGuardV2Rules, probeAt time.Time, probeState string, historyComplete bool) (AccountTokenGuardV2Record, bool) {
	rules = NormalizeReloginBudgetRules(rules)
	watermark := record.LastCountedTaskID
	hadFailureBudget := record.ReloginFailStreak > 0 || record.AutoReloginExhausted
	ordered := append([]AccountReloginOutcome(nil), outcomes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	var latest *AccountReloginOutcome
	pending := false
	for i := range ordered {
		outcome := &ordered[i]
		if outcome.ID < record.LastCountedTaskID {
			continue
		}
		if outcome.FinishedAt == nil || (outcome.Status != OpenAIOAuthReauthStatusFailed && outcome.Status != OpenAIOAuthReauthStatusSucceeded) {
			pending = true
			break
		}
		latest = outcome
		if outcome.ID == record.LastCountedTaskID {
			continue
		}
		if outcome.Status == OpenAIOAuthReauthStatusSucceeded &&
			(outcome.FinishedAt.After(probeAt) || (probeState != AccountTokenGuardV2ProbeOK && probeState != AccountTokenGuardV2ProbeAuth)) {
			pending = true
			break
		}
		record.LastCountedTaskID = outcome.ID
		if outcome.Status == OpenAIOAuthReauthStatusFailed {
			if outcome.Error == ReloginOutcomeUnknown {
				record.AutoReloginExhausted = true
			}
			_, diagnostic, handled := parseReauthIdentityError(outcome.Error)
			if !handled {
				_, diagnostic, handled = localProtocolError(outcome.Error)
			}
			if !handled {
				_, diagnostic, _ = sessionStudioError(outcome.Error)
			}
			if reloginNeedsReconciliation(diagnostic) {
				record.AutoReloginExhausted = true
			}
		}
		if outcome.Status == OpenAIOAuthReauthStatusFailed ||
			(outcome.Status == OpenAIOAuthReauthStatusSucceeded && probeState == AccountTokenGuardV2ProbeAuth) {
			record.ReloginFailStreak = min(record.ReloginFailStreak+1, 1000000)
			until := outcome.FinishedAt.Add(ReloginBackoff(rules, record.ReloginFailStreak))
			if record.CooldownUntil == nil || until.After(*record.CooldownUntil) {
				record.CooldownUntil = &until
			}
		}
	}
	if record.ReloginFailStreak >= rules.ReloginMaxFailures {
		record.AutoReloginExhausted = true
	}
	// A saved token is not a verified token. A probe started before success,
	// an older success, or a newer active task must never reset the budget.
	if historyComplete && probeState == AccountTokenGuardV2ProbeOK && !pending && latest != nil && latest.ID == record.LastCountedTaskID &&
		latest.Status == OpenAIOAuthReauthStatusSucceeded && !latest.FinishedAt.After(probeAt) &&
		(latest.ID > watermark || hadFailureBudget) {
		record.ReloginFailStreak = 0
		record.AutoReloginExhausted = false
		record.CooldownUntil = nil
	}
	return record, pending
}

func reloginNeedsReconciliation(d *ReauthDiagnostic) bool {
	if d == nil {
		return false
	}
	if d.Identity != nil {
		return true
	}
	switch d.Reason {
	case "security_check", "email_otp_required", "interaction_required", "phone_verification_required", "mfa_required",
		"authorization_denied", "account_deactivated", "account_deleted", "account_retired":
		return true
	}
	if d.Engine != OpenAIOAuthReauthEngineSessionStudio {
		return false
	}
	// The provider may have received the request, or issued credentials, before
	// the response stream or the worker's write-back was interrupted.
	return d.Stage == "event_stream" || d.Stage == "credentials_submit" ||
		d.Stage == "credentials_validation" || (d.Stage == "http_request" && d.Reason == "transport_error")
}

func (s *AccountTokenGuardV2Service) ResumeReloginBudget(ctx context.Context, id int64) (*AccountTokenGuardV2Account, error) {
	repo, ok := s.repo.(AccountReloginBudgetRepository)
	if !ok {
		return nil, infraerrors.ServiceUnavailable("TOKEN_GUARD_V2_BUDGET_UNAVAILABLE", "Re-login budget is unavailable")
	}
	rules, err := s.GetRules(ctx)
	if err != nil {
		return nil, err
	}
	if err := repo.ResumeReloginBudget(ctx, id, rules, s.clockNow()); err != nil {
		return nil, err
	}
	return s.accountView(ctx, id)
}

func (s *AccountTokenGuardV2Service) clockNow() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}
