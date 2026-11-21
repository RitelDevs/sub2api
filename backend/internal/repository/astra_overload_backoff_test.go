package repository

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func overloadProbeResponse(code string) *http.Response {
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`data: {"type":"response.failed","response":{"status":"failed","error":{"code":%q,"type":"server_error","message":"private upstream detail"}}}`+"\n\n", code)))}
}

func TestBorrowOverloadDoesNotCreateLongRotationBan(t *testing.T) {
	pool := &codexGatewayPinUpstream{config: config.CodexGatewayPinConfig{RotateNodes: true}, rotationCache: &astraRotationCache{}}
	check := astraTargetValidation{node: mihomo.AstraNode{Identity: "exit-one"}, gateway: "gateway-one", model: "gpt-6-astra", reason: "target_probe_overloaded"}
	pool.rememberTargetFailure(300, check)
	require.Empty(t, pool.rotationCache.snapshot([]int64{300}, time.Now()), "explicit overload uses only the target's 60-second quiet period")
	check.reason = "target_quality_failed"
	pool.rememberTargetFailure(300, check)
	rows := pool.rotationCache.snapshot([]int64{300}, time.Now())
	require.NotEmpty(t, rows, "a real quality failure keeps the existing rotation ban")
	require.Greater(t, rows[0].RemainingSeconds, int64(3500))
}

func TestBorrowOverloadClassificationWithoutPool(t *testing.T) {
	for _, tc := range []struct {
		status        int
		code, failure string
	}{{200, "server_is_overloaded", service.OpenAICodexStateFailureOverloaded}, {200, "server_error", service.OpenAICodexStateFailureStreamError}, {200, "server_is_overloaded_other", service.OpenAICodexStateFailureStreamError}, {503, "server_is_overloaded", service.OpenAICodexStateFailureOverloaded}, {503, "server_error", service.OpenAICodexStateFailureUpstreamError}, {401, "server_is_overloaded", service.OpenAICodexStateFailureAccountError}} {
		t.Run(fmt.Sprintf("%d-%s", tc.status, tc.code), func(t *testing.T) {
			req := accountBorrowRequest(t, "gpt-6-astra", "")
			req.AddCookie(&http.Cookie{Name: "__oailb", Value: "own-fixture"})
			up := gatewayPinDelegate{call: func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
				if tc.status == 200 {
					return overloadProbeResponse(tc.code), nil
				}
				return &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"error":{"code":%q}}`, tc.code)))}, nil
			}}
			r := service.ProbeOpenAICodexBorrowAccountQualityRoute(t.Context(), up, req, "fixed", 300, 1, nil, false)
			require.Equal(t, tc.failure, r.Failure)
			require.Equal(t, 1, r.Attempts)
			require.Zero(t, r.Correct)
		})
	}
}
