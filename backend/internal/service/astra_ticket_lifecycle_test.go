package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type astraTicketRepo struct {
	AccountRepository
	mu       sync.Mutex
	accounts map[int64]*Account
	writes   int
	err      error
}

func (r *astraTicketRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a := r.accounts[id]; a != nil {
		copy := *a
		return &copy, nil
	}
	return nil, errors.New("missing fixture")
}
func (r *astraTicketRepo) UpdateExtra(_ context.Context, id int64, extra map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.writes++
	a := *r.accounts[id]
	a.Extra = MergeOpenAICodexTicketExtra(extra, a.Extra)
	// UpdateExtra merges keys; preserve a detached account snapshot in this mock.
	for key, value := range extra {
		a.Extra[key] = value
	}
	r.accounts[id] = &a
	return nil
}

func unifiedAstraFixture(t *testing.T) (*OpenAIGatewayService, *astraTicketRepo, *openAICodexTicket) {
	t.Helper()
	a, b := ticketTestAccount(1), ticketTestAccount(2)
	a.Credentials = map[string]any{"access_token": "source-token", "chatgpt_account_id": "source-workspace"}
	b.Credentials = map[string]any{"access_token": "target-token", "chatgpt_account_id": "target-workspace"}
	repo := &astraTicketRepo{accounts: map[int64]*Account{1: a, 2: b}}
	s := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, TargetLength: 780, TTLSeconds: 240, Models: []string{"gpt-6-astra"}, HarvestProxyURL: "http://self.invalid:8080", HarvestAttemptTimeoutSeconds: 10}, nil)
	s.accountRepo = repo
	s.cfg.Gateway.CodexGatewayPin = config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{1}, TargetAccountIDs: []int64{2}, IPAffinity: true, TTLSeconds: 240}
	now := time.Now().Add(-5 * time.Second)
	seed := &openAICodexTicket{AccountID: 1, Model: "gpt-6-astra", State: mint780State(now), Length: 780, Blocks: 33, IssuedAt: now, CapturedAt: now, ExpiresAt: now.Add(240 * time.Second), ProbeVerified: true, Transport: "sse", Gateway: "unified-42", HarvestCookies: mint780Pair(time.Now().Add(time.Hour), "unified-42"), HarvestProxyURL: "http://source.invalid:8080"}
	require.NoError(t, s.storeOpenAICodexTicket(t.Context(), a, seed))
	repo.writes = 0
	return s, repo, seed
}

func targetTicketHeaders() http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer target-token")
	h.Set("ChatGPT-Account-ID", "target-workspace")
	h.Set("Cookie", "__oailb=unrelated; __cflb=unrelated")
	h.Set(openAICodexTurnStateHeader, "unrelated")
	return h
}

func TestUnifiedAstraRetiredWithStoredSource(t *testing.T) {
	s, repo, seed := unifiedAstraFixture(t)
	target := repo.accounts[2]
	ctx := t.Context()
	calls := 0
	s.httpUpstream = &harvestProxyUpstream{do: func(*http.Request, string) (*http.Response, error) {
		calls++
		return nil, errors.New("unexpected outbound")
	}}
	require.False(t, s.UnifiedAstraTicketsEnabled(ctx))
	require.ErrorContains(t, s.PrepareStoredAstraSources(ctx), "gateway_borrow_retired")
	require.ErrorContains(t, s.VerifyStoredAstraTarget(ctx, target.ID), "gateway_borrow_retired")
	reserved := 0
	attempted, result := s.tryBorrowStoredAstraTicket(ctx, target, "gpt-6-astra", time.Second, func() bool { reserved++; return true })
	require.False(t, attempted)
	require.True(t, result.Terminal)
	require.Zero(t, reserved)
	for _, protocol := range []string{"sse", "websocket"} {
		h := targetTicketHeaders()
		before := h.Clone()
		p, release, err := s.ApplyStoredAstraTarget(ctx, h, target.ID, protocol)
		require.ErrorContains(t, err, "gateway_borrow_retired")
		release()
		require.Empty(t, p)
		require.Equal(t, before, h)
	}
	require.Nil(t, s.lookupOpenAICodexTicket(target, "gpt-6-astra"))
	require.Equal(t, seed.State, s.lookupOpenAICodexTicket(repo.accounts[1], "gpt-6-astra").State)
	require.Zero(t, calls)
	require.Zero(t, repo.writes)
}

func TestUnifiedTransportLifecycleKeepsRequiredAccountQualityIndependent(t *testing.T) {
	for _, mode := range []string{"ordinary", "auto_quality", "account_quality", "account_scheduling", "required_account", "service_scope"} {
		t.Run(mode, func(t *testing.T) {
			s, repo, _ := unifiedAstraFixture(t)
			settings := s.cfg.AstraRouting(t.Context())
			switch mode {
			case "auto_quality":
				settings.AutoQuality = true
			case "account_quality":
				settings.QualityMode = config.GatewayBorrowAccountQualityMode
			case "account_scheduling":
				settings.AccountScheduling = true
			case "required_account":
				repo.accounts[2].Extra = map[string]any{GatewayBorrowModelsKey: []string{"gpt-6-astra", "gpt-6.1-sol"}}
			}
			s.cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
			ctx := context.WithValue(t.Context(), boundAstraTicketContextKey{}, true)
			if mode == "service_scope" {
				ctx = WithGatewayBorrowRequiredModel(ctx, "gpt-6-astra")
			}
			require.False(t, s.UnifiedAstraTicketsEnabled(ctx))
			calls := 0
			s.httpUpstream = &harvestProxyUpstream{do: func(*http.Request, string) (*http.Response, error) {
				calls++
				return nil, errors.New("unexpected transport")
			}}
			h := targetTicketHeaders()
			before := h.Clone()
			_, release, err := s.ApplyStoredAstraTarget(ctx, h, 2, "sse")
			if release != nil {
				release()
			}
			require.Error(t, err, "bound transport ticket never proves account IQ")
			require.Equal(t, before, h)
			require.Zero(t, calls)
			require.Zero(t, repo.writes)
		})
	}
}
