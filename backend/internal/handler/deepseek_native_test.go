//go:build unit

package handler

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type deepSeekTestUpstream struct {
	service.HTTPUpstream
	mu       sync.Mutex
	ids      []int64
	bodies   [][]byte
	response func(int, *http.Request) (int, string)
}

func TestDeepSeekNativeScheduling(t *testing.T) {
	for _, protocol := range []string{"chat", "messages"} {
		for _, tc := range []struct {
			name                      string
			switches, failures, calls int
			partial, receipt          bool
		}{
			{"configured zero switches", 0, 1, 1, false, false},
			{"fourth account succeeds", 3, 3, 4, false, true},
			{"first failure recovers", 1, 1, 2, false, false},
			{"partial stream ends", 3, 1, 1, true, false},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				var rec *httptest.ResponseRecorder
				u := &deepSeekTestUpstream{response: func(n int, _ *http.Request) (int, string) {
					if n == 1 {
						require.False(t, rec.Flushed, "no prelude before the first upstream request")
					}
					if n <= tc.failures {
						if tc.partial {
							return 200, deepSeekChunk(`{"content":"PARTIAL"}`, "null")
						}
						return 503, `{"error":{"message":"overloaded"}}`
					}
					return 200, deepSeekSuccess()
				}}
				billing := &deepSeekTestBilling{}
				h, c, recorder, _ := deepSeekHandlerFixture(t, protocol, u, billing)
				rec = recorder
				h.maxAccountSwitches = tc.switches
				if tc.receipt {
					c.Request.Header.Set("X-Sub2API-Billing-Receipt", "1")
				}
				if protocol == "chat" {
					h.ChatCompletions(c)
				} else {
					h.Messages(c)
				}
				require.Len(t, u.ids, tc.calls, rec.Body.String())
				for i, id := range u.ids {
					require.Equal(t, int64(i+1), id, "native priority order")
				}
				require.NotContains(t, rec.Body.String(), "正在重新生成")
				require.NotContains(t, rec.Body.String(), ": keep-alive\n\n")
				if tc.partial {
					require.Contains(t, rec.Body.String(), "PARTIAL")
					return
				}
				if tc.calls > tc.failures {
					require.Contains(t, rec.Body.String(), "SUCCESS")
				}
				if tc.calls <= tc.failures {
					require.Empty(t, billing.events)
				} else {
					require.Len(t, billing.events, 1)
					require.Equal(t, u.ids[len(u.ids)-1], billing.events[0].Command.AccountID)
					require.EqualValues(t, 3, billing.events[0].Command.OutputTokens)
					require.Positive(t, billing.events[0].Command.BalanceCost)
				}
			})
		}
	}
}

func TestDeepSeekNativeRequestErrors(t *testing.T) {
	const limitError = `{"error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"Input exceeds the model context window"}}`
	const observedUsage = "data: {\"id\":\"source\",\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"total_tokens\":15}}\n\n"
	for _, protocol := range []string{"chat", "messages"} {
		for _, tc := range []struct {
			name   string
			status int
			body   string
			want   string
			paid   bool
		}{
			{"invalid request", 400, `{"error":{"type":"invalid_request_error","message":"Unsupported fixture option"}}`, "Unsupported fixture option", false},
			{"context limit", 400, limitError, "context_length_exceeded", false},
			{"input content rejection", 400, `{"error":{"message":"Input text data may contain inappropriate content."}}`, "permission_error", false},
			{"stream limit with partial usage", 200, deepSeekChunk(`{"content":"PARTIAL"}`, "null") + observedUsage + "data: " + limitError + "\n\n", "context_length_exceeded", true},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				journal := &deepSeekTestBilling{}
				upstream := &deepSeekTestUpstream{response: func(int, *http.Request) (int, string) { return tc.status, tc.body }}
				h, c, rec, _ := deepSeekHandlerFixture(t, protocol, upstream, journal)
				h.maxAccountSwitches = 3
				c.Request.Header.Set("X-Sub2API-Billing-Receipt", "1")
				if protocol == "chat" {
					h.ChatCompletions(c)
				} else {
					h.Messages(c)
				}
				require.Equal(t, []int64{1}, upstream.ids)
				require.Contains(t, rec.Body.String(), tc.want)
				if tc.paid {
					require.Contains(t, rec.Body.String(), "PARTIAL")
					require.Len(t, journal.events, 1)
					require.EqualValues(t, 12, journal.events[0].Command.InputTokens)
					require.EqualValues(t, 3, journal.events[0].Command.OutputTokens)
					require.Positive(t, journal.events[0].Command.BalanceCost)
				} else {
					require.Empty(t, journal.events)
				}
			})
		}
	}
}

func TestDeepSeekNativeNonstreamSwitchLimit(t *testing.T) {
	for _, protocol := range []string{"chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			upstream := &deepSeekTestUpstream{response: func(int, *http.Request) (int, string) {
				return 503, `{"error":{"message":"overloaded"}}`
			}}
			journal := &deepSeekTestBilling{}
			h, c, rec, _ := deepSeekHandlerFixture(t, protocol, upstream, journal)
			body := `{"model":"deepseek-flash","stream":false,"max_tokens":32,"messages":[{"role":"user","content":"hello"}]}`
			c.Request.Body = io.NopCloser(strings.NewReader(body))
			c.Request.ContentLength = int64(len(body))
			c.Request.Header.Set("X-Sub2API-Billing-Receipt", "1")
			h.maxAccountSwitches = 1
			if protocol == "chat" {
				h.ChatCompletions(c)
			} else {
				h.Messages(c)
			}
			require.Equal(t, []int64{1, 2}, upstream.ids)
			require.NotContains(t, rec.Body.String(), ": keep-alive")
			require.Empty(t, journal.events)
		})
	}
}

