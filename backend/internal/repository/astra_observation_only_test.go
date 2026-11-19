package repository

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestUnifiedAstraFailedOptionalBundlePreservesOriginalHeaders(t *testing.T) {
	for _, code := range []string{"source_probe_cooldown", "target_probe_failed", "target_validation_in_progress", "configuration_changed"} {
		t.Run(code, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.CodexGatewayPin = pinConfig()
			p := &unifiedAstraProvider{enabled: true, err: errors.New(code), mutateOnFailure: true}
			calls := 0
			s := &astraRoutingUpstream{cfg: cfg, tickets: p}
			r := pinRequest(t).WithContext(t.Context())
			r.Header.Set("Cookie", "original")
			before := r.Header.Clone()
			s.delegate = gatewayPinDelegate{call: func(got *http.Request, proxy string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				calls++
				require.Equal(t, "original", proxy)
				require.Equal(t, before, got.Header)
				return pinResponse(""), nil
			}}
			resp, err := s.Do(r, "original", 300, 1)
			consumeAffinityResponse(t, resp, err)
			require.Equal(t, 1, calls)
			cookie, proxy, release, err := s.CodexGatewayPinWSRequest(t.Context(), r.Header, "original", 300, 1)
			require.NoError(t, err)
			release()
			require.Empty(t, cookie)
			require.Equal(t, "original", proxy)
			require.Equal(t, before, r.Header)
			require.Zero(t, p.calls)
			require.Zero(t, p.prepared)
			require.Zero(t, p.verified)
		})
	}
}
