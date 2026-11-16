package httputil

import (
	"context"
	"sync"
)

// RequestBodyProcessingEstimate reserves headroom, not a hard bound on the Go
// heap. Large strings need copies; dense JSON also allocates maps, slices and
// nodes. Count structural punctuation outside strings without decoding.
func RequestBodyProcessingEstimate(body []byte, copies int64) int64 {
	n := int64(len(body)) * copies
	quoted, escaped := false, false
	for _, c := range body {
		if quoted {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{', '[', ':', ',':
			n += 128
		}
	}
	return n
}

type processingMemoryKey struct{}
type processingMemory struct {
	mu       sync.Mutex
	ctx      context.Context
	reserved int64
	closed   bool
}

// WithRequestBodyProcessing owns one processing reservation. Nested adapters
// share the outer envelope and only top it up, rather than charging every layer.
// Its lifetime includes detached stream draining; cancellation never releases it.
// The owner must release after processing/accounting ends.
func WithRequestBodyProcessing(ctx context.Context, body []byte, copies int64) (context.Context, func(), error) {
	if ctx.Value(bodyMemoryReservationKey{}) == nil {
		return ctx, func() {}, nil
	}
	if _, ok := ctx.Value(processingMemoryKey{}).(*processingMemory); ok {
		return ctx, func() {}, EnsureRequestBodyProcessingMemory(ctx, RequestBodyProcessingEstimate(body, copies))
	}
	scope, release := WithRequestBodyMemoryScope(ctx)
	m := &processingMemory{ctx: scope}
	ctx = context.WithValue(scope, processingMemoryKey{}, m)
	releaseOwned := func() { m.mu.Lock(); defer m.mu.Unlock(); m.closed = true; release() }
	if err := EnsureRequestBodyProcessingMemory(ctx, RequestBodyProcessingEstimate(body, copies)); err != nil {
		release()
		return ctx, func() {}, err
	}
	return ctx, releaseOwned, nil
}

// EnsureRequestBodyProcessingMemory grows the enclosing envelope before a
// transformation. Keep its high-water reservation until its owner returns:
// old payloads can still be referenced by retry, audit or WS history state.
func EnsureRequestBodyProcessingMemory(ctx context.Context, bytes int64) error {
	m, _ := ctx.Value(processingMemoryKey{}).(*processingMemory)
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrRequestBodyMemoryBudget
	}
	if bytes <= m.reserved {
		return nil
	}
	if err := ReserveRequestBodyMemory(m.ctx, bytes-m.reserved); err != nil {
		return err
	}
	m.reserved = bytes
	return nil
}

// ResizeRequestBodyProcessingMemory is for an owner's proven lifetime boundary,
// after old per-turn representations have been discarded. Nested adapters use
// Ensure instead; they cannot refund storage still retained by their caller.
func ResizeRequestBodyProcessingMemory(ctx context.Context, bytes int64) error {
	m, _ := ctx.Value(processingMemoryKey{}).(*processingMemory)
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return ErrRequestBodyMemoryBudget
	}
	if bytes > m.reserved {
		if err := ReserveRequestBodyMemory(m.ctx, bytes-m.reserved); err != nil {
			return err
		}
	} else {
		ReleaseRequestBodyMemory(m.ctx, m.reserved-bytes)
	}
	m.reserved = bytes
	return nil
}
