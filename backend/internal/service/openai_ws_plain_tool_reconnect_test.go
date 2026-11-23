package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

// Ordinary complete plaintext history is valid stateless input. Adding an old
// provider pointer opts the same history into the stricter recovery path; a
// completed ordinary tool call alone does not prove an encrypted task anchor.
func TestWSPlainToolReconnectDoesNotRequireEncryptedRecovery(t *testing.T) {
	svc := &OpenAIGatewayService{cfg: &config.Config{}, settingService: NewSettingService(&codexPolicyMigrationRepoStub{values: map[string]string{SettingKeyOpenAI409OmitEncryptedHistoryEnabled: "true"}}, nil)}
	manager, c, account := issue715WSManager(t, svc, context.Background())
	SetOpenAIWSRecoveryPrincipal(c, 715, 715)
	defer manager.abort()
	require.NoError(t, issue715WSBegin(t, manager, account, `{"model":"gpt-6-astra","input":[{"role":"user","content":"remember a fixture marker and call echo"}]}`, "ctx_pool"))
	const call = `{"type":"function_call","name":"echo","call_id":"call_plain_fixture","arguments":"{}","status":"completed"}`
	terminal := []byte(`{"type":"response.completed","response":{"id":"resp_plain_fixture","status":"completed","output":[` + call + `]}}`)
	require.NoError(t, manager.write(terminal, func() error { return nil }))
	proof, err := svc.toolProofGet(context.Background(), manager.request.toolKey("call_plain_fixture"))
	require.NoError(t, err)
	require.Equal(t, "failed", proof, "a plaintext call grants no encrypted-task recovery authority")

	const history = `"model":"gpt-6-astra","input":[{"role":"user","content":"remember the fixture marker"},` + call + `,{"type":"function_call_output","call_id":"call_plain_fixture","output":"completed exactly once"},{"role":"user","content":"continue with the complete supplied history"}]`
	stateless := []byte(`{` + history + `}`)
	ctx := svc.WithOpenAIWSAgentRecovery(context.Background(), c, stateless, "fixture-session", "gpt-6-astra")
	_, clean, changed, err := svc.PrepareOpenAI409ReadableContinuation(ctx, c, stateless, "fixture-session", "gpt-6-astra")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, stateless, clean, "ordinary input retains every call, result and instruction")

	stale := []byte(`{"previous_response_id":"resp_plain_fixture",` + history + `}`)
	ctx = svc.WithOpenAIWSAgentRecovery(context.Background(), c, stale, "fixture-session", "gpt-6-astra")
	_, _, _, err = svc.PrepareOpenAI409ReadableContinuation(ctx, c, stale, "fixture-session", "gpt-6-astra")
	var refused *OpenAIAgentRecoveryError
	require.ErrorAs(t, err, &refused)
	require.Equal(t, "owner_conflict", refused.Reason)
}
