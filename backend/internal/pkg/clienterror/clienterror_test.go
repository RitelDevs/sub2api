package clienterror

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestMessageRemovesRepeatedRequestIDAnnotations(t *testing.T) {
	message := "Context window is full. Reduce conversation history, system prompt, or tools."
	for _, suffix := range []string{
		" (request id: 20261110000000000000000000000000TESTA) (request id: 20261110000000000000000000000000TESTB)",
		" (REQUEST_ID=req_123) [Request-ID: req_456]",
		"（请求 ID：req_123）【上游请求编号：req_456】",
		" (RequestID: req_123) (upstream_request_id=req_456)",
		" Request ID: req_123",
		" 请求标识符：req_123",
	} {
		t.Run(suffix, func(t *testing.T) {
			require.Equal(t, message, Message(message+suffix))
		})
	}
	require.Equal(t, "请求 ID is required", Message("请求 ID is required"))
	require.Equal(t, "request_id is required", Message("request_id is required"))
	require.Equal(t, "  ordinary error  ", Message("  ordinary error  "))
	require.Equal(t, "Request failed", Message("(request id: secret)"))
}

func TestErrorJSONCleansKnownHTTPErrorWithoutDiscriminator(t *testing.T) {
	payload := []byte(`{ "message":"Context window is full. Reduce conversation history, system prompt, or tools. (request id: 20261110000000000000000000000000TESTA) (request id: 20261110000000000000000000000000TESTB)","request_id":"secret","output":[{"text":"(request id: user-text)"}] }`)
	clean, changed := ErrorJSON(payload)
	require.True(t, changed)
	require.Equal(t, "Context window is full. Reduce conversation history, system prompt, or tools.", gjson.GetBytes(clean, "message").String())
	require.False(t, gjson.GetBytes(clean, "request_id").Exists())
	require.Equal(t, gjson.GetBytes(payload, "output").Raw, gjson.GetBytes(clean, "output").Raw)
	clean, changed = ErrorJSON([]byte(`{"message":"(request id: secret)","request_id":"secret"}`))
	require.True(t, changed)
	require.Equal(t, "Request failed", gjson.GetBytes(clean, "message").String())
	clean, changed = ErrorJSON([]byte(`"Failed request_id=secret"`))
	require.True(t, changed)
	require.Equal(t, `"Failed"`, string(clean))
}

func TestJSONCleansErrorFieldsWithoutChangingIdentityOrOutput(t *testing.T) {
	payload := []byte(`{ "type":"response.failed", "sequence_number":9007199254740993, "event_id":"evt_keep", "request_id":"outer", "response":{"id":"resp_keep", "status":"failed", "requestId":"response-secret", "output":[{"type":"function_call","arguments":"request id: user-value","request_id":"tool-value"},{"type":"message","content":[{"text":"(request id: quoted-user-text)"}]}], "tools":[{"parameters":{"request_id":{"type":"string"}}}], "error":{"code":"context_length_exceeded","type":"invalid_request_error","param":"input[1]","message":"Context window is full. (request id: secret-a) (request id: secret-b)","upstream_request_id":"secret-c","details":[{"x-request-id":"secret-d","message":"Nested failure [Request-ID: secret-e]"}]}}}`)
	clean, changed := JSON(payload)
	require.True(t, changed)
	require.True(t, gjson.ValidBytes(clean))
	require.Equal(t, "Context window is full.", gjson.GetBytes(clean, "response.error.message").String())
	require.Equal(t, "Nested failure", gjson.GetBytes(clean, "response.error.details.0.message").String())
	for _, path := range []string{"request_id", "response.requestId", "response.error.upstream_request_id", "response.error.details.0.x-request-id"} {
		require.False(t, gjson.GetBytes(clean, path).Exists(), path)
	}
	for _, path := range []string{"event_id", "response.id", "response.error.code", "response.error.type", "response.error.param", "response.output", "response.tools"} {
		require.Equal(t, gjson.GetBytes(payload, path).Raw, gjson.GetBytes(clean, path).Raw, path)
	}
	require.Contains(t, string(clean), `"sequence_number":9007199254740993`)
	require.Contains(t, string(clean), `{ "type":"response.failed"`)
	require.Contains(t, string(payload), "secret-a", "original upstream evidence must remain intact")
	second, changed := JSON(clean)
	require.False(t, changed)
	require.True(t, bytes.Equal(clean, second))
}

func TestJSONRecognizesOnlyErrorEnvelopes(t *testing.T) {
	for _, raw := range []string{
		`{"type":"error","message":"Failed (request id: secret)"}`,
		`{"error":{"message":"Failed (request id: secret)","request_id":"secret"}}`,
		`{"status":"failed","message":"Failed (request id: secret)"}`,
		`{"response":{"status":"failed","error":{"message":"Failed (request id: secret)"}}}`,
		`{"error":"Failed (request id: secret)"}`,
	} {
		clean, changed := JSON([]byte(raw))
		require.True(t, changed, raw)
		require.NotContains(t, string(clean), "secret")
	}
	for _, raw := range []string{
		`{ "type":"response.completed", "response":{"id":"resp_keep", "error":null, "output":[{"text":"(request id: keep)"}],"metadata":{"request_id":"keep"}} }`,
		`{"object":"chat.completion","choices":[{"message":{"content":"request id: keep"}}],"request_id":"keep"}`,
		`{"error":null,"message":"request id: keep"}`,
		`{"message":"request id: keep","request_id":"keep"}`,
		`[ {"type":"error","request_id":"keep"} ]`,
		`invalid JSON (request id: keep)`,
	} {
		payload := []byte(raw)
		clean, changed := JSON(payload)
		require.False(t, changed, raw)
		require.Equal(t, payload, clean)
		require.Same(t, &payload[0], &clean[0], "unchanged payload should retain its original buffer")
	}
}

func TestJSONCleansJSONEncodedErrorMessage(t *testing.T) {
	payload := []byte(`{"error":{"message":"{\"error\":{\"message\":\"Inner failure (request id: secret-a)\",\"request_id\":\"secret-b\"},\"requestId\":\"secret-c\"}"}}`)
	clean, changed := JSON(payload)
	require.True(t, changed)
	require.NotContains(t, string(clean), "secret")
	inner := gjson.GetBytes(clean, "error.message").String()
	require.True(t, gjson.Valid(inner))
	require.Equal(t, "Inner failure", gjson.Get(inner, "error.message").String())
	require.Equal(t, `{"message":"Failed"}`, Message(`{"message":"Failed (request id: secret)","request_id":"secret"}`))
	withOutput := `{"error":{"message":"Failure"},"output":[{"text":"(request id: quoted-user-text)"}]}`
	require.Equal(t, withOutput, Message(withOutput))
	clean, changed = JSON([]byte(`{"error":{"details":["Failure (request id: secret)"]}}`))
	require.True(t, changed)
	require.Equal(t, "Failure", gjson.GetBytes(clean, "error.details.0").String())
}

func TestJSONHandlesEscapedDiagnosticKeys(t *testing.T) {
	clean, changed := JSON([]byte(`{"type":"error","error":{"meta.data":{"request_id":"secret","message":"Failed (request id: secret)"},"a:b":{"RequestID":"secret"}}}`))
	require.True(t, changed)
	require.NotContains(t, string(clean), "secret")
	require.Equal(t, "Failed", gjson.GetBytes(clean, `error.meta\.data.message`).String())
}
