package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIProcessingMemoryRejectsBeforeUpstream(t *testing.T) {
	for _, path := range []string{"responses", "chat", "messages", "ws"} {
		t.Run(path, func(t *testing.T) {
			budget := pkghttputil.NewBodyMemoryBudget(1)
			ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
			defer release()
			body := []byte("{\"model\":\"gpt-6.1-sol\",\"input\":\"hello\"}")
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
			// An unconfigured service cannot touch dependencies/upstream; every
			// real public boundary must reject before accessing them.
			svc := &OpenAIGatewayService{}
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var err error
			switch path {
			case "responses":
				_, err = svc.Forward(ctx, c, account, body)
			case "chat":
				_, err = svc.ForwardAsChatCompletions(ctx, c, account, body, "", "")
			case "messages":
				_, err = svc.ForwardAsAnthropic(ctx, c, account, body, "", "")
			case "ws":
				err = svc.ProxyResponsesWebSocketFromClient(ctx, c, nil, account, "", body, nil)
			}
			require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
			require.Zero(t, budget.Snapshot().UsedBytes)
		})
	}
}

func TestOpenAIWSReplayProcessingEnvelopeDoesNotAccumulatePerTurn(t *testing.T) {
	budget := pkghttputil.NewBodyMemoryBudget(4096)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	ctx, finish, err := beginOpenAIRequestMemory(ctx, []byte("{}"), false)
	require.NoError(t, err)
	for range 100 {
		require.NoError(t, ensureOpenAIWSProcessingMemory(ctx, []byte("{}"), 128))
	}
	used := budget.Snapshot().UsedBytes
	require.Less(t, used, int64(4096))
	err = ensureOpenAIWSProcessingMemory(ctx, []byte("{}"), 4096)
	require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
	var closeErr *OpenAIWSClientCloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, coderws.StatusTryAgainLater, closeErr.StatusCode())
	require.Equal(t, used, budget.Snapshot().UsedBytes)
	finish()
	require.Zero(t, budget.Snapshot().UsedBytes)
}

func TestOpenAIWSFirstFrameSharedBudget(t *testing.T) {
	budget := pkghttputil.NewBodyMemoryBudget(1024)
	done := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			done <- err
			return
		}
		defer conn.CloseNow()
		conn.SetReadLimit(64 << 20)
		ctx, release := pkghttputil.WithRequestBodyMemoryBudget(r.Context(), budget)
		_, _, err = ReadOpenAIWSClientMessage(ctx, conn, 3*time.Second, coderws.StatusPolicyViolation, "timeout")
		release()
		done <- err
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, server.URL, nil)
	require.NoError(t, err)
	defer client.CloseNow()
	_ = client.Write(ctx, coderws.MessageText, bytes.Repeat([]byte("x"), 4096))
	_, _, err = client.Read(ctx)
	require.Equal(t, coderws.StatusTryAgainLater, coderws.CloseStatus(err))
	require.ErrorIs(t, <-done, pkghttputil.ErrRequestBodyMemoryBudget)
	require.Zero(t, budget.Snapshot().UsedBytes)
}

func TestOpenAIWSQueuedFramesReleaseAfterClose(t *testing.T) {
	budget := pkghttputil.NewBodyMemoryBudget(1 << 20)
	readerReady := make(chan *openAIWSIngressReader, 1)
	cleanup := make(chan struct{})
	done := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			return
		}
		ctx, release := pkghttputil.WithRequestBodyMemoryBudget(r.Context(), budget)
		defer release()
		c, _ := gin.CreateTestContext(w)
		c.Request = r.WithContext(ctx)
		reader := openAIWSGetIngressReader(c, conn)
		readerReady <- reader
		<-cleanup
		CloseOpenAIWSIngressReader(c)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, _, err := coderws.Dial(ctx, server.URL, nil)
	require.NoError(t, err)
	defer client.CloseNow()
	reader := <-readerReady
	for range 3 {
		require.NoError(t, client.Write(ctx, coderws.MessageText, []byte("{}")))
	}
	require.Eventually(t, func() bool { return len(reader.frames) == 3 }, time.Second, time.Millisecond)
	require.Equal(t, int64(3*512), budget.Snapshot().UsedBytes)
	_, _, err = reader.read(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(3*512), budget.Snapshot().UsedBytes, "consumed frame remains held")
	_, _, err = reader.read(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(2*512), budget.Snapshot().UsedBytes, "next consumer handoff releases old raw frame")
	close(cleanup)
	<-done
	require.Zero(t, budget.Snapshot().UsedBytes)
}

