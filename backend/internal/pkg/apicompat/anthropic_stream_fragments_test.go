package apicompat

import (
	"strings"
	"testing"
)

func TestAnthropicStreamFragmentSnapshots(t *testing.T) {
	state := NewAnthropicEventToResponsesState()
	state.PreserveThinkingSignatures = true
	push := func(e AnthropicStreamEvent) []ResponsesStreamEvent { return AnthropicEventToResponsesEvents(&e, state) }
	start := func(b AnthropicContentBlock) {
		push(AnthropicStreamEvent{Type: "content_block_start", ContentBlock: &b})
	}
	stop := func() { push(AnthropicStreamEvent{Type: "content_block_stop"}) }
	delta := func(d AnthropicDelta) []ResponsesStreamEvent {
		return push(AnthropicStreamEvent{Type: "content_block_delta", Delta: &d})
	}
	start(AnthropicContentBlock{Type: "text"})
	delta(AnthropicDelta{Type: "text_delta", Text: "one"})
	first := state.TextAccum
	delta(AnthropicDelta{Type: "text_delta", Text: " two"})
	stop()
	start(AnthropicContentBlock{Type: "text"})
	delta(AnthropicDelta{Type: "text_delta", Text: "three"})
	stop()
	start(AnthropicContentBlock{Type: "thinking", Thinking: "reason:", Signature: "s:"})
	delta(AnthropicDelta{Type: "thinking_delta", Thinking: "a"})
	if events := delta(AnthropicDelta{Type: "signature_delta", Signature: "b"}); len(events) != 0 {
		t.Fatal("signature was published as a delta")
	}
	stop()
	start(AnthropicContentBlock{Type: "thinking"})
	delta(AnthropicDelta{Type: "thinking_delta", Thinking: "new"})
	stop()
	msg := state.Outputs[0]
	if first != "one" || len(msg.Content) != 2 || msg.Content[0].Text != "one two" || msg.Content[1].Text != "three" {
		t.Fatalf("text part or snapshot changed: %q, %+v", first, msg)
	}
	sealed := encodeAnthropicThinking(AnthropicContentBlock{Type: "thinking", Thinking: "reason:a", Signature: "s:b"})
	if state.Outputs[1].EncryptedContent != sealed || state.Outputs[1].Summary[0].Text != "reason:a" || state.Outputs[2].Summary[0].Text != "new" || state.Outputs[2].EncryptedContent != "" {
		t.Fatal("thinking or signature crossed a block boundary")
	}
}

func BenchmarkAnthropicStreamFragments(b *testing.B) {
	for _, field := range []string{"text", "thinking", "signature", "input_json"} {
		b.Run(field, func(b *testing.B) {
			kind := field
			value := strings.Repeat("x", 64<<10)
			switch field {
			case "signature":
				kind = "thinking"
			case "input_json":
				kind = "tool_use"
				value = `{"x":"` + value + `","n":9007199254740993}`
			}
			start := AnthropicStreamEvent{Type: "content_block_start", ContentBlock: &AnthropicContentBlock{Type: kind, ID: "call_1", Name: "lookup"}}
			stop := AnthropicStreamEvent{Type: "content_block_stop"}
			var deltas []AnthropicStreamEvent
			for i := 0; i < len(value); i += 64 {
				part := value[i:min(i+64, len(value))]
				d := &AnthropicDelta{Type: field + "_delta", Text: part, Thinking: part, Signature: part, PartialJSON: part}
				deltas = append(deltas, AnthropicStreamEvent{Type: "content_block_delta", Delta: d})
			}
			b.ReportAllocs()
			for b.Loop() {
				s := NewAnthropicEventToResponsesState()
				s.PreserveThinkingSignatures = true
				AnthropicEventToResponsesEvents(&start, s)
				for i := range deltas {
					AnthropicEventToResponsesEvents(&deltas[i], s)
				}
				if field == "text" && s.TextAccum != value || field == "thinking" && s.CurrentSummary != value || field == "signature" && s.CurrentThinking.Signature != value || field == "input_json" && s.CurrentArgs != value {
					b.Fatal("incorrect accumulated value")
				}
				AnthropicEventToResponsesEvents(&stop, s)
			}
		})
	}
}
