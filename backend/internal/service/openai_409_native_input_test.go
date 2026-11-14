//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestOpenAI409NativeInputMetadataReplay(t *testing.T) {
	for _, metadata := range []struct{ name, value string }{
		{"object", `{"content_item_kinds":["text"]}`},
		{"null", `null`},
	} {
		for _, mutation := range []string{"unchanged", "before_text", "before_integer"} {
			t.Run(metadata.name+"/"+mutation, func(t *testing.T) {
				raw := []byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","instructions":"Keep completed tool results.","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Continue <result> & preserve the finding.","internal_chat_message_metadata_passthrough":{"keep":true}}],"internal_chat_message_metadata_passthrough":%s},{"type":"function_call","call_id":"call_record","name":"record","arguments":"{}"},{"type":"function_call_output","call_id":"call_record","output":{"sequence":9007199254740993,"finding":"already completed"}}],"tools":[{"type":"function","name":"record","parameters":{"type":"object"}}]}`, metadata.value))
				originalInput, originalControls := openAIAgentRecoveryInputDigest(raw), openAI409ControlsDigest(raw)
				recovery := &openAIAgentRecoveryRequest{
					omittedHistory: true, inputDigest: originalInput, replayControlsDigest: originalControls,
				}
				ctx := context.WithValue(context.Background(), openAIAgentRecoveryContextKey{}, recovery)
				c, _ := newTurnStateTestContext(t, 715, "native-input-metadata")
				var decoded map[string]any
				require.NoError(t, decodeOpenAIJSONUseNumber(raw, &decoded))
				input, ok := decoded["input"].([]any)
				require.True(t, ok)
				first, ok := input[0].(map[string]any)
				require.True(t, ok)
				parts, ok := first["content"].([]any)
				require.True(t, ok)
				textPart, ok := parts[0].(map[string]any)
				require.True(t, ok)
				resultItem, ok := input[2].(map[string]any)
				require.True(t, ok)
				output, ok := resultItem["output"].(map[string]any)
				require.True(t, ok)
				switch mutation {
				case "before_text":
					textPart["text"] = "Repeat the completed operation."
				case "before_integer":
					output["sequence"] = json.Number("9007199254740992")
				}
				err := prepareOpenAI409OAuthReplay(ctx, c, decoded)
				assert.Equal(t, originalInput, recovery.inputDigest, "never replace the original input snapshot")
				assert.Equal(t, originalControls, recovery.replayControlsDigest)
				if mutation != "unchanged" {
					require.Error(t, err, "native compatibility must not bless arbitrary input mutations")
					require.False(t, recovery.oauthReplayPrepared)
					return
				}
				require.NoError(t, err)
				require.True(t, recovery.oauthReplayPrepared)
				preparedInput, preparedControls := recovery.oauthInputDigest, recovery.oauthControlsDigest
				result := applyCodexOAuthTransformWithOptions(decoded, codexOAuthTransformOptions{
					PreserveReadableReplay: true, SkipDefaultInstructions: true,
				})
				require.NoError(t, result.Error)
				wire, err := json.Marshal(decoded)
				require.NoError(t, err)
				assert.False(t, gjson.GetBytes(wire, "input.0.internal_chat_message_metadata_passthrough").Exists())
				assert.True(t, gjson.GetBytes(wire, "input.0.content.0.internal_chat_message_metadata_passthrough.keep").Bool(), "same-named user content must remain")
				assert.Equal(t, "Continue <result> & preserve the finding.", gjson.GetBytes(wire, "input.0.content.0.text").String())
				assert.Equal(t, "9007199254740993", gjson.GetBytes(wire, "input.2.output.sequence").Raw)
				assert.Equal(t, "already completed", gjson.GetBytes(wire, "input.2.output.finding").String())
				assert.Equal(t, preparedInput, openAIAgentRecoveryInputDigest(wire), "later native metadata cleanup must match the prepared input")
				assert.Equal(t, preparedControls, openAI409ControlsDigest(wire))
				assert.Equal(t, originalInput, recovery.inputDigest)
				assert.Equal(t, originalControls, recovery.replayControlsDigest)
			})
		}
	}
}

