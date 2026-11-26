package apicompat

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestChatTerminalSnapshotUsesDeliveredOutputIndices(t *testing.T) {
	events := collectStreamEvents(t, []string{
		`{"choices":[{"delta":{"tool_calls":[{"index":4,"id":"call-a","function":{"name":"read","arguments":"{}"}}]}}]}`,
		`{"choices":[{"delta":{"content":"working"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call-b","function":{"name":"read","arguments":"{}"}}]},"finish_reason":"length"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":4,"total_tokens":7}}`,
	})
	terminal := events[len(events)-1]
	require.Equal(t, "response.incomplete", terminal.Type)
	require.Equal(t, "incomplete", terminal.Response.Status)
	require.Equal(t, "max_output_tokens", terminal.Response.IncompleteDetails.Reason)
	require.EqualValues(t, 7, terminal.Response.Usage.TotalTokens)
	require.Len(t, terminal.Response.Output, 3)
	closed := map[int]bool{}
	for _, event := range events {
		if event.Type == "response.output_item.done" {
			require.False(t, closed[event.OutputIndex])
			closed[event.OutputIndex] = true
			require.Equal(t, *event.Item, terminal.Response.Output[event.OutputIndex])
		}
	}
	require.Len(t, closed, 3)
}
