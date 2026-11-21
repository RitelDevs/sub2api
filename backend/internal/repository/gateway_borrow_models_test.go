package repository

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGatewayBorrowTargetOtherModelRetainsNative(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	called := 0
	wrapper := &astraRoutingUpstream{cfg: cfg, delegate: gatewayPinDelegate{call: func(r *http.Request, p string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		called++
		require.Empty(t, r.Header.Get("Cookie"))
		require.Equal(t, "target-proxy", p)
		return pinResponse(""), nil
	}}}
	r, _ := http.NewRequestWithContext(t.Context(), "POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-6-luna"}`))
	resp, err := wrapper.Do(r, "target-proxy", 300, 1)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, 1, called)
	large := `{"model":"gpt-6-luna","input":"` + strings.Repeat("a", 5<<20) + `"}`
	ctx := service.WithGatewayBorrowWireModel(t.Context(), "gpt-6-luna")
	r, _ = http.NewRequestWithContext(ctx, "POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(large))
	resp, err = wrapper.Do(r, "target-proxy", 300, 1)
	require.NoError(t, err, "a borrow target may still send large native Luna requests")
	_ = resp.Body.Close()
	require.Equal(t, 2, called)
}

func TestGatewayBorrowEmptyAutomaticDonorFailsClosed(t *testing.T) {
	settings := config.AstraRoutingSettings{AutoQuality: true, CookiePool: config.CodexGatewayPinConfig{Enabled: true, Models: []string{"gpt-6-astra", "gpt-6.1-sol"}, TargetAccountIDs: []int64{300}}}
	cfg := &config.Config{}
	cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
	called := 0
	wrapper := &astraRoutingUpstream{cfg: cfg, delegate: gatewayPinDelegate{call: func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		called++
		return pinResponse(""), nil
	}}}
	r, _ := http.NewRequestWithContext(t.Context(), "POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-6.1-sol"}`))
	_, err := wrapper.Do(r, "target-proxy", 300, 1)
	require.Error(t, err)
	require.Zero(t, called, "no donor may not silently send native business traffic")
}

func TestGatewayBorrowUninspectableTargetBodyCannotFailOpen(t *testing.T) {
	for _, kind := range []string{"no_getbody", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.CodexGatewayPin = pinConfig()
			cfg.Gateway.CodexGatewayPin.QualityMode = config.GatewayBorrowAccountQualityMode
			called := 0
			wrapper := &astraRoutingUpstream{cfg: cfg, delegate: gatewayPinDelegate{call: func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
				called++
				return pinResponse(""), nil
			}}}
			body := `{"model":"gpt-6-astra","input":"` + strings.Repeat("a", 4<<20) + `"}`
			r, _ := http.NewRequestWithContext(t.Context(), "POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(body))
			if kind == "no_getbody" {
				r.GetBody = nil
			}
			_, err := wrapper.Do(r, "target-proxy", 300, 1)
			require.ErrorContains(t, err, "gateway_borrow_retired")
			require.Zero(t, called)
		})
	}
}
