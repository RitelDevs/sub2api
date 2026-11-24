package service

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIPassthroughCompleteNativeEvent(t *testing.T) {
	const text = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"text\"}\n\n"
	for _, kind := range []string{"response.completed", "response.incomplete"} {
		for _, cut := range []string{`"response":`, `"usage":`, `"input_tokens":`, `"output_tokens":`} {
			t.Run(kind+"/"+cut, func(t *testing.T) {
				data := `{"response":{"id":"resp_parts","usage":{"input_tokens":3,"output_tokens":4}}}`
				data = strings.Replace(data, cut, "\n: metadata between fragments\ndata:"+cut, 1)
				terminal := "id: upstream-event\ndata:" + data + "\nevent: " + kind + "\n\n"
				wire := text + terminal
				res, w, f, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(wire)), -1)
				require.NoError(t, err)
				require.Equal(t, wire, w.Body.String())
				require.Equal(t, []int{len(text), len(wire)}, f.flushBodyLengths)
				require.Equal(t, "resp_parts", res.responseID)
				require.Equal(t, 3, res.usage.InputTokens)
				require.Equal(t, 4, res.usage.OutputTokens)
			})
		}
	}
}

func TestOpenAIPassthroughMultilineFailedReplayEligibility(t *testing.T) {
	const text = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"text\"}\n\n"
	const failure = "event: response.failed\ndata: {\"type\":\"response.failed\",\ndata: \"response\":{\"id\":\"resp_parts\",\"error\":{\"code\":\"server_error\",\"message\":\"upstream processing failed\"},\n: middle\ndata: \"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}\n\n"
	for _, prefix := range []string{"", text} {
		res, w, _, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(prefix+failure)), -1)
		require.Error(t, err)
		var retry *UpstreamFailoverError
		require.Equal(t, prefix == "", errors.As(err, &retry))
		require.Equal(t, 3, res.usage.InputTokens)
		require.Equal(t, 4, res.usage.OutputTokens)
		if prefix == "" {
			require.Empty(t, w.Body.String())
		} else {
			require.Contains(t, w.Body.String(), text)
			require.Contains(t, w.Body.String(), "response.failed")
		}
	}
}

func TestOpenAIPassthroughMultilineTerminalReadError(t *testing.T) {
	const text = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"text\"}\n\n"
	const final = "event: response.completed\ndata: {\"type\":\"response.completed\",\ndata: \"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":4}}}"
	errRead := errors.New("truncated transfer")
	_, _, _, err := runPassthroughFlushTest(t, &passthroughFlushTestErrorBody{payload: []byte(text + final), err: errRead}, -1)
	require.ErrorIs(t, err, errRead)
	res, rec, _, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(text+final)), -1)
	require.NoError(t, err)
	require.Equal(t, text+final+"\n", rec.Body.String())
	require.Equal(t, 4, res.usage.OutputTokens)
}

func TestOpenAIPassthroughAdjacentDoneAndBrokenFailure(t *testing.T) {
	const text = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"ready\"}\n"
	complete := text + "data: [DONE]\n\n"
	_, rec, _, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(complete)), -1)
	require.NoError(t, err)
	require.Equal(t, complete, rec.Body.String())
	for _, failure := range []string{"error", "response.failed"} {
		wire := text + "\nevent: " + failure + "\ndata: {\n\ndata: [DONE]\n\n"
		_, rec, _, err = runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(wire)), -1)
		require.Error(t, err)
		var retry *UpstreamFailoverError
		require.NotErrorAs(t, err, &retry)
		require.Contains(t, rec.Body.String(), text)
		require.Equal(t, 1, strings.Count(rec.Body.String(), `"type":"response.failed"`))
		require.NotContains(t, rec.Body.String(), "[DONE]")
	}
}

func TestOpenAIPassthroughPluginLimitStopsUnfinishedEvent(t *testing.T) {
	var frame openAIPassthroughEvent
	read := 0
	for frame.next(func() (string, bool) { read++; return ":x", true }, 1024, 12, true) {
		t.Fatal("unfinished event became eligible")
	}
	require.ErrorIs(t, frame.err, errPassthroughPluginEventTooLarge)
	require.Equal(t, 5, read)
}

func TestOpenAIPassthroughOpaqueExtensionBeforeFailure(t *testing.T) {
	const extension = "event: vendor.note\nid: upstream\ndata: opaque text\n\n"
	const failure = "data: {\"type\":\"response.failed\",\"error\":{\"code\":\"server_error\",\"message\":\"upstream processing failed\"}}\n\n"
	_, rec, _, err := runPassthroughFlushTest(t, io.NopCloser(strings.NewReader(extension+failure)), -1)
	require.Error(t, err)
	var retry *UpstreamFailoverError
	require.NotErrorAs(t, err, &retry)
	require.True(t, strings.HasPrefix(rec.Body.String(), extension))
}
