package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type delayedBorrowFinalSendUpstream struct {
	*borrowSafetyProvider
	afterVerification func()
	business          int
}
type finalBorrowSendRepo struct {
	AccountRepository
	account *Account
}

func (r *finalBorrowSendRepo) GetOpenAITurnAdmission(context.Context, int64) (*Account, *Account, error) {
	return r.account, nil, nil
}
func (p *delayedBorrowFinalSendUpstream) Do(req *http.Request, proxy string, id int64, n int) (*http.Response, error) {
	return p.DoWithTLS(req, proxy, id, n, nil)
}
func (p *delayedBorrowFinalSendUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	// Stand in for the repository's six verification requests. No business
	// bytes have been sent when the account changes here.
	if p.afterVerification != nil {
		p.afterVerification()
	}
	if err := CheckGatewayBorrowFinalSend(req); err != nil {
		return nil, err
	}
	p.business++
	return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"model\":\"gpt-6-astra\",\"status\":\"completed\"}}\n\n"))}, nil
}

func TestGatewayBorrowNeverStartsSlowVerification(t *testing.T) {
	gateway, a, p := borrowSafetyService()
	gateway.accountRepo = &finalBorrowSendRepo{account: a}
	upstream := &delayedBorrowFinalSendUpstream{borrowSafetyProvider: p, afterVerification: func() { t.Fatal("retired verification invoked") }}
	gateway.httpUpstream = upstream
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, chatgptCodexAPIURL, strings.NewReader(`{"model":"gpt-6-astra"}`))
	require.NoError(t, err)
	resp, err := gateway.doOpenAIProxyAttempt(req, a, runtimeProxyEgress{})
	require.ErrorContains(t, err, "gateway_borrow_retired")
	require.Nil(t, resp)
	require.Zero(t, upstream.business)
	require.Zero(t, p.calls)
}

func TestGatewayBorrowFinalCheckSkipsValidationProbe(t *testing.T) {
	calls := 0
	ctx := WithGatewayBorrowFinalSendCheck(context.Background(), func(*http.Request) error { calls++; return denyOpenAITurn("fixture") })
	ctx = context.WithValue(ctx, openAICodexStateProbeContextKey{}, true)
	req, _ := http.NewRequestWithContext(ctx, "POST", "https://chatgpt.com/backend-api/codex/responses", nil)
	require.NoError(t, CheckGatewayBorrowFinalSend(req))
	require.Zero(t, calls)
}
