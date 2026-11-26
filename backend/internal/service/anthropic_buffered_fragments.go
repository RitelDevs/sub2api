package service

import (
	"bytes"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
)

// Builders are keyed by the upstream block index. Content may grow and move
// to another backing array while deltas for an earlier block are still read.
type anthropicBufferedFragments map[int]*anthropicBufferedBlock

type anthropicBufferedBlock struct {
	text, thinking, signature strings.Builder
	startedInput              bool
}

func (f *anthropicBufferedFragments) apply(content []apicompat.AnthropicContentBlock, index int, delta *apicompat.AnthropicDelta) {
	if index < 0 || index >= len(content) || delta == nil {
		return
	}
	block := &content[index]
	acc := (*f)[index]
	if acc == nil {
		if *f == nil {
			*f = make(anthropicBufferedFragments)
		}
		acc = &anthropicBufferedBlock{}
		(*f)[index] = acc
	}
	switch delta.Type {
	case "text_delta":
		block.Text = appendBufferedText(&acc.text, block.Text, delta.Text)
	case "thinking_delta":
		block.Thinking = appendBufferedText(&acc.thinking, block.Thinking, delta.Thinking)
	case "signature_delta":
		block.Signature = appendBufferedText(&acc.signature, block.Signature, delta.Signature)
	case "input_json_delta":
		if delta.PartialJSON == "" {
			return
		}
		if !acc.startedInput {
			// Inspect only the original input, never a streamed prefix which
			// might happen to be a complete {} before a later whitespace delta.
			seed := bytes.Trim(block.Input, " \r\t\n")
			if len(seed) >= 2 && seed[0] == '{' && seed[len(seed)-1] == '}' && len(bytes.Trim(seed[1:len(seed)-1], " \r\t\n")) == 0 {
				block.Input = nil
			} else {
				block.Input = bytes.Clone(block.Input)
			}
			acc.startedInput = true
		}
		block.Input = append(block.Input, delta.PartialJSON...)
	}
}

func appendBufferedText(b *strings.Builder, prefix, delta string) string {
	if b.Len() == 0 {
		b.WriteString(prefix)
	}
	b.WriteString(delta)
	return b.String()
}
