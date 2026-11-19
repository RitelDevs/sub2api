package repository

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func accountBorrowFixture(t *testing.T) (*astraRoutingUpstream, *codexGatewayPinUpstream) {
	t.Helper()
	settings := config.AstraRoutingSettings{AutoQuality: true, QualityMode: config.GatewayBorrowAccountQualityMode, Revision: "account-policy", CookiePool: config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{299, 298}, TargetAccountIDs: []int64{300}, Models: []string{"gpt-6-astra", "gpt-6.1-sol"}}}
	cfg := &config.Config{}
	cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
	wrapper := &astraRoutingUpstream{cfg: cfg}
	wrapper.SetAstraGatewayPreparer(func(context.Context, int64) error { return nil })
	settings.CookiePool.QualityMode = settings.QualityMode
	return wrapper, &codexGatewayPinUpstream{config: settings.CookiePool, routingSettings: settings, rotationCache: &wrapper.rotationCache}
}
func accountBorrowRequest(t *testing.T, model, state string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"`+model+`"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer fixture-target")
	req.Header.Set("ChatGPT-Account-ID", "target-identity")
	req.Header.Set("X-Codex-Turn-State", state)
	return req
}
func accountBorrowAdd(t *testing.T, pool *codexGatewayPinUpstream, id int64, value string, remaining time.Duration) {
	t.Helper()
	now := time.Now()
	route := codexGatewayRouteFromResponse(pinResponse("__oailb="+value+"; Path=/; Secure; Max-Age=230"), "/backend-api/codex/responses", now)
	route.expires = now.Add(remaining)
	pool.recordSource(id, now, route, true)
}
func accountBorrowResponse(model, answer string) *http.Response {
	response := pinResponse("")
	response.Header.Set("X-Codex-Turn-State", "resigned-is-not-quality-failure")
	response.Body = io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"model":"` + model + `","status":"completed","output":[{"content":[{"type":"output_text","text":"` + answer + `"}]}]}}` + "\n\n"))
	return response
}

func TestAccountQualityAnswerRemainsPrivate(t *testing.T) {
	req := accountBorrowRequest(t, "gpt-6-astra", "")
	req.AddCookie(&http.Cookie{Name: "__oailb", Value: "own-fixture"})
	up := gatewayPinDelegate{call: func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error) {
		return accountBorrowResponse("gpt-6-astra", "private provider text"), nil
	}}
	res := service.ProbeOpenAICodexBorrowAccountQualityRoute(t.Context(), up, req, "fixed", 300, 1, nil, false)
	require.Equal(t, 4, res.Attempts)
	require.Zero(t, res.Correct)
	require.Empty(t, res.Answer)
	require.NotEqual(t, service.OpenAICodexStateHealthy, res.Verdict)
}