func TestOpenAIWSBridgeRawFieldKeepsLastDuplicateAndNull(t *testing.T) {
	body := []byte("{\"tools\":[1],\"input\":\"large\",\"tools\":null}")
	field, present := openAIWSHTTPBridgeRawField(body, "tools")
	require.True(t, present)
	require.Equal(t, "null", string(field))
	_, present = openAIWSHTTPBridgeRawField([]byte("{broken"), "tools")
	require.False(t, present)
	// Returned fields own their bytes even when the parent buffer is reused.
	for i := range body {
		body[i] = 'x'
	}
	require.Equal(t, "null", string(field))
}

func TestOpenAIWSReplayMemoryTracksBackingAndShrinksAfterCompaction(t *testing.T) {
	original := make([]byte, 64<<10)
	// Tiny remaining items still pin the entire original request allocation.
	items := []json.RawMessage{original[100:110], original[200:210]}
	memory := &openAIWSReplayMemory{}
	size := memory.measure([][]json.RawMessage{items, items}, original)
	require.Equal(t, int64(cap(original)+2*cap(items)*24), size, "body backing charged once")
	size = memory.measure([][]json.RawMessage{{items[1]}})
	require.Equal(t, int64(cap(original)+24), size, "full parent capacity survives partial history")
	size = memory.measure(nil)
	require.Zero(t, size)
	require.Empty(t, memory.backings)

	budget := pkghttputil.NewBodyMemoryBudget(1 << 20)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	ctx, finish, err := beginOpenAIRequestMemory(ctx, []byte("{}"), false)
	require.NoError(t, err)
	defer finish()
	require.NoError(t, ensureOpenAIWSProcessingMemory(ctx, []byte("{}"), 64<<10))
	high := budget.Snapshot().UsedBytes
	require.NoError(t, resizeOpenAIWSProcessingMemory(ctx, []byte("{}"), 128))
	require.Less(t, budget.Snapshot().UsedBytes, high)
	small := budget.Snapshot().UsedBytes
	for range 100 {
		require.NoError(t, ensureOpenAIWSProcessingMemory(ctx, []byte("{}"), 64<<10))
		require.NoError(t, resizeOpenAIWSProcessingMemory(ctx, []byte("{}"), 128))
		require.Equal(t, small, budget.Snapshot().UsedBytes)
	}
}

