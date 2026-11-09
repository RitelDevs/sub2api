//go:build unit

package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestDeepSeekRecoveryFailureKeepsStructuredReason(t *testing.T) {
	cause := fmt.Errorf("wrapped private response: %w", deepSeekRecoveryFailure("incomplete_tool_call", 0))
	require.Equal(t, "deepseek_incomplete_tool_call", DeepSeekRecoveryFailureReason(cause))
	require.Equal(t, "deepseek_attempt_failed", DeepSeekRecoveryFailureReason(errors.New("private response")))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	RecordDeepSeekRecoveryFailure(c, &Account{ID: 192, Platform: "deepseek"}, nil, 1, cause, DeepSeekRecoveryOutcome{})
	raw, ok := c.Get(OpsUpstreamErrorsKey)
	require.True(t, ok)
	events := raw.([]*OpsUpstreamErrorEvent)
	require.Len(t, events, 1)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(events[0].Detail), &detail))
	require.Equal(t, "deepseek_incomplete_tool_call", detail["failure_reason"])
	require.NotContains(t, events[0].Detail, "private response")
}

func TestDeepSeekRecoveryToolPayloadBoundary(t *testing.T) {
	for _, v := range []any{nil, map[string]any{}, map[string]any{"name": "", "arguments": ""}, map[string]any{"name": nil, "arguments": nil}} {
		require.False(t, DeepSeekRecoveryToolPayloadPresent("function_call", v))
	}
	for _, v := range []any{map[string]any{"name": "lookup", "arguments": ""}, map[string]any{"arguments": "{"}, map[string]any{"id": "call1"}, map[string]any{"arguments": map[string]any{}}} {
		require.True(t, DeepSeekRecoveryToolPayloadPresent("function_call", v))
	}
	require.False(t, DeepSeekRecoveryToolPayloadPresent("tool_calls", []any{}))
	require.True(t, DeepSeekRecoveryToolPayloadPresent("tool_calls", []any{map[string]any{}}))
}
