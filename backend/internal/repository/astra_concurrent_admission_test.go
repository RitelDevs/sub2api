package repository

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAstraConcurrentPreparationNeverStarts(t *testing.T) {
	s := &astraRoutingUpstream{}
	s.SetAstraGatewayPreparer(func(context.Context, int64) error { panic("source callback invoked") })
	done := make(chan error, 8)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	for range cap(done) {
		go func() { done <- s.PrepareAstraGateway(ctx) }()
	}
	for range cap(done) {
		select {
		case err := <-done:
			require.ErrorContains(t, err, "gateway_borrow_retired")
		case <-ctx.Done():
			t.Fatal("retired preparation blocked")
		}
	}
}
