//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type borrowSourceTestRepo struct {
	AccountRepository
	account       *Account
	reads, writes int
	onRead        func(int)
}

func (r *borrowSourceTestRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	if r.onRead != nil {
		r.onRead(r.reads)
	}
	return r.account, nil
}
func (r *borrowSourceTestRepo) SetRateLimited(_ context.Context, _ int64, until time.Time) error {
	r.writes++
	copy := *r.account
	copy.RateLimitResetAt = &until
	r.account = &copy
	return nil
}

type borrowSourceTestUpstream struct {
	HTTPUpstream
	calls   int
	status  int
	body    string
	headers http.Header
	request *http.Request
}

func (p *borrowSourceTestUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	p.calls++
	p.request = req
	return &http.Response{StatusCode: p.status, Header: p.headers, Body: io.NopCloser(strings.NewReader(p.body))}, nil
}
func newBorrowSourceTest(t *testing.T) (*AccountTestService, *borrowSourceTestRepo, *borrowSourceTestUpstream) {
	t.Helper()
	gateway, account, _ := borrowSafetyService()
	account.Extra = map[string]any{}
	repo := &borrowSourceTestRepo{account: account}
	upstream := &borrowSourceTestUpstream{status: 200, headers: make(http.Header), body: "data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-astra\",\"status\":\"completed\",\"output\":[{\"content\":[{\"type\":\"output_text\",\"text\":\"OK\"}]}]}}\n\n"}
	settings := config.AstraRoutingSettings{AutoQuality: true, CookiePool: config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{account.ID}}}
	gateway.cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
	gateway.accountRepo = repo
	return &AccountTestService{accountRepo: repo, httpUpstream: upstream, openaiGatewayService: gateway, cfg: gateway.cfg}, repo, upstream
}

func TestAutomaticBorrowSourceKnownRestrictionsNeverSend(t *testing.T) {
	for _, kind := range []string{"native quota", "paused", "overload", "model quota"} {
		t.Run(kind, func(t *testing.T) {
			s, repo, upstream := newBorrowSourceTest(t)
			future := time.Now().Add(time.Hour)
			switch kind {
			case "native quota":
				repo.account.RateLimitResetAt = &future
			case "paused":
				repo.account.Schedulable = false
			case "overload":
				repo.account.OverloadUntil = &future
			case "model quota":
				repo.account.Extra[modelRateLimitsKey] = map[string]any{"gpt-6-astra": map[string]any{"rate_limit_reset_at": future.Format(time.RFC3339)}}
			}
			require.Error(t, s.prepareAstraGatewaySource(context.Background(), repo.account.ID))
			require.Zero(t, upstream.calls)
			require.Zero(t, repo.writes)
		})
	}
}

func TestAutomaticBorrowSourceDeniedBeforeReadOrSend(t *testing.T) {
	s, r, u := newBorrowSourceTest(t)
	ctx := WithGatewayBorrowRequiredModel(t.Context(), "gpt-6.1-sol")
	for _, prepare := range []func(context.Context, int64) error{s.prepareAstraGatewaySource, s.prepareAutomaticGatewayBorrowSource} {
		require.ErrorContains(t, prepare(ctx, r.account.ID), "gateway_borrow_retired")
	}
	require.Zero(t, r.reads)
	require.Zero(t, r.writes)
	require.Zero(t, u.calls)
}

func TestAutomaticBorrowSourceRuntimeHidesBlockedCandidate(t *testing.T) {
	s, repo, _ := newBorrowSourceTest(t)
	long := time.Now().Add(time.Hour)
	repo.account.RateLimitResetAt = &long
	expiry := time.Now().Add(time.Minute)
	upstream := &borrowSafetyProvider{snapshot: AstraGatewayRuntime{Sources: []AstraRouteStatus{{AccountID: repo.account.ID, State: "candidate", ExpiresAt: &expiry, RemainingSeconds: 60, Active: true}}}}
	s.httpUpstream = upstream
	status := s.AstraGatewayStatus(context.Background())
	require.Len(t, status.Sources, 1)
	require.Equal(t, "blocked", status.Sources[0].State)
	require.Equal(t, "source_account_rate_limited", status.Sources[0].Reason)
	require.Nil(t, status.Sources[0].ExpiresAt)
	require.False(t, status.Sources[0].Active)
	require.Zero(t, status.Sources[0].RemainingSeconds)
	require.NotNil(t, status.Sources[0].RetryAt)
}
