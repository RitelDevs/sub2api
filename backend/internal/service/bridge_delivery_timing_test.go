//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAnthropicBridgesRecordWrittenTextAfterMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)
	frame := func(name, data string) string { return "event: " + name + "\ndata: " + data + "\n\n" }
	head := frame("message_start", `{"type":"message_start","message":{"id":"fixture","type":"message","role":"assistant","model":"fixture","content":[],"usage":{"input_tokens":5,"output_tokens":0}}}`)
	tail := frame("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`) +
		frame("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"delivered"}}`) +
		frame("content_block_stop", `{"type":"content_block_stop","index":0}`) +
		frame("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`) +
		frame("message_stop", `{"type":"message_stop"}`)
	for _, protocol := range []string{"chat", "responses"} {
		t.Run(protocol, func(t *testing.T) {
			start := time.Now()
			collector := requesttiming.New(start, 0)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil).WithContext(requesttiming.With(context.Background(), collector))
			reader, writer := io.Pipe()
			defer reader.Close()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				defer writer.Close()
				_, _ = io.WriteString(writer, head)
				// This intentional upstream delay distinguishes metadata from text.
				time.Sleep(100 * time.Millisecond)
				_, _ = io.WriteString(writer, tail)
			}()
			resp := &http.Response{Body: reader, Header: http.Header{}}
			var result *ForwardResult
			var err error
			if protocol == "chat" {
				result, err = (&GatewayService{}).handleCCStreamingFromAnthropic(resp, c, "fixture", "fixture", nil, start)
			} else {
				result, err = (&GatewayService{}).handleResponsesStreamingResponse(resp, c, "fixture", "fixture", nil, start, apicompat.ResponsesClientToolMapping{})
			}
			<-finished
			require.NoError(t, err)
			require.NotNil(t, result.FirstTokenMs)
			collector.Finish(200, false)
			collector.WhenFinished(func(s requesttiming.Snapshot) {
				first, ok := s.Events["bridge_first_upstream_event"]
				require.True(t, ok)
				text, ok := s.Events["downstream_first_text_write"]
				require.True(t, ok)
				require.Greater(t, text, first)
				require.GreaterOrEqual(t, text, 100.0)
				require.LessOrEqual(t, float64(*result.FirstTokenMs), first+2)
				t.Logf("legacy=%dms metadata=%.2fms text_write=%.2fms", *result.FirstTokenMs, first, text)
			})
		})
	}
}

func BenchmarkGeminiBridgeDeliveryObservation(b *testing.B) {
	wire := []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"word\"}]}}]}\n\n")
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		ctx := context.Background()
		if enabled {
			name = "enabled"
			ctx = requesttiming.With(ctx, requesttiming.New(time.Now(), 0))
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				requesttiming.DeliveredSSE(ctx, wire)
			}
		})
	}
}
