package requesttiming

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOutermostDeliveryMarksOnlyGeneratedClientEvents(t *testing.T) {
	start := time.Now()
	now := start
	c := newCollector(start, 0, func() time.Time { return now })
	ctx := With(context.Background(), c)
	DeliveredSSE(WithOuterDelivery(ctx), []byte("data: {\"choices\":[{\"delta\":{\"content\":\"staged\"}}]}\n\n"))
	Delivered(ctx, []byte(`{"type":"response.created"}`))
	require.Empty(t, c.snapshot().Events)
	now = start.Add(25 * time.Millisecond)
	Delivered(ctx, []byte(`{"type":"response.refusal.delta","delta":"declined"}`))
	now = start.Add(50 * time.Millisecond)
	DeliveredSSE(ctx, []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"functionCall\":{}},{\"text\":\"plan\",\"thought\":true}]}}]}\n\n"))
	now = start.Add(75 * time.Millisecond)
	Delivered(ctx, []byte(`{"error":{"code":502}}`))
	require.Equal(t, map[string]float64{
		"downstream_first_text_write": 25, "downstream_first_tool_write": 50,
		"downstream_first_reasoning_write": 50, "downstream_first_terminal_write": 75,
	}, c.snapshot().Events)
}
