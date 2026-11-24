package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiPreservesChatRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, content, reason, finish string
		parts                         []string
	}{
		{"only refusal", `null`, "stop", "STOP", []string{"Unavailable."}},
		{"with content", `"Partial. "`, "stop", "STOP", []string{"Partial. ", "Unavailable."}},
		{"length limit", `"Partial. "`, "length", "MAX_TOKENS", []string{"Partial. ", "Unavailable."}},
		{"explicit filter", `null`, "content_filter", "SAFETY", []string{"Unavailable."}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := json.Marshal(ChatCompletionsResponse{Choices: []ChatChoice{{
				Index: 1, Message: ChatMessage{Content: json.RawMessage(tc.content), Refusal: "Unavailable."}, FinishReason: tc.reason,
			}}})
			require.NoError(t, err)
			out, err := ChatCompletionsJSONToGemini(body, "fixture")
			require.NoError(t, err)
			candidate := gjson.GetBytes(out, "candidates.0")
			require.Equal(t, int64(1), candidate.Get("index").Int())
			require.Equal(t, tc.finish, candidate.Get("finishReason").String())
			var parts []string
			for _, part := range candidate.Get("content.parts").Array() {
				parts = append(parts, part.Get("text").String())
			}
			require.Equal(t, tc.parts, parts)
		})
	}
}

func TestGeminiPreservesChatRefusalDeltas(t *testing.T) {
	tr := NewGeminiSSETranslator("fixture")
	for _, step := range []struct {
		wire, finish string
		text         []string
	}{
		{`{"choices":[{"index":1,"delta":{"refusal":"First "}}]}`, "", []string{"First "}},
		{`{"choices":[{"index":1,"delta":{"content":"body ","refusal":"then refusal"}}]}`, "", []string{"body ", "then refusal"}},
		{`{"choices":[{"index":1,"delta":{"refusal":"."},"finish_reason":"stop"}]}`, "STOP", []string{"."}},
		{`[DONE]`, "", nil},
	} {
		out := tr.Push([]byte("data: " + step.wire + "\n\n"))
		require.NoError(t, tr.Err())
		var got []string
		var terminals []string
		for _, payload := range sseDataPayloads(string(out)) {
			candidate := gjson.Get(payload, "candidates.0")
			require.Equal(t, int64(1), candidate.Get("index").Int())
			for _, part := range candidate.Get("content.parts").Array() {
				if text := part.Get("text").String(); text != "" {
					got = append(got, text)
				}
			}
			if finish := candidate.Get("finishReason").String(); finish != "" {
				terminals = append(terminals, finish)
			}
		}
		require.Equal(t, step.text, got)
		if step.finish == "" {
			require.Empty(t, terminals)
		} else {
			require.Equal(t, []string{step.finish}, terminals)
		}
	}
	require.Empty(t, tr.Flush())
	require.NoError(t, tr.Err())
}
