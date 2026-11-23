package requesttiming

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDeliveredEventsAreDistinctAndWriteScoped(t *testing.T) {
	start := time.Now()
	now := start
	c := newCollector(start, 0, func() time.Time { return now })
	ctx := With(context.Background(), c)
	DeliveredSSE(WithOuterDelivery(ctx), []byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0}]}}]}\n\n"))
	require.Empty(t, c.snapshot().Events)
	DeliveredSSE(ctx, []byte(": heartbeat\n\ndata: {\"type\":\"response.created\"}\n\n"))
	require.Empty(t, c.snapshot().Events)
	now = start.Add(200 * time.Millisecond)
	Delivered(ctx, []byte(`{"choices":[{"delta":{"content":"visible"}}]}`))
	now = start.Add(300 * time.Millisecond)
	Delivered(ctx, []byte(`{"type":"response.function_call_arguments.delta","delta":"{"}`))
	now = start.Add(400 * time.Millisecond)
	Delivered(ctx, []byte(`{"candidates":[{"content":{"parts":[{"text":"thinking","thought":true}]}}]}`))
	now = start.Add(500 * time.Millisecond)
	Delivered(ctx, []byte(`{"candidates":[{"finishReason":"STOP","content":{"parts":[{"text":"more"}]}}]}`))
	require.Equal(t, map[string]float64{
		"downstream_first_text_write":      200,
		"downstream_first_tool_write":      300,
		"downstream_first_reasoning_write": 400,
		"downstream_first_terminal_write":  500,
	}, c.snapshot().Events)
}
