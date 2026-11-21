package service

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRetiredBorrowRejectsBeforeAccessingAnyAccountOrSending(t *testing.T) {
	// With no store, credential provider or transport installed, any attempt to use one is a bug.
	gateway := new(OpenAIGatewayService)
	ctx := t.Context()
	hdr := http.Header{"User-Agent": {"untouched"}}
	before := hdr.Clone()
	require.False(t, gateway.UnifiedAstraTicketsEnabled(ctx))
	require.ErrorContains(t, gateway.PrepareStoredAstraSources(ctx), "gateway_borrow_retired")
	require.ErrorContains(t, gateway.VerifyStoredAstraTarget(ctx, 17), "gateway_borrow_retired")
	for _, protocol := range []string{"sse", "websocket"} {
		route, release, err := gateway.ApplyStoredAstraTarget(ctx, hdr, 17, protocol)
		require.ErrorContains(t, err, "gateway_borrow_retired")
		require.Empty(t, route)
		release()
		require.Equal(t, before, hdr)
	}
	service := new(AccountTestService)
	_, err := service.TestAstraGateway(ctx, "prepare", 17, 1)
	require.ErrorContains(t, err, "gateway_borrow_retired")
	require.NoError(t, service.StartPersistedAutomaticGatewayBorrow())
}

func TestRetiredBorrowRejectsStoredDonorProvenance(t *testing.T) {
	_, _, seed := unifiedAstraFixture(t)
	now := time.Now()
	require.True(t, seed.valid(now, 780))
	foreign := *seed
	foreign.SourceAccountID = seed.AccountID + 1
	require.False(t, foreign.valid(now, 780))
	historical := *seed
	historical.Acquisition = "reuse"
	require.False(t, historical.valid(now, 780))
}
