//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesForwardFailedAttemptSettlement(t *testing.T) {
	const delta = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	const failure = `{"type":"response.failed","response":{"id":"resp_meter","status":"failed","error":{"code":"server_error","message":"upstream processing failed"}%s}}`
	for _, tt := range []struct {
		name, prefix, meter string
		in, out             int
		retry, unavailable  bool
	}{
		{name: "retryable-before-generation", retry: true},
		{name: "metered", meter: `,"usage":{"input_tokens":3,"output_tokens":4}`, in: 3, out: 4},
		{name: "zero", meter: `,"usage":{"input_tokens":0,"output_tokens":0}`},
		{name: "empty-meter", meter: `,"usage":{}`, unavailable: true},
		{name: "negative-input-valid-output", meter: `,"usage":{"input_tokens":-3,"output_tokens":4}`, out: 4},
		{name: "fractional-output-valid-input", meter: `,"usage":{"input_tokens":3,"output_tokens":4.5}`, in: 3},
		{name: "string-input", meter: `,"usage":{"input_tokens":"3"}`, unavailable: true},
		{name: "overflowing-output", meter: `,"usage":{"output_tokens":9223372036854775808}`, unavailable: true},
		{name: "inconsistent-reasoning-total", meter: fmt.Sprintf(`,"usage":{"input_tokens":%d,"output_tokens":%d,"total_tokens":1,"output_tokens_details":{"reasoning_tokens":22}}`, math.MaxInt, math.MaxInt-7), in: math.MaxInt, out: math.MaxInt-7},
		{name: "cache-only-zero", meter: `,"usage":{"input_tokens_details":{"cached_tokens":0}}`},
		{name: "content-without-meter", prefix: delta, unavailable: true},
		{name: "tool-before-arguments", prefix: "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"lookup\",\"call_id\":\"call_1\",\"arguments\":\"\"}}\n\n", unavailable: true},
		{name: "progressive-meter", prefix: "data: {\"type\":\"response.in_progress\",\"response\":{\"usage\":{\"input_tokens\":5}}}\n\n", in: 5},
	} {
		for _, stream := range []bool{true, false} {
			for _, pass := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/stream=%v/passthrough=%v", tt.name, stream, pass), func(t *testing.T) {
					wire := tt.prefix + "data: " + fmt.Sprintf(failure, tt.meter) + "\n\n"
					up := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(wire))}}
					acct := newOpenAIImageGenerationControlTestAccount()
					acct.Extra = map[string]any{"openai_passthrough": pass}
					svc := newOpenAIImageGenerationControlTestService(up)
					c, rec := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
					req := []byte(fmt.Sprintf(`{"model":"gpt-5.6-sol","stream":%t,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"test"}]}]}`, stream))
					res, err := svc.Forward(context.Background(), c, acct, req)
					require.Error(t, err)
					require.Len(t, up.requests, 1)
					var retry *UpstreamFailoverError
					require.Equal(t, tt.retry, errors.As(err, &retry))
					if tt.retry {
						require.Nil(t, res)
						require.Empty(t, rec.Body.Bytes())
						return
					}
					require.NotNil(t, res)
					require.Equal(t, "resp_meter", res.ResponseID)
					require.Equal(t, tt.in, res.Usage.InputTokens)
					require.Equal(t, tt.out, res.Usage.OutputTokens)
					require.Equal(t, tt.unavailable, res.UsageUnavailable)
					require.Equal(t, !tt.unavailable, res.ObservedUsageOnError)
					require.False(t, res.SucceededForScheduling())
					require.False(t, res.ClientDisconnect)
					require.NotEmpty(t, rec.Body.Bytes())
				})
			}
		}
	}
}

func TestResponsesForwardCRLF(t *testing.T) {
	const delta = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\r\n\r\n"
	for _, pass := range []bool{true, false} {
		for _, status := range []string{"completed", "failed"} {
			t.Run(fmt.Sprintf("passthrough=%v/%s", pass, status), func(t *testing.T) {
				terminal := fmt.Sprintf(`data: {"type":"response.%s","response":{"id":"resp_crlf","status":"%s","usage":{"input_tokens":3,"output_tokens":4}}}`, status, status)
				body := io.NopCloser(strings.NewReader(delta + terminal + "\r\n\r\n"))
				up := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}}
				acct := newOpenAIImageGenerationControlTestAccount()
				acct.Extra = map[string]any{"openai_passthrough": pass}
				svc := newOpenAIImageGenerationControlTestService(up)
				c, rec := newOpenAIImageGenerationControlTestContext(true, "codex_cli_rs/0.144.1")
				res, err := svc.Forward(context.Background(), c, acct, []byte(`{"model":"gpt-5.6-sol","stream":true,"input":"hello"}`))
				if status == "failed" {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
				}
				require.Len(t, up.requests, 1)
				require.NotNil(t, res)
				require.Equal(t, "resp_crlf", res.ResponseID)
				require.Equal(t, 3, res.Usage.InputTokens)
				require.Equal(t, 4, res.Usage.OutputTokens)
				require.Contains(t, rec.Body.String(), `"response.`+status+`"`)
			})
		}
	}
}
