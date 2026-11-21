package repository

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAstraReadinessSnapshotDoesNotWaitForNetwork(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	wrapper := &astraRoutingUpstream{cfg: cfg}
	pool := &codexGatewayPinUpstream{}
	wrapper.pool = pool
	require.NoError(t, pool.targetProbeMu.Lock(t.Context()))
	defer pool.targetProbeMu.Unlock()
	done := make(chan struct{})
	go func() { wrapper.AstraGatewaySnapshot(context.Background()); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("status blocked on target network probe")
	}
}

func TestAstraStateProbeBypassesBorrowingRecursion(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	calls := 0
	wrapper := &astraRoutingUpstream{cfg: cfg, delegate: gatewayPinDelegate{call: func(r *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		require.True(t, service.IsOpenAICodexStateProbeRequest(r.Context()))
		return pinResponse(""), nil
	}}}
	req := pinRequest(t)
	req.Header.Set("Cookie", "__oailb=explicit-route")
	result := service.ProbeOpenAICodexStateRoute(t.Context(), wrapper, req, "exit", 300, 1, nil)
	require.Equal(t, service.OpenAICodexStateHealthy, result.Verdict)
	require.Equal(t, 2, calls)
	require.Nil(t, wrapper.pool, "standalone probes must not acquire or publish borrowing routes")
}
