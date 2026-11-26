package apicompat

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiToolArgumentsMaySpanSeveralFullEvents(t *testing.T) {
	const frameLimit = 256
	translator := NewGeminiSSETranslator("m", frameLimit)
	value := strings.Repeat("x", 80)
	for _, fragment := range []string{`{"text":"`, value, value, value, value, `"}`} {
		arg, err := json.Marshal(fragment)
		require.NoError(t, err)
		chunk := `{"choices":[{"index":0,"delta":{"tool_calls":[{"id":"call-a","function":{"name":"lookup","arguments":` + string(arg) + `}}]}}]}`
		require.Less(t, len(chunk)+len("data: \n"), frameLimit)
		_, err = translator.Convert([]byte("data: "+chunk+"\n\n"), false)
		require.NoError(t, err)
	}
	out, err := translator.Convert([]byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"), true)
	require.NoError(t, err)
	require.Contains(t, string(out), strings.Repeat(value, 4))
	_, err = NewGeminiSSETranslator("m", frameLimit).Convert([]byte("data: "+strings.Repeat("x", frameLimit)+"\n\n"), true)
	require.Error(t, err)
}

func TestGeminiResponsePreservesThoughtRefusalAndToolIdentity(t *testing.T) {
	const args = `{"n":9007199254740993,"raw":"original"}`
	call := ChatToolCall{ID: "call-A", Function: ChatFunctionCall{Name: "lookup", Arguments: args}}
	text, thought, refusal, finish := "text", "thought", "refused", "tool_calls"
	st := NewGeminiSSETranslator("m")
	st.ExpectCandidates(1)
	b, err := json.Marshal(ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{Content: &text, ReasoningContent: &thought, Refusal: &refusal, ToolCalls: []ChatToolCall{call}}, FinishReason: &finish}}})
	require.NoError(t, err)
	stream, err := st.Convert([]byte("data: "+string(b)+"\n\ndata: [DONE]\n\n"), true)
	require.NoError(t, err)
	b, err = json.Marshal(ChatCompletionsResponse{Choices: []ChatChoice{{Message: ChatMessage{Content: json.RawMessage(`"text"`), ReasoningContent: thought, Refusal: refusal, ToolCalls: []ChatToolCall{call}}, FinishReason: finish}}})
	require.NoError(t, err)
	normal, err := ChatCompletionsJSONToGemini(b, "m", 1)
	require.NoError(t, err)
	for _, data := range []string{firstSSEPayload(string(stream)), string(normal)} {
		parts := gjson.Get(data, "candidates.0.content.parts")
		require.True(t, parts.Get("0.thought").Bool())
		require.Equal(t, "thought", parts.Get("0.text").String())
		require.Equal(t, "text", parts.Get("1.text").String())
		require.Equal(t, "refused", parts.Get("2.text").String())
		require.Equal(t, "call-A", parts.Get("3.functionCall.id").String())
		require.Equal(t, args, parts.Get("3.functionCall.args").Raw)
	}
}

func TestGeminiResponseToolArgumentIntegrity(t *testing.T) {
	for _, args := range []string{"{", "null", "[]", "7", ""} {
		for _, reason := range []string{"length", "tool_calls"} {
			call := ChatToolCall{ID: "a", Function: ChatFunctionCall{Name: "run", Arguments: args}}
			b, err := json.Marshal(ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{call}}, FinishReason: &reason}}})
			require.NoError(t, err)
			out, err := NewGeminiSSETranslator("m").Convert([]byte("data: "+string(b)+"\n\n"), true)
			if reason == "length" {
				require.NoError(t, err)
				require.Contains(t, string(out), "MAX_TOKENS")
				require.NotContains(t, string(out), "functionCall")
			} else if args == "" {
				require.NoError(t, err)
				require.Contains(t, string(out), `"args":{}`)
			} else {
				require.ErrorIs(t, err, ErrGeminiResponseConversion)
				require.Empty(t, out)
			}
		}
	}
}

func TestGeminiStreamAnnotatedTerminalAndCardinality(t *testing.T) {
	const stop = `{"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`
	frame := func(data string) string { return "data: " + data + "\n\n" }
	for _, tc := range []struct {
		name, tail string
		n          int
		ok         bool
	}{
		{"Azure", frame(`{"choices":[{"index":0,"finish_reason":null,"content_filter_results":{"hate":{"filtered":false}},"content_filter_offsets":{"check_offset":42}}]}`) + frame("[DONE]"), 1, true},
		{"missing candidate", frame("[DONE]"), 2, false},
		{"post-terminal delta", frame(`{"choices":[{"index":0,"delta":{"content":"oops"}}]}`), 1, false},
		{"OpenRouter", frame(`{"error":{"message":"fixture"},"choices":[{"index":0,"delta":{},"finish_reason":"error"}]}`), 1, false},
		{"post-done data", frame("[DONE]") + frame(`{"usage":{"total_tokens":1}}`), 1, false},
		{"duplicate done", frame("[DONE]") + frame("[DONE]"), 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tr := NewGeminiSSETranslator("m")
			tr.ExpectCandidates(tc.n)
			out, err := tr.Convert([]byte(frame(stop)+tc.tail), true)
			require.Equal(t, tc.ok, err == nil)
			require.Equal(t, 1, strings.Count(string(out), `"STOP"`))
			if !tc.ok {
				require.True(t, errors.Is(err, ErrGeminiResponseConversion))
			}
		})
	}
}

func TestGeminiPartialUsage(t *testing.T) {
	tr := NewGeminiSSETranslator("m")
	wire := `data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"completion_tokens":0,"prompt_tokens_details":{"cached_tokens":2}}}` + "\n\n"
	out, err := tr.Convert([]byte(wire), true)
	require.NoError(t, err)
	counts := gjson.Get(sseDataPayloads(string(out))[1], "usageMetadata")
	require.Equal(t, "0", counts.Get("candidatesTokenCount").Raw)
	require.Equal(t, "2", counts.Get("cachedContentTokenCount").Raw)
	require.False(t, counts.Get("promptTokenCount").Exists())
	require.False(t, counts.Get("totalTokenCount").Exists())
}