func (u *deepSeekTestUpstream) Do(req *http.Request, _ string, id int64, _ int) (*http.Response, error) {
	body, _ := io.ReadAll(req.Body)
	u.mu.Lock()
	u.ids = append(u.ids, id)
	u.bodies = append(u.bodies, body)
	n := len(u.ids)
	u.mu.Unlock()
	status, data := u.response(n, req)
	ct := "text/event-stream"
	if !strings.HasPrefix(data, "data:") {
		ct = "application/json"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {ct}, "X-Request-Id": {fmt.Sprintf("attempt-%d", n)}}, Body: io.NopCloser(strings.NewReader(data))}, nil
}

func deepSeekChunk(delta, finish string) string {
	return fmt.Sprintf("data: {\"id\":\"source\",\"object\":\"chat.completion.chunk\",\"model\":\"deepseek-flash\",\"choices\":[{\"index\":0,\"delta\":%s,\"finish_reason\":%s}]}\n\n", delta, finish)
}

func deepSeekSuccess() string {
	return deepSeekChunk(`{"content":"SUCCESS"}`, "null") +
		deepSeekChunk(`{}`, `"stop"`) +
		"data: {\"id\":\"source\",\"choices\":[],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":3,\"total_tokens\":15}}\n\ndata: [DONE]\n\n"
}

func deepSeekHandlerFixture(t *testing.T, protocol string, upstream *deepSeekTestUpstream, billing ...service.UsageBillingRepository) (*OpenAIGatewayHandler, *gin.Context, *httptest.ResponseRecorder, *openAIWSUsageHandlerUsageLogRepoStub) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	groupID := int64(17)
	var accounts []service.Account
	for i := int64(1); i <= 4; i++ {
		accounts = append(accounts, service.Account{ID: i, Name: fmt.Sprintf("deepseek-%d", i), Platform: "deepseek", Type: service.AccountTypeAPIKey,
			Status: service.StatusActive, Schedulable: true, Priority: int(i), Concurrency: 1, GroupIDs: []int64{groupID},
			Credentials: map[string]any{"api_key": "sk-fixture", "base_url": "https://deepseek.example.test", "api_protocol": "chat_completions"}})
	}
	return deepSeekHandlerWithRepo(t, protocol, upstream, openAIImagesFailoverAccountRepo{accounts: accounts}, billing...)
}

func deepSeekHandlerWithRepo(t *testing.T, protocol string, upstream *deepSeekTestUpstream, repo service.AccountRepository, billing ...service.UsageBillingRepository) (*OpenAIGatewayHandler, *gin.Context, *httptest.ResponseRecorder, *openAIWSUsageHandlerUsageLogRepoStub) {
	t.Helper()
	groupID := int64(17)
	usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 16)}
	cfg := &config.Config{RunMode: config.RunModeSimple}
	cfg.Default.RateMultiplier = 1
	admissionCfg := *cfg // This fixture exercises billing with an in-memory journal after admission.
	cache := service.NewBillingCacheService(nil, nil, nil, nil, nil, nil, &admissionCfg, nil)
	t.Cleanup(cache.Stop)
	slots := service.NewConcurrencyService(&concurrencyCacheMock{acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) { return true, nil }})
	var billingRepo service.UsageBillingRepository
	if len(billing) > 0 {
		billingRepo = billing[0]
		cfg.RunMode = "standard"
	}
	svc := service.NewOpenAIGatewayService(repo, nil, usage, billingRepo, nil, nil, nil, nil, cfg, nil, slots, service.NewBillingService(cfg, nil), nil, cache, upstream, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil)
	h := NewOpenAIGatewayHandler(svc, slots, cache, service.NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg), nil, nil, nil, nil, cfg)
	body := `{"model":"deepseek-flash","stream":true,"max_tokens":64000,"messages":[{"role":"user","content":"hello"}]}`
	path := "/v1/chat/completions"
	if protocol == "messages" {
		path = "/v1/messages"
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	core, observed := observer.New(zap.DebugLevel)
	c.Request = c.Request.WithContext(logger.IntoContext(c.Request.Context(), zap.New(core)))
	t.Cleanup(func() {
		if t.Failed() {
			for _, entry := range observed.All() {
				t.Log(entry.Message, entry.ContextMap())
			}
		}
	})
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.ClientRequestID, "deepseek-native-test"))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 99, GroupID: &groupID, Group: &service.Group{ID: groupID, Platform: "deepseek", RateMultiplier: 1}, User: &service.User{ID: 100}})
	c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 100, Concurrency: 0})
	return h, c, rec, usage
}

type deepSeekTestBilling struct {
	service.UsageBillingRepository
	events       []service.UsageBillingEvent
	applications int
}

func (b *deepSeekTestBilling) StageUsageBilling(_ context.Context, c *service.UsageBillingCommand, u *service.UsageLog) error {
	b.events = append(b.events, service.UsageBillingEvent{Version: 1, Command: *c, Usage: *u})
	return nil
}
func (b *deepSeekTestBilling) ApplyStagedUsageBilling(context.Context, service.UsageBillingEventKey) (*service.UsageBillingRecovery, error) {
	b.applications++
	return &service.UsageBillingRecovery{Event: b.events[len(b.events)-1], Result: service.UsageBillingApplyResult{Applied: true}}, nil
}
func (*deepSeekTestBilling) PendingUsageBilling(context.Context, int) ([]service.UsageBillingEventKey, error) {
	return nil, nil
}
func (*deepSeekTestBilling) AcknowledgeUsageBillingCache(context.Context, service.UsageBillingEventKey) error {
	return nil
}
