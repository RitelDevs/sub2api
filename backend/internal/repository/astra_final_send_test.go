package repository

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestRetiredBorrowNeverInvokesFinalSendCallback(t *testing.T) {
	s := &astraRoutingUpstream{}
	ctx := service.WithGatewayBorrowRequiredModel(t.Context(), "gpt-6-astra")
	called := false
	ctx = service.WithGatewayBorrowFinalSendCheck(ctx, func(*http.Request) error { called = true; return nil })
	resp, err := s.Do(accountBorrowRequest(t, "gpt-6-astra", "").WithContext(ctx), "fixed", 300, 1)
	require.Nil(t, resp)
	require.True(t, service.IsOpenAITurnAdmissionError(err))
	require.False(t, called)
}
