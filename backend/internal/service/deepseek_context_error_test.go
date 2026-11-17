//go:build unit

package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"
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

func TestDeepSeekHTTPDiagnosticsSafeFields(t *testing.T) {
	for _, tc := range []struct {
		name, body, code, typ, param, reason string
		hints                                []string
	}{
		{"precharge", `{"error":{"code":"INSUFFICIENT_BALANCE","type":"permission_error","param":"max_tokens","message":"预扣余额不足：max_tokens=393216 private-user-text"}}`, "insufficient_balance", "permission_error", "max_tokens", "balance_error_code", []string{"precharge", "output_budget"}},
		{"message balance", `{"message":"预扣余额不足 private-user-text"}`, "", "", "", "balance_error_message", []string{"precharge"}},
		{"context thinking", `{"error":{"type":"invalid_request_error","param":"thinking.budget_tokens","message":"context window thinking budget_tokens private-user-text"}}`, "", "invalid_request_error", "thinking.budget_tokens", "", []string{"context", "thinking"}},
		{"tool image", `{"error":{"code":"invalid_value","param":"messages[2].content[0].type","message":"tool_use_id tool_result image private-user-text"}}`, "invalid_value", "", "messages[].content[].type", "", []string{"tool_pairing", "image"}},
		{"unknown fields", `{"error":{"code":"private-user-text","type":"private-user-text","param":"messages.0.private-user-text","message":"private-user-text"},"request":{"message":"预扣余额不足 image"}}`, "", "", "", "", nil},
		{"nested response", `{"response":{"error":{"code":"invalid_image","param":"messages.2.content.0.source.type","message":"image private-user-text"}}}`, "invalid_image", "", "messages[].content[].source.type", "", []string{"image"}},
		{"plain text", `预扣余额不足 private-user-text`, "", "", "", "balance_error_message", []string{"precharge"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &DeepSeekRecoveryAttempt{}
			a.observeHTTPError(PlatformDeepseek, []byte(tc.body), 403, nil)
			out := a.Outcome()
			require.Equal(t, tc.code, out.HTTPErrorCode)
			require.Equal(t, tc.typ, out.HTTPErrorType)
			require.Equal(t, tc.param, out.HTTPErrorParam)
			require.Equal(t, tc.reason, out.HTTPErrorReason)
			require.Equal(t, tc.hints, out.HTTPErrorMessageHints)
			raw, err := json.Marshal(out)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "private-user-text")
			require.NotContains(t, string(raw), "393216")
			require.NotContains(t, string(raw), "预扣余额不足")
			if len(out.HTTPErrorMessageHints) > 0 {
				out.HTTPErrorMessageHints[0] = "changed"
				require.Equal(t, tc.hints, a.Outcome().HTTPErrorMessageHints)
			}
		})
	}
}

func TestDeepSeekHTTPDiagnosticsMessageFingerprint(t *testing.T) {
	message := "  余额不足 private-user-text  "
	payload, err := json.Marshal(map[string]any{"message": message})
	require.NoError(t, err)
	a := &DeepSeekRecoveryAttempt{}
	a.observeHTTPError(PlatformDeepseek, payload, 403, nil)
	out := a.Outcome()
	sum := sha256.Sum256([]byte(message))
	require.Equal(t, hex.EncodeToString(sum[:]), out.HTTPErrorMessageHash)
	require.Equal(t, utf8.RuneCountInString(message), out.HTTPErrorMessageChars)
	a.observeHTTPError(PlatformDeepseek, []byte(`{"error":{"code":"insufficient_balance"}}`), 403, nil)
	require.Empty(t, a.Outcome().HTTPErrorMessageHash)
	require.Zero(t, a.Outcome().HTTPErrorMessageChars)
}

func TestDeepSeekHTTPDiagnosticsPreservePlatform(t *testing.T) {
	a := &DeepSeekRecoveryAttempt{}
	headers := http.Header{"X-Codex-Primary-Window-Minutes": {"10080"}, "X-Codex-Primary-Used-Percent": {"100"}}
	a.observeHTTPError(PlatformOpenAI, []byte(`{}`), 429, headers)
	require.Equal(t, "weekly_quota", a.Outcome().HTTPErrorClass)
	require.Equal(t, "codex_weekly_header", a.Outcome().HTTPErrorReason)
	a.observeHTTPError(PlatformDeepseek, []byte(`{}`), 429, headers)
	require.Empty(t, a.Outcome().HTTPErrorClass)
}

func TestDeepSeekHTTPDiagnosticsPreservePolicyAndPublicErrors(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		status                      int
		body, class, reason, public string
		paused                      bool
	}{
		{"balance 403", 403, `{"error":{"code":"insufficient_balance","message":"预扣余额不足 private-user-text"}}`, "balance_low", "balance_error_code", "upstream_unavailable", true},
		{"balance message 400", 400, `{"error":{"message":"余额不足 private-user-text"}}`, "balance_low", "balance_error_message", "upstream_unavailable", false},
		{"context message 400", 400, `{"error":{"type":"invalid_request_error","message":"context window max_tokens private-user-text"}}`, "", "", "upstream_unavailable", false},
		{"context code 400", 400, `{"error":{"code":"context_length_exceeded","message":"private-user-text"}}`, "", "", "context_limit", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &scheduling429Repo{}
			limits := NewRateLimitService(repo, nil, &config.Config{}, nil, nil)
			svc := &OpenAIGatewayService{rateLimitService: limits, httpUpstream: &deepSeekRecoveryUpstreamStub{do: func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Header: http.Header{"X-Request-Id": {"fixture-id"}}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}}}
			ctx, a := NewDeepSeekRecoveryAttempt(t.Context())
			defer a.Close()
			account := &Account{ID: 252, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true}
			_, err := svc.doOpenAIUpstream(deepSeekRecoveryRequest(t, ctx, "/v1/messages"), "", account)
			require.Error(t, err)
			require.Equal(t, "deepseek_http_error", DeepSeekRecoveryFailureReason(err))
			out := a.Outcome()
			require.Equal(t, tc.status, out.HTTPStatus)
			require.Equal(t, "fixture-id", out.UpstreamRequestID)
			require.Equal(t, tc.class, out.HTTPErrorClass)
			require.Equal(t, tc.reason, out.HTTPErrorReason)
			require.Equal(t, tc.public, out.PublicErrorCode)
			require.Equal(t, !tc.paused, account.Schedulable)
			if tc.paused {
				require.Equal(t, []bool{false}, repo.values)
			} else {
				require.Empty(t, repo.values)
			}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			RecordDeepSeekRecoveryFailure(c, account, nil, 1, err, out)
			events, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			detail := events.([]*OpsUpstreamErrorEvent)[0].Detail
			require.Contains(t, detail, "http_error_message_sha256")
			require.NotContains(t, detail, "private-user-text")
		})
	}
}
