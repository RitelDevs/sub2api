package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiToolArgumentsMustRemainObjects(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, reason := range []string{"tool_calls", "length"} {
			for _, args := range []string{`{"raw":"original","n":9007199254740993}`, "{", "null", "[]"} {
				t.Run(strings.Join([]string{map[bool]string{false: "json", true: "stream"}[stream], reason, args}, "/"), func(t *testing.T) {
					call := ChatToolCall{Function: ChatFunctionCall{Name: "lookup", Arguments: args}}
					var out []byte
					var err error
					if stream {
						tr := NewGeminiSSETranslator("fixture")
						wire, _ := json.Marshal(ChatCompletionsChunk{Choices: []ChatChunkChoice{{Index: 0, Delta: ChatDelta{ToolCalls: []ChatToolCall{call}}, FinishReason: &reason}}})
						out = tr.Push(append(append([]byte("data: "), wire...), []byte("\n\n")...))
						out = append(out, tr.Flush()...)
						err = tr.Err()
					} else {
						wire, _ := json.Marshal(ChatCompletionsResponse{Choices: []ChatChoice{{Index: 0, Message: ChatMessage{ToolCalls: []ChatToolCall{call}}, FinishReason: reason}}})
						out, err = ChatCompletionsJSONToGemini(wire, "fixture")
					}
					if args[0] == '{' && json.Valid([]byte(args)) {
						require.NoError(t, err)
						require.Contains(t, string(out), `"raw":"original"`)
						require.Contains(t, string(out), "9007199254740993")
					} else if reason == "length" {
						require.NoError(t, err)
						require.NotContains(t, string(out), "functionCall")
						require.Contains(t, string(out), "MAX_TOKENS")
					} else {
						require.Error(t, err)
						require.NotContains(t, string(out), "functionCall")
					}
				})
			}
		}
	}
}

func TestGeminiStreamRequiresCandidateTerminals(t *testing.T) {
	for _, wire := range []string{
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n",
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"},{\"index\":1,\"delta\":{\"content\":\"partial\"}}]}\n\ndata: [DONE]\n\n",
		"data: {\"choices\":[],\"usage\":{\"total_tokens\":7}}\n\ndata: [DONE]\n\n",
	} {
		tr := NewGeminiSSETranslator("fixture")
		out := tr.Push([]byte(wire))
		out = append(out, tr.Flush()...)
		require.Error(t, tr.Err())
		require.Contains(t, string(out), `"error"`)
	}
}

func TestGeminiStreamKeepsUsageBeforeAndAfterTerminal(t *testing.T) {
	tr := NewGeminiSSETranslator("fixture")
	wire := "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}],\"usage\":{\"total_tokens\":5}}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"total_tokens\":7}}\n\ndata: [DONE]\n\n"
	out := tr.Push([]byte(wire))
	out = append(out, tr.Flush()...)
	require.NoError(t, tr.Err())
	var usage []int64
	for _, data := range sseDataPayloads(string(out)) {
		if value := gjson.Get(data, "usageMetadata.totalTokenCount"); value.Exists() {
			usage = append(usage, value.Int())
		}
	}
	require.Equal(t, []int64{5, 7}, usage)
}
