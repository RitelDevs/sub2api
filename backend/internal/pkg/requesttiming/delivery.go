package requesttiming

import (
	"bytes"
	"context"

	"github.com/tidwall/gjson"
)

type outerDeliveryKey struct{}

// WithOuterDelivery leaves the outermost protocol writer responsible for
// identifying what was actually written to the downstream response.
func WithOuterDelivery(ctx context.Context) context.Context {
	return context.WithValue(ctx, outerDeliveryKey{}, true)
}

// DeliveredSSE accepts complete, locally encoded SSE records after a successful write.
// Network framing and upstream accounting are handled by the respective codecs.
func DeliveredSSE(ctx context.Context, wire []byte) {
	if From(ctx) == nil || ctx.Value(outerDeliveryKey{}) != nil {
		return
	}
	for line := range bytes.SplitSeq(wire, []byte{'\n'}) {
		if data, ok := bytes.CutPrefix(line, []byte("data:")); ok {
			observeDelivery(ctx, bytes.TrimSpace(data))
		}
	}
}

// Delivered accepts one application-generated JSON payload after a successful write.
func Delivered(ctx context.Context, data []byte) {
	if From(ctx) != nil && ctx.Value(outerDeliveryKey{}) == nil {
		observeDelivery(ctx, data)
	}
}

func observeDelivery(ctx context.Context, data []byte) {
	event := gjson.ParseBytes(data)
	switch event.Get("type").String() {
	case "response.output_text.delta", "response.refusal.delta":
		if event.Get("delta").String() != "" {
			Mark(ctx, "downstream_first_text_write")
		}
	case "response.function_call_arguments.delta", "response.custom_tool_call_input.delta":
		if event.Get("delta").String() != "" {
			Mark(ctx, "downstream_first_tool_write")
		}
	case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
		if event.Get("delta").String() != "" {
			Mark(ctx, "downstream_first_reasoning_write")
		}
	case "response.completed", "response.incomplete", "response.failed":
		Mark(ctx, "downstream_first_terminal_write")
	}
	if event.Get("error").IsObject() {
		Mark(ctx, "downstream_first_terminal_write")
	}
	for _, choice := range event.Get("choices").Array() {
		delta := choice.Get("delta")
		if delta.Get("content").String() != "" || delta.Get("refusal").String() != "" {
			Mark(ctx, "downstream_first_text_write")
		}
		if len(delta.Get("tool_calls").Array()) > 0 {
			Mark(ctx, "downstream_first_tool_write")
		}
		if delta.Get("reasoning_content").String() != "" {
			Mark(ctx, "downstream_first_reasoning_write")
		}
		if choice.Get("finish_reason").String() != "" {
			Mark(ctx, "downstream_first_terminal_write")
		}
	}
	for _, candidate := range event.Get("candidates").Array() {
		for _, part := range candidate.Get("content.parts").Array() {
			if part.Get("functionCall").IsObject() {
				Mark(ctx, "downstream_first_tool_write")
			}
			if part.Get("text").String() != "" {
				if part.Get("thought").Bool() {
					Mark(ctx, "downstream_first_reasoning_write")
				} else {
					Mark(ctx, "downstream_first_text_write")
				}
			}
		}
		if candidate.Get("finishReason").String() != "" {
			Mark(ctx, "downstream_first_terminal_write")
		}
	}
}
