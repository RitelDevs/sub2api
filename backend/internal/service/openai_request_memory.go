package service

import (
	"context"
	"encoding/json"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	coderws "github.com/coder/websocket"
)

// Admission estimates calibrated with BenchmarkOpenAIRequestMemory, not heap
// bounds. Raw-byte copies alone miss dense JSON's object overhead.
const (
	OpenAINativeProcessingCopies     int64 = 6
	openAIConversionProcessingCopies int64 = 14
)

func beginOpenAIRequestMemory(ctx context.Context, body []byte, conversion bool) (context.Context, func(), error) {
	copies := OpenAINativeProcessingCopies
	if conversion {
		copies = openAIConversionProcessingCopies
	}
	return pkghttputil.WithRequestBodyProcessing(ctx, body, copies)
}

func openAIWSMemoryError(err error) error {
	return NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "request memory capacity is exhausted; retry later", err)
}

// Replay items share immutable payload backing arrays. Track each array once by
// its last address (no unsafe pointer arithmetic); retain its full original
// capacity even when only a tiny item survives. Drop entries no longer referenced
// after context compaction. This metadata owns no additional payload copies.
type openAIWSReplayMemory struct{ backings map[*byte]int64 }

func wsMemoryBacking(body []byte) *byte {
	if cap(body) == 0 {
		return nil
	}
	return &body[:cap(body)][cap(body)-1]
}

func (m *openAIWSReplayMemory) measure(groups [][]json.RawMessage, owners ...[]byte) int64 {
	next := make(map[*byte]int64)
	var headers int64
	for _, items := range groups {
		headers += int64(cap(items)) * 24
		for _, item := range items {
			key := wsMemoryBacking(item)
			if key == nil {
				continue
			}
			size := int64(cap(item))
			if old := m.backings[key]; old > size {
				size = old
			}
			if size > next[key] {
				next[key] = size
			}
		}
	}
	for _, owner := range owners {
		key := wsMemoryBacking(owner)
		if _, retained := next[key]; retained && int64(cap(owner)) > next[key] {
			next[key] = int64(cap(owner))
		}
	}
	m.backings = next
	for _, size := range next {
		headers += size
	}
	return headers
}

func resizeOpenAIWSProcessingMemory(ctx context.Context, payload []byte, retained int64) error {
	estimate := pkghttputil.RequestBodyProcessingEstimate(payload, OpenAINativeProcessingCopies) + retained*2
	if err := pkghttputil.ResizeRequestBodyProcessingMemory(ctx, estimate); err != nil {
		return openAIWSMemoryError(err)
	}
	return nil
}

func ensureOpenAIWSProcessingMemory(ctx context.Context, payload []byte, retained int64) error {
	// The retained allowance includes old payload backing storage and history
	// copies used by retry assembly. Current JSON structure is counted too.
	estimate := pkghttputil.RequestBodyProcessingEstimate(payload, OpenAINativeProcessingCopies) + retained*2
	if err := pkghttputil.EnsureRequestBodyProcessingMemory(ctx, estimate); err != nil {
		return openAIWSMemoryError(err)
	}
	return nil
}

// Reserve the effective full replay before json.Marshal/sjson materialize it.
// An incremental frame may be tiny even when retained history is large/dense.
// Count both current source and full input for their transient overlap.
func ensureOpenAIWSReplayProcessingMemory(ctx context.Context, payload []byte, items []json.RawMessage, retained int64) error {
	estimate := pkghttputil.RequestBodyProcessingEstimate(payload, OpenAINativeProcessingCopies) + retained*2
	for _, item := range items {
		estimate += pkghttputil.RequestBodyProcessingEstimate(item, OpenAINativeProcessingCopies) + 128 + OpenAINativeProcessingCopies
	}
	if err := pkghttputil.EnsureRequestBodyProcessingMemory(ctx, estimate); err != nil {
		return openAIWSMemoryError(err)
	}
	return nil
}