func TestOpenAI409NativeInputWebSearchHistoryReplay(t *testing.T) {
	for _, mode := range []string{"non_lite", "lite_http", "lite_ws_metadata"} {
		t.Run(mode, func(t *testing.T) {
			raw := []byte(`{"model":"gpt-6.1-sol","instructions":"Use the existing search results.","input":[{"type":"web_search_call","id":"ws_recorded","status":"completed","action":{"type":"search","query":"recorded finding"}},{"type":"message","role":"user","content":[{"type":"input_text","text":"Summarize the existing results."}]}],"tools":[],"tool_choice":"auto"}`)
			c, _ := newTurnStateTestContext(t, 715, "native-input-web-history")
			switch mode {
			case "lite_http":
				c.Request.Header.Set(responsesLiteHeader, "true")
			case "lite_ws_metadata":
				var err error
				raw, err = sjson.SetBytes(raw, "client_metadata.ws_request_header_x_openai_internal_codex_responses_lite", "true")
				require.NoError(t, err)
			}
			lite := isOpenAIResponsesLiteHeader(c.GetHeader(responsesLiteHeader)) || isOpenAIResponsesLiteWebSocketPayload(raw)
			require.Equal(t, mode != "non_lite", lite)
			originalInput, originalControls := openAIAgentRecoveryInputDigest(raw), openAI409ControlsDigest(raw)
			recovery := &openAIAgentRecoveryRequest{
				omittedHistory: true, inputDigest: originalInput, replayControlsDigest: originalControls,
			}
			ctx := context.WithValue(context.Background(), openAIAgentRecoveryContextKey{}, recovery)
			var decoded map[string]any
			require.NoError(t, decodeOpenAIJSONUseNumber(raw, &decoded))
			require.NoError(t, prepareOpenAI409OAuthReplay(ctx, c, decoded))
			require.True(t, recovery.oauthReplayPrepared)
			preparedInput, preparedControls := recovery.oauthInputDigest, recovery.oauthControlsDigest
			result := applyCodexOAuthTransformWithOptions(decoded, codexOAuthTransformOptions{
				PreserveReadableReplay: true, SkipDefaultInstructions: true, ResponsesLite: lite,
			})
			require.NoError(t, result.Error)
			wire, err := json.Marshal(decoded)
			require.NoError(t, err)
			assert.Equal(t, "none", gjson.GetBytes(wire, "tool_choice").String(), "history declaration must not enable new tool calls")
			for _, path := range []string{"input.0", "input.1"} {
				assert.JSONEq(t, gjson.GetBytes(raw, path).Raw, gjson.GetBytes(wire, path).Raw, "recorded history must remain")
			}
			if lite {
				assert.Equal(t, int64(3), gjson.GetBytes(wire, "input.#").Int())
				assert.Equal(t, "additional_tools", gjson.GetBytes(wire, "input.2.type").String())
				assert.Equal(t, "developer", gjson.GetBytes(wire, "input.2.role").String())
				assert.True(t, gjsonToolsContainWebSearch(gjson.GetBytes(wire, "input.2.tools")))
				assert.False(t, gjsonToolsContainWebSearch(gjson.GetBytes(wire, "tools")))
			} else {
				assert.Equal(t, int64(2), gjson.GetBytes(wire, "input.#").Int())
				assert.True(t, gjsonToolsContainWebSearch(gjson.GetBytes(wire, "tools")))
			}
			assert.Equal(t, preparedInput, openAIAgentRecoveryInputDigest(wire), "history-tool input injection must be prepared before freezing")
			assert.Equal(t, preparedControls, openAI409ControlsDigest(wire), "history-tool declarations and choice must be prepared before freezing")
			assert.Equal(t, originalInput, recovery.inputDigest, "derived OAuth expectations must leave the original snapshot intact")
			assert.Equal(t, originalControls, recovery.replayControlsDigest)
		})
	}
}
