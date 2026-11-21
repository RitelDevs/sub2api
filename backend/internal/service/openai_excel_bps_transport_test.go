package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type bpsTestLease struct{ releases, failures int }

func (l *bpsTestLease) Release()               { l.releases++ }
func (l *bpsTestLease) ReportFailure()         { l.failures++ }
func (l *bpsTestLease) ReportStreamFailure()   { l.failures++ }
func (l *bpsTestLease) ReportSuccess()         {}
func (l *bpsTestLease) ReportUpstreamFailure() { l.failures++ }

type bpsTestUpstream struct {
	httpUpstreamRecorder
	send func(*http.Request, string) (*http.Response, error)
}

func (u *bpsTestUpstream) Do(r *http.Request, proxy string, _ int64, _ int) (*http.Response, error) {
	return u.send(r, proxy)
}

func TestExcelBPSProxyNeverRetriesTransportErrors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		evidence func(*http.Request)
	}{
		{"dial_failure", func(r *http.Request) { httptrace.ContextClientTrace(r.Context()).GetConn("bps.openai.com:443") }},
		{"missing_trace", func(*http.Request) {}},
		{"connection_obtained", func(r *http.Request) {
			tr := httptrace.ContextClientTrace(r.Context())
			tr.GetConn("bps.openai.com:443")
			tr.GotConn(httptrace.GotConnInfo{})
		}},
		{"header_started", func(r *http.Request) {
			tr := httptrace.ContextClientTrace(r.Context())
			tr.GetConn("bps.openai.com:443")
			tr.WroteHeaderField("Authorization", []string{"secret"})
		}},
		{"body_read_without_trace", func(r *http.Request) {
			tr := httptrace.ContextClientTrace(r.Context())
			tr.GetConn("bps.openai.com:443")
			_, _ = r.Body.Read(make([]byte, 1))
		}},
		{"replay_body_read", func(r *http.Request) {
			tr := httptrace.ContextClientTrace(r.Context())
			tr.GetConn("bps.openai.com:443")
			b, _ := r.GetBody()
			defer func() { _ = b.Close() }()
			_, _ = b.Read(make([]byte, 1))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			account := excelAccount()
			account.Extra["openai_excel_bps_mihomo"] = true
			first := &bpsTestLease{}
			acquired, calls := 0, 0
			acquire := func(_ context.Context, _ string, excluded ...string) (string, excelBPSLease, error) {
				acquired++
				require.Empty(t, excluded)
				return "http://127.0.0.1:19000", first, nil
			}
			upstream := &bpsTestUpstream{send: func(r *http.Request, proxy string) (*http.Response, error) {
				calls++
				defer func() { _ = r.Body.Close() }()
				require.Equal(t, "http://127.0.0.1:19000", proxy)
				tc.evidence(r)
				return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNRESET}
			}}
			s := &OpenAIGatewayService{httpUpstream: upstream}
			resp, lease, _, err := s.doExcelBPSRequest(context.Background(), c, account, "private-session", []byte("original body"), "bearer-secret", "account-secret", acquire)
			require.Equal(t, 1, first.releases)
			require.Equal(t, 1, first.failures)
			require.Error(t, err)
			require.Nil(t, resp)
			require.Nil(t, lease)
			require.Equal(t, 1, calls)
			require.Equal(t, 1, acquired)
		})
	}
}