func TestOpenAIWSMemoryReadLimitIncludesCompressedFrames(t *testing.T) {
	for _, size := range []int{1024, 1025} {
		t.Run(fmt.Sprintf("bytes=%d", size), func(t *testing.T) {
			budget := pkghttputil.NewBodyMemoryBudget(16 << 10)
			received := make(chan int, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := coderws.Accept(w, r, &coderws.AcceptOptions{CompressionMode: coderws.CompressionContextTakeover})
				if err != nil {
					received <- -1
					return
				}
				defer conn.CloseNow()
				conn.SetReadLimit(1024)
				ctx, release := pkghttputil.WithRequestBodyMemoryBudget(r.Context(), budget)
				_, body, err := ReadOpenAIWSClientMessage(ctx, conn, 3*time.Second, coderws.StatusPolicyViolation, "timeout")
				if err == nil {
					_ = conn.Write(context.Background(), coderws.MessageText, []byte("ok"))
				}
				release()
				received <- len(body)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, _, err := coderws.Dial(ctx, server.URL, &coderws.DialOptions{CompressionMode: coderws.CompressionContextTakeover, CompressionThreshold: 1})
			require.NoError(t, err)
			defer client.CloseNow()
			_ = client.Write(ctx, coderws.MessageText, bytes.Repeat([]byte("x"), size))
			_, ack, err := client.Read(ctx)
			if size == 1024 {
				require.NoError(t, err)
				require.Equal(t, "ok", string(ack))
				require.Equal(t, size, <-received)
			} else {
				require.Equal(t, coderws.StatusMessageTooBig, coderws.CloseStatus(err))
				require.Zero(t, <-received)
			}
			require.Zero(t, budget.Snapshot().UsedBytes)
		})
	}
}

func TestOpenAIWSReplayProcessingRejectsEffectiveHistoryBeforeSerialization(t *testing.T) {
	budget := pkghttputil.NewBodyMemoryBudget(4096)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	payload := []byte("{\"input\":[]}")
	ctx, finish, err := beginOpenAIRequestMemory(ctx, payload, false)
	require.NoError(t, err)
	defer finish()
	history := []json.RawMessage{json.RawMessage("\"" + strings.Repeat("x", 1024) + "\"")}
	err = ensureOpenAIWSReplayProcessingMemory(ctx, payload, history, 1024)
	require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
	// Dense structure gets charged even when its wire representation is small.
	history = []json.RawMessage{json.RawMessage("[" + strings.TrimSuffix(strings.Repeat("{\"a\":1},", 30), ",") + "]")}
	require.ErrorIs(t, ensureOpenAIWSReplayProcessingMemory(ctx, payload, history, 0), pkghttputil.ErrRequestBodyMemoryBudget)
}

func TestOpenAIWSMemoryReadRejectionDrainsDelayedUsage(t *testing.T) {
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 60
	cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 60
	upstream := newStagedPassthroughConn()
	svc := newPassthroughLifecycleService(cfg, upstream)
	budget := pkghttputil.NewBodyMemoryBudget(64 << 10)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	after := make(chan *OpenAIForwardResult, 2)
	afterErr := make(chan error, 2)
	contexts := make(chan *gin.Context, 1)
	server, done := startPassthroughLifecycleServerWithHooks(t, ctx, svc, passthroughLifecycleAccount(), func(c *gin.Context) *OpenAIWSIngressHooks {
		contexts <- c
		return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, err error) { after <- result; afterErr <- err }}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	defer client.CloseNow()
	requirePassthroughUpstreamWrite(t, upstream, time.Second)
	ginCtx := <-contexts
	// An unread new frame exhausts the pool while the first turn is submitted.
	_ = client.Write(context.Background(), coderws.MessageText, bytes.Repeat([]byte("x"), 128<<10))
	readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := client.Read(readCtx)
	require.Equal(t, coderws.StatusTryAgainLater, coderws.CloseStatus(err))
	select {
	case <-upstream.closed:
		t.Fatal("local memory close canceled submitted upstream before usage drain")
	default:
	}
	// Delay the terminal deliberately: immediate cancellation would lose it.
	time.AfterFunc(75*time.Millisecond, func() {
		upstream.Send("{\"type\":\"response.completed\",\"response\":{\"id\":\"resp_memory_drain\",\"usage\":{\"input_tokens\":9,\"output_tokens\":2}}}")
	})
	select {
	case err = <-done:
		require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
		var closeErr *OpenAIWSClientCloseError
		require.ErrorAs(t, err, &closeErr)
		require.Equal(t, coderws.StatusTryAgainLater, closeErr.StatusCode())
	case <-time.After(3 * time.Second):
		t.Fatal("memory close exceeded bounded usage drain")
	}
	result := <-after
	require.NotNil(t, result)
	require.Equal(t, 9, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.NoError(t, <-afterErr)
	require.Empty(t, after, "usage settlement must run once")
	require.Empty(t, upstream.writes, "rejected new frame must never be sent or replayed")
	CloseOpenAIWSIngressReader(ginCtx)
	release()
	require.Zero(t, budget.Snapshot().UsedBytes)
}

func TestOpenAIWSBridgeMemoryRejectsLargeHistoryBeforeSecondUpstream(t *testing.T) {
	cfg := passthroughLifecycleConfig()
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeThresholdBytes = 1
	tool := map[string]any{"id": "fc_large", "type": "function_call", "call_id": "call_large", "name": "inspect", "arguments": "{\"x\":\"" + strings.Repeat("x", 64<<10) + "\"}"}
	event, err := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_first", "output": []any{tool}, "usage": map[string]any{"input_tokens": 7, "output_tokens": 3}}})
	require.NoError(t, err)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: " + string(event) + "\n\n"))}}
	svc := newPassthroughLifecycleService(cfg, newStagedPassthroughConn())
	svc.httpUpstream = upstream
	account := passthroughLifecycleAccount()
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeHTTPBridge
	budget := pkghttputil.NewBodyMemoryBudget(512 << 10)
	ctx, release := pkghttputil.WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	after := make(chan *OpenAIForwardResult, 3)
	contexts := make(chan *gin.Context, 1)
	server, done := startPassthroughLifecycleServerWithHooks(t, ctx, svc, account, func(c *gin.Context) *OpenAIWSIngressHooks {
		contexts <- c
		return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) { after <- result }}
	})
	defer server.Close()
	client := dialPassthroughLifecycleClient(t, server)
	client.SetReadLimit(1 << 20)
	defer client.CloseNow()
	readCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err = client.Read(readCtx)
	require.NoError(t, err)
	first := <-after
	require.NotNil(t, first)
	require.Equal(t, 7, first.Usage.InputTokens)
	require.NoError(t, client.Write(readCtx, coderws.MessageText, []byte("{\"type\":\"response.create\",\"model\":\"gpt-5.1\",\"previous_response_id\":\"resp_first\",\"input\":[{\"type\":\"function_call_output\",\"call_id\":\"call_large\",\"output\":\"ok\"}]}")))
	select {
	case err = <-done:
		require.ErrorIs(t, err, pkghttputil.ErrRequestBodyMemoryBudget)
		var failover *UpstreamFailoverError
		require.False(t, errors.As(err, &failover))
	case <-time.After(3 * time.Second):
		t.Fatal("history admission did not terminate")
	}
	require.Len(t, upstream.requests, 1, "history refusal must precede the next HTTP send")
	CloseOpenAIWSIngressReader(<-contexts)
	release()
	require.Zero(t, budget.Snapshot().UsedBytes)
}
