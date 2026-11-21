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

func TestRetiredAstraTransport(t *testing.T) {
	ctx := t.Context()
	settings := config.AstraRoutingSettings{CookiePool: pinConfig()}
	// Even a previously populated wrapper cannot reach its old provider or pool.
	s := &astraRoutingUpstream{tickets: &unifiedAstraProvider{enabled: true}, pool: &codexGatewayPinUpstream{}}
	s.SetAstraGatewayPreparer(func(context.Context, int64) error { t.Fatal("unexpected preparation"); return nil })
	s.ApplyAstraRoutingSettings(config.AstraRoutingSettings{}, settings)
	r := pinRequest(t).WithContext(ctx)
	profile := &tlsfingerprint.Profile{}
	calls := 0
	s.delegate = gatewayPinDelegate{call: func(got *http.Request, p string, a int64, n int, f *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		require.Same(t, r, got)
		require.Equal(t, "fixed", p)
		require.Equal(t, int64(300), a)
		require.Equal(t, 2, n)
		require.Same(t, profile, f)
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}}
	_, err := s.DoWithTLS(r, "fixed", 300, 2, profile)
	require.NoError(t, err)
	require.Nil(t, s.current(ctx))
	require.False(t, s.unifiedTickets(ctx))
	require.Error(t, s.PrepareAstraGateway(ctx))
	require.Zero(t, s.AstraGatewaySnapshot(ctx).ReadyRoutes)
	cookie, proxy, release, err := s.CodexGatewayPinWSRequest(ctx, r.Header, "fixed", 300, 2)
	require.NoError(t, err)
	release()
	require.Empty(t, cookie)
	require.Equal(t, "fixed", proxy)
	for _, denied := range []context.Context{service.WithGatewayBorrowRequiredModel(ctx, "gpt-6-astra"), service.WithAstraSourceAcquisitionForAccount(ctx, 299)} {
		_, err := s.Do(r.WithContext(denied), "fixed", 299, 2)
		require.True(t, service.IsOpenAITurnAdmissionError(err))
	}
	settings.AutoQuality = true
	s.ApplyAstraRoutingSettings(config.AstraRoutingSettings{}, settings)
	_, err = s.Do(r, "fixed", 300, 2)
	require.True(t, service.IsOpenAITurnAdmissionError(err))
	_, _, release, err = s.CodexGatewayPinWSRequest(ctx, r.Header, "fixed", 300, 2)
	release()
	require.Error(t, err)
	require.Equal(t, 1, calls)
}
