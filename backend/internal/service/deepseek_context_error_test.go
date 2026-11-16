//go:build unit

package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDeepSeekPublicContextError(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
		want   string
	}{
		{`{"error":{"code":"context_length_exceeded","message":"private"}}`, 400, "context_limit"},
		{`{"error":{"type":"prompt_too_long"}}`, 200, "context_limit"},
		{`{"error":{"message":"context private text"}}`, 400, "upstream_unavailable"},
		{`{}`, 413, "upstream_unavailable"},
		{`{}`, 429, "rate_limited"},
	} {
		require.Equal(t, tc.want, deepSeekPublicErrorCode([]byte(tc.body), tc.status))
	}
	ctx, a := NewDeepSeekRecoveryAttempt(context.Background())
	defer a.Close()
	upstream := &deepSeekRecoveryUpstreamStub{do: func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 400, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"context_length_exceeded","message":"private body"}}`))}, nil
	}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	_, err := svc.doOpenAIUpstream(deepSeekRecoveryRequest(t, ctx, "/v1/messages"), "", &Account{ID: 7, Platform: "deepseek"})
	require.Error(t, err)
	require.Equal(t, "context_limit", a.Outcome().PublicErrorCode)
	resp, b := deepSeekRecoveryFixture(t, "/v1/messages", "text/event-stream", "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"context_length_exceeded\"}}\n\n")
	_, err = io.ReadAll(resp.Body)
	require.Error(t, err)
	require.Equal(t, "context_limit", b.Outcome().PublicErrorCode)
}
