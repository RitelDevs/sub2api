//go:build unit

package service

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnthropicTerminalDeliveryContract(t *testing.T) {
	start := `data: {"type":"message_start","message":{"id":"msg_delivery","role":"assistant","content":[],"usage":{"input_tokens":12,"output_tokens":1,"cache_creation_input_tokens":10,"cache_creation":{"ephemeral_5m_input_tokens":3,"ephemeral_1h_input_tokens":7}}}}` + "\n\n"
	text := compatTextBlockEvents + `data: {"type":"content_block_stop","index":0}` + "\n\n"
	final := `data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}` + "\n\n"
	stop := `data: {"type":"message_stop"}` + "\n\n"
	for _, route := range []string{"responses", "chat_completions"} {
		for _, stream := range []bool{true, false} {
			for _, tc := range []struct {
				name, wire string
				ok         bool
				output     int
			}{
				{"ordinary", start + text + final + stop, true, 2},
				{"bare-cr", strings.ReplaceAll(start+text+final+stop, "\n", "\r"), true, 2},
				{"empty-end-turn", start + final + stop, true, 2},
				{"known-final-zero", start + text + strings.Replace(final, `"output_tokens":2`, `"output_tokens":0`, 1) + stop, true, 0},
				{"stop-then-error", start + text + final + stop + compatOverloadedErrorEvent, true, 2},
				{"error-before-stop", start + text + final + compatOverloadedErrorEvent, false, 2},
				{"missing-final-delta", start + text + stop, false, 1},
				{"missing-final-count", start + text + strings.Replace(final, `"output_tokens":2`, `"input_tokens":12`, 1) + stop, false, 1},
				{"missing-stop", start + text + final, false, 2},
			} {
				t.Run(fmt.Sprintf("%s/stream_%t/%s", route, stream, tc.name), func(t *testing.T) {
					s, c, rec, body, account := newCompatFailoverTestRequest(t, route, stream, tc.wire)
					result, err := forwardCompatForTest(s, c, route, body, account)
					if tc.ok {
						require.NoError(t, err)
						require.Equal(t, 200, rec.Code)
						require.NotContains(t, rec.Body.String(), `"error":{`)
					} else {
						var switchAccount *UpstreamFailoverError
						require.Error(t, err)
						require.False(t, errors.As(err, &switchAccount))
						require.Contains(t, rec.Body.String(), `"error":{`)
						require.NotContains(t, rec.Body.String(), "[DONE]")
						require.NotContains(t, rec.Body.String(), "event: response.completed")
					}
					require.NotNil(t, result)
					require.Equal(t, 12, result.Usage.InputTokens)
					require.Equal(t, tc.output, result.Usage.OutputTokens)
					require.Equal(t, 3, result.Usage.CacheCreation5mTokens)
					require.Equal(t, 7, result.Usage.CacheCreation1hTokens)
				})
			}
		}
	}
}

func TestAnthropicRetryAfterGeneration(t *testing.T) {
	for _, route := range []string{"responses", "chat_completions"} {
		for _, stream := range []bool{true, false} {
			for _, tc := range []struct {
				name, generation string
				knownZero        bool
			}{
				{"explicit-zero", `{"type":"message_start","message":{"id":"msg","usage":{"input_tokens":0}}}`, true},
				{"tool-arguments", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`, false},
				{"signature", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig"}}`, false},
			} {
				t.Run(fmt.Sprintf("%s/stream_%t/%s", route, stream, tc.name), func(t *testing.T) {
					wire := "data: " + tc.generation + "\n\n" + compatOverloadedErrorEvent
					svc, c, rec, body, account := newCompatFailoverTestRequest(t, route, stream, wire)
					got, err := forwardCompatForTest(svc, c, route, body, account)
					var retry *UpstreamFailoverError
					require.Error(t, err)
					require.False(t, errors.As(err, &retry))
					require.Contains(t, rec.Body.String(), "overloaded_error")
					if tc.knownZero {
						require.NotNil(t, got)
						require.Zero(t, got.Usage.OutputTokens)
					} else {
						require.Nil(t, got)
					}
				})
			}
		}
	}
}
