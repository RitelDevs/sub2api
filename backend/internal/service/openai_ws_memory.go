package service

import (
	"context"

	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// Reader preserves coder/websocket's configured per-message limit, including
// compressed messages. Keep the existing control-close/usage-drain policy;
// cancellation is handled by the owner which closes and joins the reader.
func readOpenAIWSBudgetedFrame(ctx context.Context, conn *coderws.Conn) (coderws.MessageType, []byte, error) {
	kind, reader, err := conn.Reader(context.Background())
	if err != nil {
		return kind, nil, err
	}
	payload, err := pkghttputil.ReadBodyWithMemoryBudget(context.WithoutCancel(ctx), reader)
	return kind, payload, err
}

// takeFrame transfers queue ownership to the single consumer. The processing
// envelope owns any old payload still referenced by history/retry after this.
func (r *openAIWSIngressReader) takeFrame(frame openAIWSClientRead) (coderws.MessageType, []byte, error) {
	r.waitMu.Lock()
	if r.activeRelease != nil {
		r.activeRelease()
	}
	r.activeRelease = frame.release
	r.waitMu.Unlock()
	r.lastReadAt.Store(&frame.receivedAt)
	return frame.messageType, frame.payload, nil
}

// CloseOpenAIWSIngressReader joins the producer before returning buffered-byte
// reservations, including queued/preserved frames never handed to a consumer.
// The handler owns this across account retries.
func CloseOpenAIWSIngressReader(c *gin.Context) {
	r := openAIWSIngressReaderFromContext(c)
	if r == nil {
		return
	}
	_ = r.conn.CloseNow()
	<-r.done
	r.waitMu.Lock()
	defer r.waitMu.Unlock()
	if r.activeRelease != nil {
		r.activeRelease()
		r.activeRelease = nil
	}
	for _, frame := range r.waitFrames {
		if frame.release != nil {
			frame.release()
		}
	}
	r.waitFrames = nil
	for {
		select {
		case frame := <-r.frames:
			if frame.release != nil {
				frame.release()
			}
		default:
			return
		}
	}
}
