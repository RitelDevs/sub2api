package apicompat

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiToolRoundTripKeepsParallelIDs(t *testing.T) {
	const calls = `[{"index":0,"id":"first","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Paris\"}"}},{"index":1,"id":"second","type":"function","function":{"name":"lookup","arguments":"{\"city\":\"Tokyo\"}"}}]`
	for _, mode := range []string{"json", "stream"} {
		t.Run(mode, func(t *testing.T) {
			var raw string
			if mode == "json" {
				out, err := ChatCompletionsJSONToGemini([]byte(`{"choices":[{"message":{"tool_calls":`+calls+`},"finish_reason":"tool_calls"}]}`), "model")
				require.NoError(t, err)
				raw = string(out)
			} else {
				tr := NewGeminiSSETranslator("model")
				require.Empty(t, tr.Push([]byte(`data: {"choices":[{"index":0,"delta":{"tool_calls":`+calls+`}}]}`+"\n\n")))
				out := tr.Push([]byte(`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}` + "\n\ndata: [DONE]\n\n"))
				require.NoError(t, tr.Err())
				raw = firstSSEPayload(string(out))
			}
			content := gjson.Get(raw, "candidates.0.content")
			require.Equal(t, "first", content.Get("parts.0.functionCall.id").String())
			require.Equal(t, "second", content.Get("parts.1.functionCall.id").String())
			body := `{"contents":[` + content.Raw + `,{"role":"user","parts":[
				{"functionResponse":{"id":"second","name":"lookup","response":{"output":"Tokyo result"}}},
				{"functionResponse":{"id":"first","name":"lookup","response":{"output":"Paris result"}}}]}]}`
			wire, err := GeminiGenerateContentToChatCompletions("model", []byte(body), false)
			require.NoError(t, err)
			messages := gjson.GetBytes(wire, "messages").Array()
			require.Len(t, messages, 3)
			require.Equal(t, `{"city":"Paris"}`, messages[0].Get("tool_calls.0.function.arguments").String())
			require.Equal(t, `{"city":"Tokyo"}`, messages[0].Get("tool_calls.1.function.arguments").String())
			require.Equal(t, messages[0].Get("tool_calls.1.id").String(), messages[1].Get("tool_call_id").String())
			require.Equal(t, "Tokyo result", messages[1].Get("content").String())
			require.Equal(t, messages[0].Get("tool_calls.0.id").String(), messages[2].Get("tool_call_id").String())
			require.Equal(t, "Paris result", messages[2].Get("content").String())
		})
	}
}

func TestGeminiRejectsConflictingToolHistory(t *testing.T) {
	const calls = `{"role":"model","parts":[{"functionCall":{"id":"a","name":"lookup"}},{"functionCall":{"id":"b","name":"lookup"}}]}`
	for _, tc := range []struct{ name, model, responses string }{
		{"ambiguous-name", calls, `{"functionResponse":{"name":"lookup"}}`},
		{"unknown-id", calls, `{"functionResponse":{"id":"c","name":"lookup"}}`},
		{"wrong-name", calls, `{"functionResponse":{"id":"a","name":"other"}}`},
		{"repeated-response", calls, `{"functionResponse":{"id":"a","name":"lookup"}},{"functionResponse":{"id":"a","name":"lookup"}}`},
		{"duplicate-call", strings.Replace(calls, `"id":"b"`, `"id":"a"`, 1), `{"functionResponse":{"id":"a","name":"lookup"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire, err := GeminiGenerateContentToChatCompletions("model", []byte(`{"contents":[`+tc.model+`,{"role":"user","parts":[`+tc.responses+`]}]}`), false)
			require.Error(t, err)
			require.Empty(t, wire)
		})
	}
}

func TestGeminiToolIDsDoNotCollideWithLegacyIDs(t *testing.T) {
	body := []byte(`{"contents":[{"role":"model","parts":[{"functionCall":{"name":"lookup"}},{"functionCall":{"id":"call_lookup_0","name":"lookup"}}]},
		{"role":"user","parts":[{"functionResponse":{"id":"call_lookup_0","name":"lookup","response":{"output":"explicit"}}},{"functionResponse":{"name":"lookup","response":{"output":"legacy"}}}]}]}`)
	wire, err := GeminiGenerateContentToChatCompletions("model", body, false)
	require.NoError(t, err)
	messages := gjson.GetBytes(wire, "messages").Array()
	require.Len(t, messages, 3)
	legacy := messages[0].Get("tool_calls.0.id").String()
	explicit := messages[0].Get("tool_calls.1.id").String()
	require.NotEqual(t, legacy, explicit)
	require.Equal(t, explicit, messages[1].Get("tool_call_id").String())
	require.Equal(t, legacy, messages[2].Get("tool_call_id").String())
}

func TestGeminiMixedContentKeepsPartOrder(t *testing.T) {
	body := []byte(`{"contents":[{"parts":[{"text":"above"},{"inlineData":{"mimeType":"image/png","data":"cGlj"}},{"text":"below"},{"fileData":{"mimeType":"image/png","fileUri":"https://example.org/file.png"}},{"text":"last"}]},{"parts":[{"text":"pure"},{"text":"text"}]}]}`)
	wire, err := GeminiGenerateContentToChatCompletions("model", body, false)
	require.NoError(t, err)
	messages := gjson.GetBytes(wire, "messages").Array()
	require.Len(t, messages, 2)
	parts := messages[0].Get("content").Array()
	require.Len(t, parts, 5)
	require.Equal(t, "above", parts[0].Get("text").String())
	require.Equal(t, "data:image/png;base64,cGlj", parts[1].Get("image_url.url").String())
	require.Equal(t, "below", parts[2].Get("text").String())
	require.Equal(t, "https://example.org/file.png", parts[3].Get("image_url.url").String())
	require.Equal(t, "last", parts[4].Get("text").String())
	require.Equal(t, "pure\ntext", messages[1].Get("content").String())
}
