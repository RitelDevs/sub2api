package service

import (
	"context"
	"net/http"
	"time"
)

// AstraTicketLifecycle joins gateway reuse to the account ticket store. It is
// installed at construction, before either background collection or HTTP starts.
// Ticket values are applied directly to request headers; public DTOs contain
// only readiness metadata, never credential or routing cookie values.
type AstraTicketLifecycle interface {
	UnifiedAstraTicketsEnabled(context.Context) bool
	PrepareStoredAstraSources(context.Context) error
	VerifyStoredAstraTarget(context.Context, int64) error
	ApplyStoredAstraTarget(context.Context, http.Header, int64, string) (string, func(), error)
	StoredAstraSnapshot(context.Context) AstraGatewayRuntime
}

type boundAstraTicketContextKey struct{}

// IsBoundAstraTicketRequest prevents a later transport wrapper from replacing
// one half of a verified credential bundle. This marker is never an HTTP header.
func IsBoundAstraTicketRequest(ctx context.Context) bool {
	bound, _ := ctx.Value(boundAstraTicketContextKey{}).(bool)
	return bound
}

func (s *OpenAIGatewayService) UnifiedAstraTicketsEnabled(ctx context.Context) bool {
	return false
}

func astraTicketAccountEligible(ctx context.Context, a *Account) bool {
	if a != nil && (a.AuthConnectionID != "" || a.HasManagedOpenAIConnections()) {
		return false
	}
	// Explicit preparation can restore a target paused by Astra scheduling, but
	// never bypasses authentication, expiry, transport or upstream cooldowns.
	if a == nil || a.gatewayBorrowPolicyActive() || !a.IsActive() || !isOpenAICodexTicketAccount(a, OpenAICodexStateProbeDefaultModel) || a.ParentAccountID != nil {
		return false
	}
	now := time.Now()
	return (a.ExpiresAt == nil || now.Before(*a.ExpiresAt)) && !a.IsRateLimited() && !a.IsOverloaded() &&
		(a.TempUnschedulableUntil == nil || !now.Before(*a.TempUnschedulableUntil)) &&
		!a.isModelRateLimitedWithContext(ctx, OpenAICodexStateProbeDefaultModel)
}

func (s *OpenAIGatewayService) storedAstraDonor(ctx context.Context, targetID int64) (*Account, *openAICodexTicket) {
	return nil, nil
}

func (s *OpenAIGatewayService) PrepareStoredAstraSources(ctx context.Context) error {
	return denyOpenAITurn("gateway_borrow_retired")
}

// tryBorrowStoredAstraTicket runs at most one donor candidate. The caller owns
// the harvest lock and request budget; failure may fall back to self collection.
func (s *OpenAIGatewayService) tryBorrowStoredAstraTicket(ctx context.Context, account *Account, model string, timeout time.Duration, reserve func() bool) (bool, codexHarvestProbeResult) {
	return false, codexHarvestProbeResult{Terminal: true}
}

func (s *OpenAIGatewayService) acquireStoredAstraTarget(ctx context.Context, id int64, protocol string) (*Account, *openAICodexTicket, error) {
	return nil, nil, denyOpenAITurn("gateway_borrow_retired")
}

func (s *OpenAIGatewayService) VerifyStoredAstraTarget(ctx context.Context, id int64) error {
	_, _, err := s.acquireStoredAstraTarget(ctx, id, "sse")
	return err
}

func (s *OpenAIGatewayService) ApplyStoredAstraTarget(ctx context.Context, h http.Header, id int64, protocol string) (string, func(), error) {
	return "", func() {}, denyOpenAITurn("gateway_borrow_retired")
}

func (s *OpenAIGatewayService) StoredAstraSnapshot(ctx context.Context) AstraGatewayRuntime {
	return AstraGatewayRuntime{GeneratedAt: time.Now(), Sources: []AstraRouteStatus{}, Targets: []AstraRouteStatus{}, WS: []AstraWSStatus{}}
}

// Compile-time assertions keep the repository adapter independent of service internals.
var _ AstraTicketLifecycle = (*OpenAIGatewayService)(nil)

func astraTicketCooldown(seconds int, retry time.Duration) time.Duration {
	d := time.Duration(seconds) * time.Second
	if retry > d {
		d = retry
	}
	if d < time.Second {
		d = time.Second
	}
	return d
}
