package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

func TestAnthropicBufferedFragments(t *testing.T) {
	seed := json.RawMessage(" {\t }\n")
	blocks := []apicompat.AnthropicContentBlock{{Text: "a", Input: seed}, {Thinking: "b", Signature: "s"}}
	var fragments anthropicBufferedFragments
	apply := func(index int, delta apicompat.AnthropicDelta) { fragments.apply(blocks, index, &delta) }
	apply(-1, apicompat.AnthropicDelta{Type: "text_delta", Text: "ignored"})
	apply(2, apicompat.AnthropicDelta{Type: "text_delta", Text: "ignored"})
	apply(0, apicompat.AnthropicDelta{Type: "input_json_delta"})
	if string(blocks[0].Input) != string(seed) {
		t.Fatal("an empty fragment changed the initial input")
	}
	apply(0, apicompat.AnthropicDelta{Type: "text_delta", Text: "1"})
	first := blocks[0].Text
	apply(1, apicompat.AnthropicDelta{Type: "thinking_delta", Thinking: "2"})
	apply(1, apicompat.AnthropicDelta{Type: "signature_delta", Signature: "3"})
	blocks = append(blocks, apicompat.AnthropicContentBlock{Input: json.RawMessage(`{"seed":true}`)})
	apply(0, apicompat.AnthropicDelta{Type: "input_json_delta", PartialJSON: "{}"})
	apply(0, apicompat.AnthropicDelta{Type: "input_json_delta", PartialJSON: "\n"})
	apply(2, apicompat.AnthropicDelta{Type: "input_json_delta", PartialJSON: "\t"})
	apply(0, apicompat.AnthropicDelta{Type: "text_delta", Text: "4"})
	if first != "a1" || blocks[0].Text != "a14" || blocks[1].Thinking != "b2" || blocks[1].Signature != "s3" {
		t.Fatalf("incorrect accumulated blocks: %+v, snapshot %q", blocks, first)
	}
	if string(seed) != " {\t }\n" || string(blocks[0].Input) != "{}\n" || string(blocks[2].Input) != "{\"seed\":true}\t" {
		t.Fatalf("incorrect seed or raw JSON fragments: %q / %q / %q", seed, blocks[0].Input, blocks[2].Input)
	}
}

func BenchmarkAnthropicBufferedFragments(b *testing.B) {
	for _, field := range []string{"text", "thinking", "signature", "input_json"} {
		b.Run(field, func(b *testing.B) {
			value := strings.Repeat("x", 64<<10)
			if field == "input_json" {
				value = `{"x":"` + value + `","n":9007199254740993}`
			}
			var deltas []apicompat.AnthropicDelta
			for i := 0; i < len(value); i += 64 {
				part := value[i:min(i+64, len(value))]
				deltas = append(deltas, apicompat.AnthropicDelta{Type: field + "_delta", Text: part, Thinking: part, Signature: part, PartialJSON: part})
			}
			b.ReportAllocs()
			for b.Loop() {
				var fragments anthropicBufferedFragments
				blocks := []apicompat.AnthropicContentBlock{{Input: json.RawMessage("{}")}}
				for i := range deltas {
					fragments.apply(blocks, 0, &deltas[i])
				}
				blk := &blocks[0]
				if field == "text" && blk.Text != value || field == "thinking" && blk.Thinking != value || field == "signature" && blk.Signature != value || field == "input_json" && string(blk.Input) != value {
					b.Fatal("incorrect accumulated value")
				}
			}
		})
	}
}
