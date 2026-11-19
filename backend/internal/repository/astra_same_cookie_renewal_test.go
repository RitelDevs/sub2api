package repository

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func renewalCookie(exp time.Time) string {
	claims := fmt.Sprintf(`{"host":"chat.gateway.unified-42.api.openai.com","exp":%d}`, exp.Unix())
	return "header." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".signature"
}

func TestCodexGatewayCookieLeaseRespectsAbsoluteExpiry(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	exp := now.Add(70 * time.Second)
	cookie := renewalCookie(exp)
	response := pinResponse("__oailb=" + cookie + "; Path=/; Secure; Max-Age=3600")
	route := codexGatewayRouteFromResponse(response, "/backend-api/codex/responses", now, 230)
	require.NotNil(t, route)
	require.Equal(t, exp, route.expires, "replayed Max-Age cannot exceed the Cookie's unchanged JWT exp")
	response = pinResponse("__oailb=" + renewalCookie(now.Add(-time.Second)) + "; Path=/; Secure; Max-Age=3600")
	stale := codexGatewayRouteFromResponse(response, "/backend-api/codex/responses", now, 230)
	require.True(t, stale == nil || stale.cookie.Value == "" || !now.Before(stale.expires))
	// Attribute expiry remains a bound even when Max-Age is also supplied.
	response = pinResponse("__oailb=" + renewalCookie(now.Add(time.Hour)) + "; Path=/; Secure; Max-Age=3600; Expires=" + exp.UTC().Format(http.TimeFormat))
	route = codexGatewayRouteFromResponse(response, "/backend-api/codex/responses", now, 230)
	require.NotNil(t, route)
	require.True(t, exp.Equal(route.expires))
	_, _ = io.Copy(io.Discard, response.Body)
}
