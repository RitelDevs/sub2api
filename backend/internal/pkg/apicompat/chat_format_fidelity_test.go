package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiSchemaAndDocumentReachAnthropicRequest(t *testing.T) {
	const schema = `{"type":"object","properties":{"receipt_number":{"type":"integer","minimum":9007199254740993}},"required":["receipt_number"],"additionalProperties":false}`
	const input = `{"contents":[{"role":"user","parts":[{"text":"Read the PDF"},{"inlineData":{"mimeType":"application/pdf","displayName":"receipt.pdf","data":"JVBERi0xLjQ="}},{"text":"Extract receipt number"}]}],"generationConfig":{"responseMimeType":"application/json","responseJsonSchema":` + schema + `}}`
	wire, err := GeminiGenerateContentToChatCompletions("claude-sonnet-4-5", []byte(input), false)
	require.NoError(t, err)
	require.Equal(t, "receipt.pdf", gjson.GetBytes(wire, "messages.0.content.1.file.filename").String())
	var chat ChatCompletionsRequest
	require.NoError(t, json.Unmarshal(wire, &chat))
	responses, err := ChatCompletionsToResponses(&chat)
	require.NoError(t, err)
	responses.Reasoning = &ResponsesReasoning{Effort: "medium"}
	up, err := ResponsesToAnthropicRequest(responses)
	require.NoError(t, err)
	wire, err = json.Marshal(up)
	require.NoError(t, err)
	require.Equal(t, schema, gjson.GetBytes(wire, "output_config.format.schema").Raw)
	require.Equal(t, "medium", gjson.GetBytes(wire, "output_config.effort").String())
	require.Equal(t, "document", gjson.GetBytes(wire, "messages.0.content.1.type").String())
	require.Equal(t, "JVBERi0xLjQ=", gjson.GetBytes(wire, "messages.0.content.1.source.data").String())
	require.Equal(t, "Extract receipt number", gjson.GetBytes(wire, "messages.0.content.2.text").String())
}

func TestResponsesImageAndRefusalHistory(t *testing.T) {
	const input = `[{"role":"user","content":[{"type":"input_image","image_url":"https://example.invalid/chart.png","detail":"high"}]},{"role":"assistant","content":[{"type":"refusal","refusal":"unavailable"}]}]`
	cc, err := ResponsesToChatCompletionsRequest(&ResponsesRequest{Input: json.RawMessage(input)})
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(cc.Messages[0].Content, "0.image_url.detail").String())
	require.Equal(t, "unavailable", cc.Messages[1].Refusal)
	out, err := ChatCompletionsToResponses(cc)
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(out.Input, "0.content.0.detail").String())
	require.Equal(t, "unavailable", gjson.GetBytes(out.Input, "1.content.0.refusal").String())
	cc, err = ResponsesToChatCompletionsRequest(&ResponsesRequest{Input: json.RawMessage(`[{"role":"user","content":[{"type":"input_text","text":"read chart"},{"type":"input_image","file_id":"file-chart"}]}]`)})
	require.Nil(t, cc)
	require.ErrorContains(t, err, "file_id")
}

func TestChatRefusalPartLifecycleOnWire(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("fixture")
	for _, step := range []struct {
		field, text string
		index       int
	}{{"refusal", "un", 0}, {"content", "body", 1}, {"refusal", "available", 0}} {
		var chunk ChatCompletionsChunk
		require.NoError(t, json.Unmarshal([]byte(`{"choices":[{"index":0,"delta":{"`+step.field+`":"`+step.text+`"}}]}`), &chunk))
		events := ChatCompletionsChunkToResponsesEvents(&chunk, state)
		wire, err := json.Marshal(events[len(events)-1])
		require.NoError(t, err)
		require.Equal(t, int64(step.index), gjson.GetBytes(wire, "content_index").Int())
		require.True(t, gjson.GetBytes(wire, "content_index").Exists())
		require.Equal(t, step.text, gjson.GetBytes(wire, "delta").String())
	}
	events := FinalizeChatCompletionsResponsesStream(state)
	var refusalDone, refusalPart bool
	for _, event := range events {
		wire, err := json.Marshal(event)
		require.NoError(t, err)
		switch event.Type {
		case "response.refusal.done":
			refusalDone = true
			require.Equal(t, "unavailable", gjson.GetBytes(wire, "refusal").String())
		case "response.content_part.done":
			if event.ContentIndex == 0 {
				refusalPart = true
				require.Equal(t, "unavailable", gjson.GetBytes(wire, "part.refusal").String())
			}
		}
	}
	require.True(t, refusalDone && refusalPart)
	last := events[len(events)-1].Response.Output[0]
	require.Equal(t, []ResponsesContentPart{{Type: "refusal", Refusal: "unavailable"}, {Type: "output_text", Text: "body"}}, last.Content)
}

func TestResponsesRefusalDoneAfterTextDeliveredOnce(t *testing.T) {
	state := NewResponsesEventToAnthropicState()
	events := []ResponsesStreamEvent{
		{Type: "response.output_text.delta", ContentIndex: 0, Delta: "Partially available. "},
		{Type: "response.refusal.done", ContentIndex: 1, Refusal: "Unavailable."},
		{Type: "response.completed", Response: &ResponsesResponse{Status: "completed", Output: []ResponsesOutput{{Type: "message", Content: []ResponsesContentPart{{Type: "output_text", Text: "Partially available. "}, {Type: "refusal", Refusal: "Unavailable."}}}}}},
	}
	var chunks []string
	for _, event := range events {
		for _, message := range ResponsesEventToAnthropicEvents(&event, state) {
			if message.Delta != nil && message.Delta.Type == "text_delta" {
				chunks = append(chunks, message.Delta.Text)
			}
		}
	}
	require.Equal(t, []string{"Partially available. ", "Unavailable."}, chunks)
}
