//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/net/http2"
)

func TestBufferedResponsesForward(t *testing.T) {
	const progressive = "data: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_buffered\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n"
	const delta = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	const completed = "data: {\n: comment\ndata: \"response\":{\"id\":\"resp_buffered\",\"status\":\"completed\",\"output\":[]}}\nevent: response.completed\n\n"
	const failed = "data: {\ndata: \"response\":{\"id\":\"resp_buffered\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"provider failed\"},\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\nevent: response.failed\n\n"
	const req = `{"model":"gpt-5.6-sol","stream":false,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}]}`
	for _, tt := range []struct {
		name, wire, contentType, separator string
		status                             int
		metered                            bool
	}{
		{"cr-multiline-success", progressive + delta + completed, "text/event-stream", "\r", 200, true},
		{"bom-first-data-only", "\ufeffdata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_buffered\",\"output\":[{\"content\":[{\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n", "text/event-stream", "\n", 200, true},
		{"wrong-content-type", "\ufeff: heartbeat\n\n" + progressive + delta + completed, "application/json", "\r", 200, true},
		{"crlf-metered-failure", "\ufeff" + failed, "text/event-stream", "\r\n", 502, true},
		{"cr-metered-failure", "\ufeff" + failed, "text/event-stream", "\r", 502, true},
		{"meter-before-truncated-terminal", progressive + "event: response.completed\ndata: {\"response\":{\"id\":\"partial\"}", "text/event-stream", "\n", 502, true},
		{"empty-terminal", "data: {\"type\":\"response.completed\",\"response\":[]}\n\n", "text/event-stream", "\n", 502, false},
		{"invalid-multiline-terminal", progressive + "event: response.completed\ndata: {\"response\":{\"id\":\"invalid\"}}\ndata: broken\n\n", "text/event-stream", "\r", 502, true},
		{"terminal-failed-status", progressive + "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_buffered\",\"status\":\"failed\"}}\n\n", "text/event-stream", "\n", 502, true},
		{"no-terminal", "data: [DONE]\n\n", "text/event-stream", "\n", 502, false},
	} {
		for _, pass := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/passthrough=%t", tt.name, pass), func(t *testing.T) {
				wire := strings.ReplaceAll(tt.wire, "\n", tt.separator)
				up := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {tt.contentType}}, Body: io.NopCloser(strings.NewReader(wire))}}
				acct := newOpenAIImageGenerationControlTestAccount()
				acct.Extra = map[string]any{"openai_passthrough": pass}
				svc := newOpenAIImageGenerationControlTestService(up)
				c, rec := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
				res, err := svc.Forward(context.Background(), c, acct, []byte(req))
				require.Len(t, up.requests, 1)
				require.Equal(t, tt.status, rec.Code)
				require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
				require.True(t, json.Valid(rec.Body.Bytes()), "response=%q", rec.Body.String())
				if tt.status == 200 {
					require.NoError(t, err)
					require.Equal(t, "hello", gjson.GetBytes(rec.Body.Bytes(), "output.0.content.0.text").String())
				} else {
					require.Error(t, err)
					var retry *UpstreamFailoverError
					require.NotErrorAs(t, err, &retry)
					require.Equal(t, "upstream_error", gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
				}
				if tt.metered {
					require.NotNil(t, res)
					require.Equal(t, "resp_buffered", res.ResponseID)
					require.Equal(t, 3, res.Usage.InputTokens)
					require.Equal(t, 4, res.Usage.OutputTokens)
					require.Equal(t, tt.status == 502, res.ObservedUsageOnError)
				} else {
					require.Nil(t, res)
				}
			})
		}
	}
}

func TestBufferedResponsesForwardInterruptedBody(t *testing.T) {
	const meter = "\ufeffdata: {\"type\":\"response.in_progress\",\"response\":{\"id\":\"resp_reset\",\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}"
	const req = `{"model":"gpt-5.6-sol","stream":false,"input":"test"}`
	reset := http2.StreamError{StreamID: 1, Code: http2.ErrCodeInternal, Cause: errors.New("received from peer")}
	for _, tt := range []struct {
		name, body string
		maxBytes   int64
		metered    bool
	}{
		{"reset-after-event", meter + "\r\rdata: {\"usage\":", 0, true},
		{"reset-before-delimiter", meter + "\r", 0, false},
		{"limit-after-event", meter + "\r\rdata: too large", int64(len(meter) + 2), true},
		{"limit-before-delimiter", meter + "\r\r", int64(len(meter) + 1), false},
	} {
		for _, pass := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/passthrough=%t", tt.name, pass), func(t *testing.T) {
				reader := io.MultiReader(strings.NewReader(tt.body), iotest.ErrReader(reset))
				up := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(reader)}}
				acct := newOpenAIImageGenerationControlTestAccount()
				acct.Extra = map[string]any{"openai_passthrough": pass}
				svc := newOpenAIImageGenerationControlTestService(up)
				svc.cfg.Gateway.UpstreamResponseReadMaxBytes = tt.maxBytes
				c, rec := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
				res, err := svc.Forward(context.Background(), c, acct, []byte(req))
				require.Len(t, up.requests, 1)
				require.Error(t, err)
				var retry *UpstreamFailoverError
				require.NotErrorAs(t, err, &retry)
				require.Equal(t, http.StatusBadGateway, rec.Code)
				if tt.maxBytes > 0 {
					require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
				} else {
					require.Equal(t, OpenAIUpstreamHTTP2StreamErrorCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
				}
				if !tt.metered {
					require.Nil(t, res)
					return
				}
				require.NotNil(t, res)
				require.True(t, res.ObservedUsageOnError)
				require.False(t, res.SucceededForScheduling())
				require.Equal(t, "resp_reset", res.ResponseID)
				require.Equal(t, 3, res.Usage.InputTokens)
				require.Equal(t, 4, res.Usage.OutputTokens)
			})
		}
	}
}