func TestExcelBPSFailoverBudgetAndCancellation(t *testing.T) {
	for _, scenario := range []string{"cancelled", "deadline", "http_403", "challenge", "response_and_error", "pool_exhausted", "static_proxy"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			account := excelAccount()
			account.Extra["openai_excel_bps_mihomo"] = scenario != "static_proxy"
			leases := []*bpsTestLease{}
			acquired, calls := 0, 0
			acquire := func(context.Context, string, ...string) (string, excelBPSLease, error) {
				acquired++
				if scenario == "pool_exhausted" {
					return "", nil, errors.New("no eligible nodes")
				}
				l := &bpsTestLease{}
				leases = append(leases, l)
				return fmt.Sprintf("http://127.0.0.1:%d", 19000+acquired), l, nil
			}
			upstream := &bpsTestUpstream{send: func(r *http.Request, _ string) (*http.Response, error) {
				calls++
				defer func() { _ = r.Body.Close() }()
				httptrace.ContextClientTrace(r.Context()).GetConn("bps.openai.com:443")
				if scenario == "http_403" {
					return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("denied"))}, nil
				}
				if scenario == "challenge" {
					return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"challenge_required"}}`))}, nil
				}
				if scenario == "response_and_error" {
					return &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader("failed"))}, io.EOF
				}
				if scenario == "cancelled" {
					cancel()
					return nil, context.Canceled
				}
				if scenario == "deadline" {
					return nil, context.DeadlineExceeded
				}
				return nil, io.EOF
			}}
			s := &OpenAIGatewayService{httpUpstream: upstream}
			resp, lease, _, err := s.doExcelBPSRequest(ctx, c, account, "session", []byte("body"), "token", "account", acquire)
			if scenario == "http_403" {
				require.NoError(t, err)
				require.Equal(t, 403, resp.StatusCode)
				require.NoError(t, resp.Body.Close())
				lease.Release()
			} else {
				require.Error(t, err)
				require.Nil(t, lease)
			}
			expected := 1
			if scenario == "pool_exhausted" {
				expected = 0
			}
			require.Equal(t, expected, calls)
			for _, l := range leases {
				require.Equal(t, 1, l.releases)
				if scenario == "cancelled" || scenario == "deadline" || scenario == "http_403" || scenario == "challenge" {
					require.Zero(t, l.failures)
				} else {
					require.Equal(t, 1, l.failures)
				}
			}
			if scenario == "pool_exhausted" {
				require.ErrorIs(t, err, errExcelBPSProxyUnavailable)
			}
			if scenario == "static_proxy" {
				require.Zero(t, acquired)
			}
		})
	}
}

func TestOpenAITransportRejection(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, mitigated, body string
		status                             int
		blocked                            bool
	}{
		{"marked_success", "text/event-stream", "challenge", "", 200, true},
		{"html_success", "text/html; charset=utf-8", "", "", 200, true},
		{"html_route_rejection", "", "", "<!DOCTYPE html><title>Just a moment</title>", 404, true},
		{"verification", "application/json", "", `{"error":{"code":"security_check_required"}}`, 429, true},
		{"edge_refusal", "", "", `{"cloudflare_error":true,"code":1010}`, 403, true},
		{"ambiguous_large_error", "", "", strings.Repeat("x", (32<<10)+1), 503, true},
		{"permission", "application/json", "", `{"error":{"code":"permission_denied"}}`, 403, false},
		{"route_rejection", "application/json", "", `{"detail":"Not Found"}`, 404, false},
		{"stream", "text/event-stream", "", "data: response", 200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := io.NopCloser(strings.NewReader(tc.body))
			resp := &http.Response{StatusCode: tc.status, Header: make(http.Header), Body: original}
			resp.Header.Set("Content-Type", tc.contentType)
			resp.Header.Set("Cf-Mitigated", tc.mitigated)
			err := rejectOpenAITransportChallenge(resp)
			if tc.blocked {
				if err == nil {
					t.Fatal("expected a terminal rejection")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.status == 200 && resp.Body != original {
				t.Fatal("success stream replaced")
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != tc.body {
				t.Errorf("normal response changed: %q, %v", body, err)
			}
			_ = resp.Body.Close()
		})
	}
}

func TestExcelBPSTransportDiagnosticsAreCredentialFree(t *testing.T) {
	core, logs := observer.New(zap.WarnLevel)
	ctx := logger.IntoContext(context.Background(), zap.New(core).With(zap.String("request_id", "req-43885")))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := excelAccount()
	account.Extra["openai_excel_bps_mihomo"] = true
	err := &url.Error{Op: "Post", URL: "https://secret-user:proxy-password@bps.openai.com/?token=secret-token", Err: fmt.Errorf("credential=raw-secret: %w", syscall.ECONNRESET)}
	recordExcelBPSTransportFailure(ctx, c, account, "private-session-key", "http://127.0.0.1:19007", err, "transport", 1, false)
	events, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	attempts, ok := events.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, attempts, 1)
	require.Equal(t, "connection_reset", attempts[0].Reason)
	require.Contains(t, attempts[0].Detail, "19007")
	require.Equal(t, 0, attempts[0].UpstreamStatusCode)
	require.Equal(t, "req-43885", logs.All()[0].ContextMap()["request_id"])
	all := fmt.Sprint(attempts[0], logs.All()[0].ContextMap())
	for _, secret := range []string{"secret-user", "proxy-password", "secret-token", "raw-secret", "private-session-key"} {
		require.NotContains(t, all, secret)
	}
	require.Zero(t, excelBPSLocalProxyPort("http://user:password@127.0.0.1:19000"))
}

func TestExcelBPSTransportErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "deadline_exceeded"},
		{&net.DNSError{Err: "private host"}, "dns_error"}, {syscall.ECONNREFUSED, "connection_refused"},
		{io.ErrUnexpectedEOF, "unexpected_eof"}, {errors.New("net/http: TLS handshake timeout"), "tls_handshake_timeout"},
		{errors.New("http2: connection lost"), "http2_error"}, {nil, "stream_incomplete"},
	} {
		require.Equal(t, tc.want, transportdiag.Classify(tc.err))
	}
}

// Exercise actual net/http tracing without listening sockets or external traffic.
func TestExcelBPSFailoverRealHTTPTransport(t *testing.T) {
	for _, sent := range []bool{false, true} {
		t.Run(fmt.Sprintf("sent_%t", sent), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			account := excelAccount()
			account.Extra["openai_excel_bps_mihomo"] = true
			acquired, calls := 0, 0
			acquire := func(context.Context, string, ...string) (string, excelBPSLease, error) {
				acquired++
				return fmt.Sprintf("http://127.0.0.1:%d", 19000+acquired), &bpsTestLease{}, nil
			}
			observed := make(chan string, 1)
			transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
				if !sent {
					return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
				}
				local, remote := net.Pipe()
				go func() {
					defer func() { _ = remote.Close() }()
					req, e := http.ReadRequest(bufio.NewReader(remote))
					if e != nil {
						observed <- "read error"
						return
					}
					b, _ := io.ReadAll(req.Body)
					_ = req.Body.Close()
					observed <- string(b)
					// Drop after accepting the POST, before returning response headers.
				}()
				return local, nil
			}}
			defer transport.CloseIdleConnections()
			client := &http.Client{Transport: transport}
			upstream := &bpsTestUpstream{send: func(r *http.Request, _ string) (*http.Response, error) {
				calls++
				r.URL.Scheme = "http" // in-memory peer; no DNS, socket or TLS bypass in production
				return client.Do(r)
			}}
			s := &OpenAIGatewayService{httpUpstream: upstream}
			resp, lease, _, err := s.doExcelBPSRequest(context.Background(), c, account, "session", []byte("must not duplicate"), "token", "account", acquire)
			require.Error(t, err)
			require.Nil(t, resp)
			require.Nil(t, lease)
			require.Equal(t, 1, calls)
			require.Equal(t, 1, acquired)
			if sent {
				require.Equal(t, "must not duplicate", <-observed)
			}
		})
	}
}

func TestExcelBPSTransportAndStreamFailureAreServerErrors(t *testing.T) {
	for _, streamFailure := range []bool{false, true} {
		t.Run(fmt.Sprint(streamFailure), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{err: io.EOF}
			if streamFailure {
				upstream.err = nil
				upstream.resp = &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\",\"status\":\"in_progress\"}}\n\n"))}
			}
			svc := openAIClientToolsTestService(nil)
			svc.httpUpstream = upstream
			body := []byte("{\"model\":\"gpt-6-astra\",\"stream\":true,\"input\":\"test\"}")
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
			_, err := svc.Forward(context.Background(), c, excelAccount(), body)
			require.Error(t, err)
			require.Contains(t, rec.Body.String(), "server_error")
			if streamFailure {
				require.Equal(t, 200, rec.Code)
				require.Contains(t, rec.Body.String(), "basispoints_stream_incomplete")
				_, marked := GetOpsStreamError(c)
				require.True(t, marked, "HTTP 200 stream failure must enter Ops errors")
			} else {
				require.Equal(t, 502, rec.Code)
				require.Contains(t, rec.Body.String(), "basispoints_transport_error")
			}
		})
	}
}

func TestExcelBPSAttachmentLeaseCannotChangeExitOrReleaseOwner(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			lease := &bpsTestLease{}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			account := excelAccount()
			account.Extra["openai_excel_bps_mihomo"] = true
			calls := 0
			upstream := &bpsTestUpstream{send: func(r *http.Request, proxy string) (*http.Response, error) {
				calls++
				require.Equal(t, "http://127.0.0.1:19007", proxy)
				if fail {
					httptrace.ContextClientTrace(r.Context()).GetConn("bps.openai.com:443")
					return nil, errors.New("dial failed")
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("ok"))}, nil
			}}
			svc := openAIClientToolsTestService(nil)
			svc.httpUpstream = upstream
			resp, borrowed, _, err := svc.doExcelBPSRequest(context.Background(), c, account, "scope", []byte("{}"), "token", "account", pinnedExcelBPSAcquire("http://127.0.0.1:19007", lease))
			if fail {
				require.Error(t, err)
				require.Equal(t, 1, lease.failures)
			} else {
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
				borrowed.Release()
			}
			require.Equal(t, 1, calls)
			require.Zero(t, lease.releases)
			lease.Release()
			require.Equal(t, 1, lease.releases)
		})
	}
}
