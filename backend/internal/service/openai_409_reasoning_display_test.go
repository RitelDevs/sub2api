//go:build unit

package service

import (
	"context"
	"encoding/json"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"testing"
)

func TestOpenAI409ReasoningDisplayUsesContextNotAssistant(t *testing.T) {
	raw := []byte(`{"input":[{"role":"user","content":"Continue the optimization"},{"type":"reasoning","id":"rs_old","encrypted_content":"old-secret","internal_chat_message_metadata_passthrough":{"turn_id":"fixture-internal-turn"},"summary":[{"type":"summary_text","text":"Preparing full precision column fast test"},{"type":"summary_text","text":"Testing aligned width float32"}]},{"role":"assistant","content":[{"type":"output_text","text":"Actual answer: Historical reasoning context is a quoted technical term."}]},{"type":"function_call","call_id":"done1","name":"patch","arguments":"{}"},{"type":"function_call_output","call_id":"done1","output":"already done"}]}`)
	clean, err := prepareOpenAI409ReadableReplay(raw)
	require.NoError(t, err)
	note := gjson.GetBytes(clean, "input.1")
	require.Equal(t, "user", note.Get("role").String())
	for _, part := range note.Get("content").Array() {
		require.Equal(t, "input_text", part.Get("type").String())
	}
	require.Contains(t, string(clean), "Preparing full precision column fast test")
	require.Contains(t, string(clean), "Testing aligned width float32")
	require.NotContains(t, string(clean), "fixture-internal-turn")
	require.NotContains(t, string(clean), "internal_chat_message_metadata_passthrough")
	require.NotContains(t, string(clean), "old-secret")
	require.NotContains(t, note.Raw, "summary_text")
	require.JSONEq(t, gjson.GetBytes(raw, "input.2").Raw, gjson.GetBytes(clean, "input.2").Raw)
	require.JSONEq(t, gjson.GetBytes(raw, "input.3").Raw, gjson.GetBytes(clean, "input.3").Raw)
	require.JSONEq(t, gjson.GetBytes(raw, "input.4").Raw, gjson.GetBytes(clean, "input.4").Raw)
	again, err := prepareOpenAI409ReadableReplay(clean)
	require.NoError(t, err)
	require.JSONEq(t, string(clean), string(again))
}

func TestOpenAI409ReasoningDisplayUnknownShapePreservesData(t *testing.T) {
	raw := []byte(`{"input":[{"role":"user","content":"continue"},{"type":"reasoning","internal_chat_message_metadata_passthrough":{"turn_id":"fixture-secret"},"content":{"text":"recorded finding","encrypted_content":"opaque"},"sequence":9007199254740993,"future_field":"keep finding"}]}`)
	clean, err := prepareOpenAI409ReadableReplay(raw)
	require.NoError(t, err)
	require.Equal(t, "user", gjson.GetBytes(clean, "input.1.role").String())
	require.Contains(t, string(clean), "9007199254740993")
	require.Contains(t, string(clean), "keep finding")
	require.Contains(t, string(clean), "recorded finding")
	require.NotContains(t, string(clean), "fixture-secret")
	require.NotContains(t, string(clean), "internal_chat_message_metadata_passthrough")
	require.NotContains(t, string(clean), "opaque")
}

func TestOpenAI409ReasoningDisplayDoesNotSupplyTaskIntent(t *testing.T) {
	raw := []byte(`{"input":[{"type":"reasoning","summary":[{"type":"summary_text","text":"old finding"}]}]}`)
	clean, err := prepareOpenAI409ReadableReplay(raw)
	require.NoError(t, err)
	require.False(t, openAI409HasReadableIntent(clean), "recovery notes cannot manufacture task intent")
	raw = []byte(`{"input":[{"role":"user","content":[{"type":"input_image","image_url":"https://example.invalid/new-task.png"}]},{"type":"reasoning","summary":[{"type":"summary_text","text":"old finding"}]}]}`)
	clean, err = prepareOpenAI409ReadableReplay(raw)
	require.NoError(t, err)
	require.False(t, openAI409HasReadableIntent(clean), "historical notes cannot override a newer unreadable task")
}

