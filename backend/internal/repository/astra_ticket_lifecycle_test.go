package repository

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type unifiedAstraProvider struct {
	service.AstraTicketLifecycle
	enabled                                        bool
	calls, released, prepared, verified, snapshots int
	err                                            error
	mutateOnFailure                                bool
}

func (p *unifiedAstraProvider) UnifiedAstraTicketsEnabled(context.Context) bool { return p.enabled }
func (p *unifiedAstraProvider) PrepareStoredAstraSources(context.Context) error {
	p.prepared++
	return p.err
}
func (p *unifiedAstraProvider) VerifyStoredAstraTarget(context.Context, int64) error {
	p.verified++
	return p.err
}
func (p *unifiedAstraProvider) StoredAstraSnapshot(context.Context) service.AstraGatewayRuntime {
	p.snapshots++
	return service.AstraGatewayRuntime{ReadyRoutes: 1, Targets: []service.AstraRouteStatus{{AccountID: 300, State: "ready", Reason: "target_probe_passed"}}}
}
func (p *unifiedAstraProvider) ApplyStoredAstraTarget(_ context.Context, h http.Header, _ int64, protocol string) (string, func(), error) {
	p.calls++
	if p.err != nil {
		if p.mutateOnFailure {
			h.Set("Cookie", "partial-borrow")
			h.Set("Authorization", "partial-auth")
		}
		return "", func() {}, p.err
	}
	h.Set("Cookie", "__cflb=target-lb; __oailb=target-route")
	h.Set("X-Codex-Turn-State", "verified-target-"+protocol)
	return "http://verified-exit.invalid", func() { p.released++ }, nil
}

func TestUnifiedAstraDiagnosticsNeverUseProvider(t *testing.T) {
	p := &unifiedAstraProvider{enabled: true}
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	s := &astraRoutingUpstream{cfg: cfg, tickets: p}
	ctx := t.Context()
	require.Error(t, s.PrepareAstraGateway(ctx))
	require.Error(t, s.VerifyAstraGatewayTarget(ctx, pinRequest(t), "fixed", 300, 1))
	require.Zero(t, s.AstraGatewaySnapshot(ctx).ReadyRoutes)
	h := http.Header{"Cookie": {"unrelated=keep"}}
	before := h.Clone()
	route, proxy, release, err := s.CodexGatewayPinWSRequest(ctx, h, "fixed", 300, 1)
	require.NoError(t, err)
	release()
	require.Empty(t, route)
	require.Equal(t, "fixed", proxy)
	require.Equal(t, before, h)
	require.Zero(t, p.calls)
	require.Zero(t, p.prepared)
	require.Zero(t, p.verified)
	require.Zero(t, p.snapshots)
}

// A ready unified transport bundle cannot replace the strict account IQ path.
func TestUnifiedAstraAdapterCannotBypassRequiredBorrow(t *testing.T) {
	for _, mode := range []string{"service_scope", "auto_quality", "account_quality", "account_scheduling"} {
		t.Run(mode, func(t *testing.T) {
			settings := config.AstraRoutingSettings{Revision: "strict", CookiePool: pinConfig()}
			switch mode {
			case "auto_quality":
				settings.AutoQuality = true
			case "account_quality":
				settings.QualityMode = config.GatewayBorrowAccountQualityMode
			case "account_scheduling":
				settings.AccountScheduling = true
			}
			cfg := &config.Config{}
			cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
			provider := &unifiedAstraProvider{enabled: true}
			business := 0
			upstream := &astraRoutingUpstream{cfg: cfg, tickets: provider, delegate: gatewayPinDelegate{call: func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
				business++
				return pinResponse(""), nil
			}}}
			req := pinRequest(t).WithContext(t.Context())
			if mode == "service_scope" {
				req = req.WithContext(service.WithGatewayBorrowRequiredModel(req.Context(), "gpt-6-astra"))
			}
			_, err := upstream.Do(req, "native-exit", 300, 1)
			require.Error(t, err, "no quality route must remain fail-closed")
			require.Zero(t, business, "neither ready own ticket nor native fallback sends business")
			require.Zero(t, provider.calls, "strict account must not use optional unified bundle")
			if mode != "service_scope" {
				upstream.AstraGatewaySnapshot(t.Context())
				require.Zero(t, provider.snapshots, "transport ticket readiness must not replace IQ evidence")
				_, _, release, wsErr := upstream.CodexGatewayPinWSRequestForModel(t.Context(), req.Header, "native-exit", 300, 1, "gpt-6-astra")
				release()
				require.Error(t, wsErr)
				require.Zero(t, provider.calls)
			}
		})
	}
}
