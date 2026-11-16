package httputil

import (
	"context"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestRequestProcessingMemoryNestedCancellationAndRelease(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	budget := NewBodyMemoryBudget(10000)
	ctx, release := WithRequestBodyMemoryBudget(parent, budget)
	defer release()
	require.NoError(t, ReserveRequestBodyMemory(ctx, 100))
	body := []byte("opaque")
	processing, finish, err := WithRequestBodyProcessing(ctx, body, 6)
	require.NoError(t, err)
	require.Equal(t, int64(136), budget.Snapshot().UsedBytes)
	nested, done, err := WithRequestBodyProcessing(processing, body, 12)
	require.NoError(t, err)
	done()
	require.Equal(t, int64(172), budget.Snapshot().UsedBytes)
	cancel()
	// Detached upstream usage draining still owns processing storage.
	require.NoError(t, EnsureRequestBodyProcessingMemory(context.WithoutCancel(nested), 100))
	require.Equal(t, int64(200), budget.Snapshot().UsedBytes)
	finish()
	finish()
	require.Equal(t, int64(100), budget.Snapshot().UsedBytes)
	require.ErrorIs(t, EnsureRequestBodyProcessingMemory(nested, 1), ErrRequestBodyMemoryBudget)
	release()
	require.Zero(t, budget.Snapshot().UsedBytes)
}

func TestRequestProcessingMemoryRejectsBeforeAllocationAndReusesCapacity(t *testing.T) {
	budget := NewBodyMemoryBudget(60)
	ctx, release := WithRequestBodyMemoryBudget(context.Background(), budget)
	defer release()
	first, finish, err := WithRequestBodyProcessing(ctx, []byte("12345"), 6)
	require.NoError(t, err)
	for range 100 {
		require.NoError(t, EnsureRequestBodyProcessingMemory(first, 30))
	}
	require.Equal(t, int64(30), budget.Snapshot().UsedBytes)
	_, _, err = WithRequestBodyProcessing(first, []byte("123456"), 12)
	require.ErrorIs(t, err, ErrRequestBodyMemoryBudget)
	require.Equal(t, int64(30), budget.Snapshot().UsedBytes)
	require.Equal(t, uint64(1), budget.Snapshot().Rejected)
	finish()
	require.Zero(t, budget.Snapshot().UsedBytes)
	_, finish, err = WithRequestBodyProcessing(ctx, []byte("123456"), 6)
	require.NoError(t, err)
	finish()
}

func TestRequestProcessingMemoryAccountsForDenseJSON(t *testing.T) {
	text := []byte("\"" + strings.Repeat("{[:,", 1024) + "\"")
	require.Equal(t, int64(len(text))*6, RequestBodyProcessingEstimate(text, 6))
	dense := []byte("[" + strings.TrimSuffix(strings.Repeat("{\"a\":[]},", 100), ",") + "]")
	require.Greater(t, RequestBodyProcessingEstimate(dense, 6), int64(len(dense))*20)
	escaped := []byte("\"\\\"{[:,\"")
	require.Equal(t, int64(len(escaped))*6, RequestBodyProcessingEstimate(escaped, 6))
}

func TestRequestProcessingMemoryDisabled(t *testing.T) {
	ctx, release, err := WithRequestBodyProcessing(context.Background(), []byte("{}"), 12)
	require.NoError(t, err)
	require.Equal(t, context.Background(), ctx)
	release()
}