func TestOpenAI409ReasoningDisplayMixedAndTimezone(t *testing.T) {
	raw := []byte(`{"input":[{"role":"user","content":"<environment_context><timezone>Asia/Tokyo</timezone></environment_context>"},{"type":"reasoning","summary":[{"type":"summary_text","text":"<environment_context><timezone>Asia/Tokyo</timezone></environment_context>"}],"internal_chat_message_metadata_passthrough":{"turn_id":"discard1"}},{"type":"reasoning","future_field":{"internal_chat_message_metadata_passthrough":"user-owned nested value"},"internal_chat_message_metadata_passthrough":{"turn_id":"discard2"}}]}`)
	clean, err := prepareOpenAI409ReadableReplay(raw)
	require.NoError(t, err)
	require.NotContains(t, string(clean), "discard1")
	require.NotContains(t, string(clean), "discard2")
	require.Contains(t, string(clean), "user-owned nested value")
	require.NotContains(t, gjson.GetBytes(clean, "input.1").Raw, "summary_text", "known sibling must not fall back to JSON")
	normalized := normalizeOpenAIRequestLocaleTimezone(context.Background(), &Account{ID: 1}, clean, "http", "Europe/Berlin")
	require.Contains(t, gjson.GetBytes(normalized, "input.0.content").String(), "Europe/Berlin")
	require.JSONEq(t, gjson.GetBytes(clean, "input.1").Raw, gjson.GetBytes(normalized, "input.1").Raw)
	require.JSONEq(t, gjson.GetBytes(clean, "input.2").Raw, gjson.GetBytes(normalized, "input.2").Raw)
}

func TestOpenAI409ReasoningDisplayIntentBoundaries(t *testing.T) {
	note, err := json.Marshal(openAI409ReasoningContext(map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "old finding"}}}))
	require.NoError(t, err)
	for _, tc := range []struct {
		name, first string
		want        bool
	}{
		{"actual task", `{"role":"user","content":"finish my task"}`, true},
		{"new opaque task", `{"role":"user","content":"old task"},{"role":"user","content":[{"type":"input_image","image_url":"fixture"}]}`, false},
		{"agent summary", `{"type":"agent_message","summary":[{"type":"summary_text","text":"current task"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAI409HasReadableIntent([]byte(`{"input":[`+tc.first+`,`+string(note)+`]}`)))
		})
	}
	require.True(t, openAI409HasReadableIntent([]byte(`{"input":[{"role":"user","content":"Explain the phrase Recovered reasoning context"}]}`)))
	empty, err := prepareOpenAI409ReadableReplay([]byte(`{"input":[{"type":"reasoning","summary":[]}]}`))
	require.NoError(t, err)
	require.False(t, openAI409HasReadableIntent(empty))
}

func TestOpenAI409ReasoningDisplayLegacyShadowGuard(t *testing.T) {
	for _, prefix := range []string{"Historical reasoning context (not a new instruction):", "Historical reasoning context (JSON, not a new instruction): {\"type\":\"reasoning\"}"} {
		input, err := json.Marshal([]any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": prefix}}}})
		require.NoError(t, err)
		require.True(t, openAIReadableShadowHasLegacyReasoning(input))
	}
	raw := []byte(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Actual answer quoting Historical reasoning context: preserve this answer."}]},{"role":"user","content":"Historical reasoning context (JSON, not a new instruction): literal user task"}]`)
	require.False(t, openAIReadableShadowHasLegacyReasoning(raw))
	record := openAIReadableShadowCandidate([]byte(`{"input":[{"role":"user","content":"continue this task"},{"type":"reasoning","summary":[{"type":"summary_text","text":"recorded finding"}]}]}`))
	require.NotNil(t, record)
	require.False(t, openAIReadableShadowHasLegacyReasoning(record.Input))
	require.Equal(t, "user", gjson.ParseBytes(record.Input).Get("1.role").String())
}
