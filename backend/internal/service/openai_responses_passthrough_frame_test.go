package service

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResponsesPassthroughObservesWholeFrame(t *testing.T) {
	const text = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n"
	const terminal = "id: evt-3\ndata: {\"response\":{\"id\":\"resp_parts\",\n: between fields\ndata: \"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\nevent: response.completed\n\n"
	wire := text + terminal
	result, rec, writer, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(wire)), -1)
	require.NoError(t, err)
	require.Equal(t, wire, rec.Body.String())
	require.Equal(t, []int{len(text), len(wire)}, writer.flushBodyLengths)
	require.Equal(t, "resp_parts", result.responseID)
	require.Equal(t, 3, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)

	errRead := errors.New("truncated response")
	result, rec, _, err = runPassthroughFlushTest(t, &passthroughFlushTestErrorBody{payload: []byte(strings.TrimSuffix(wire, "\n\n")), err: errRead}, -1)
	require.ErrorIs(t, err, errRead)
	require.Equal(t, text, rec.Body.String())
	require.Equal(t, 3, result.usage.InputTokens)
	require.Equal(t, 4, result.usage.OutputTokens)
}

func TestResponsesFrameLineEndingsAndAssemblyLimit(t *testing.T) {
	const limit = 1024 * 1024
	require.Equal(t, int64(9*limit), openAIResponsesFrameLimit(limit))
	data := `data: {"delta":"` + strings.Repeat("x", limit-19) + `"}`
	require.Less(t, len(data), limit)
	wire := "event: response.output_text.delta\rid: 7\r\n" + data + "\n\r"
	scanner := bufio.NewScanner(strings.NewReader(wire))
	scanner.Split(newOpenAIResponsesLineSplit())
	scanner.Buffer(make([]byte, 4096), limit)
	reader := openAIResponsesFrameReader{limit: openAIResponsesFrameLimit(limit)}
	seen := 0
	for scanner.Scan() {
		err := reader.push(scanner.Text(), func(f *openAIResponsesFrame) {
			seen++
			require.Equal(t, "response.output_text.delta", f.eventType)
			require.True(t, f.delimited)
			require.Equal(t, strings.TrimPrefix(data, "data: "), f.payload)
		})
		require.NoError(t, err)
	}
	require.NoError(t, scanner.Err())
	require.Equal(t, 1, seen)
}
