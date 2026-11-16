//go:build unit

package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type memoryAdmissionUpstream struct{ calls int }

func (u *memoryAdmissionUpstream) Do(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
	u.calls++
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewBufferString("{\"id\":\"resp_memory\",\"status\":\"completed\",\"model\":\"gpt-5.1\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\"usage\":{\"input_tokens\":7,\"output_tokens\":3}}"))}, nil
}

func TestProcessingMemoryHandlerRejectsWithoutProviderFailureOrReplay(t *testing.T) {
	for _, path := range []string{"responses", "chat"} {
		t.Run(path, func(t *testing.T) {
			upstream := &memoryAdmissionUpstream{}
			account := service.Account{ID: 1, Name: "memory-fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{3131}, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://fixture.example"}}
			second := account
			second.ID = 2
			h := newOpenAIResponsesFailoverTestHandler(t, upstream, account, second)
			body := []byte("{\"model\":\"gpt-5.1\",\"stream\":false,\"input\":\"hello\"}")
			budgetSize := int64(1)
			if path == "chat" {
				body = []byte("{\"model\":\"gpt-5.1\",\"stream\":false,\"messages\":[{\"role\":\"user\",\"content\":\"hello\"}]}")
				// Allow handler admission, then deny the heavier service adapter.
				budgetSize = int64(len(body)) + pkghttputil.RequestBodyProcessingEstimate(body, service.OpenAINativeProcessingCopies) + 1
			}
			budget := pkghttputil.NewBodyMemoryBudget(budgetSize)
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer release()
			c, w := newOpenAIResponsesFailoverTestContext(t, ctx)
			c.Request.Body = pkghttputil.NewPrereadBody(body)
			if path == "chat" {
				require.NoError(t, pkghttputil.ReserveRequestBodyMemory(ctx, int64(len(body))))
			}
			if path == "responses" {
				h.Responses(c)
			} else {
				h.ChatCompletions(c)
			}
			require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), "capacity")
			require.Zero(t, upstream.calls, "must not send/retry a tool or call either account")
			require.Zero(t, h.gatewayService.SnapshotOpenAIAccountSchedulerMetrics().RuntimeStatsAccountCount, "local capacity is not provider health")
			release()
			require.Zero(t, budget.Snapshot().UsedBytes)
		})
	}
}

func TestProcessingMemoryRejectHelperPreservesExistingUsage(t *testing.T) {
	c, w := newOpenAIResponsesFailoverTestContext(t, context.Background())
	h := &OpenAIGatewayHandler{}
	used := &service.OpenAIForwardResult{Usage: service.OpenAIUsage{InputTokens: 7, OutputTokens: 3}}
	require.False(t, h.rejectOpenAIProcessingMemory(c, used, pkghttputil.ErrRequestBodyMemoryBudget, false, false))
	require.False(t, w.Flushed)
	require.Empty(t, w.Body.String())
	require.Equal(t, 7, used.Usage.InputTokens)
	require.Equal(t, 3, used.Usage.OutputTokens)
	require.True(t, h.rejectOpenAIProcessingMemory(c, nil, pkghttputil.ErrRequestBodyMemoryBudget, false, false))
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func (u *memoryAdmissionUpstream) DoWithTLS(req *http.Request, proxy string, id int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, id, concurrency)
}

func TestProcessingMemoryAdmittedRequestRecordsUsageOnce(t *testing.T) {
	upstream := &memoryAdmissionUpstream{}
	usage := &openAIWSUsageHandlerUsageLogRepoStub{created: make(chan *service.UsageLog, 4)}
	account := service.Account{ID: 1, Name: "memory-fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{3131}, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://fixture.example"}}
	h := newOpenAIResponsesFailoverTestHandlerWithUsage(t, upstream, nil, nil, usage, account)
	budget := pkghttputil.NewBodyMemoryBudget(1 << 20)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	c, w := newOpenAIResponsesFailoverTestContext(t, ctx)
	h.Responses(c)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	require.Equal(t, 1, upstream.calls)
	select {
	case record := <-usage.created:
		require.Equal(t, 7, record.InputTokens)
		require.Equal(t, 3, record.OutputTokens)
	case <-time.After(2 * time.Second):
		t.Fatal("admitted request usage was not recorded")
	}
	select {
	case <-usage.created:
		t.Fatal("request usage recorded twice")
	default:
	}
	release()
	require.Zero(t, budget.Snapshot().UsedBytes)
}
