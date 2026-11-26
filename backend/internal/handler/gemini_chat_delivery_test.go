package handler

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type geminiTestSink struct {
	gin.ResponseWriter
	failWrite, failFlush bool
}

func (s *geminiTestSink) Write(p []byte) (int, error) {
	if s.failWrite {
		return 0, io.ErrClosedPipe
	}
	return s.ResponseWriter.Write(p)
}
func (s *geminiTestSink) FlushError() error {
	if s.failFlush {
		return io.ErrClosedPipe
	}
	s.ResponseWriter.Flush()
	return nil
}

func TestGeminiBridgeFinalWriter(t *testing.T) {
	const role = `data: {"choices":[{"index":0,"delta":{"role":"assistant"}}]}` + "\n\n"
	const text = `data: {"choices":[{"index":0,"delta":{"content":"first"}}]}` + "\n\n"
	for _, mode := range []string{"headers only", "metadata flush then invalid", "cancel tail", "write failure", "flush failure"} {
		t.Run(mode, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			sink := &geminiTestSink{ResponseWriter: c.Writer}
			c.Writer = sink
			collector := requesttiming.New(time.Now(), 0)
			ctx, cancel := context.WithCancel(requesttiming.With(context.Background(), collector))
			defer cancel()
			c.Request = httptest.NewRequest("POST", "/v1beta/models/fixture:streamGenerateContent", strings.NewReader(`{"contents":[{"parts":[{"text":"test"}]}]}`)).WithContext(ctx)
			c.Params = gin.Params{{Key: "modelAction", Value: "/fixture:streamGenerateContent"}}
			ServeGeminiViaChatCompletions(c, func(c *gin.Context) {
				c.Header("Content-Type", "text/event-stream")
				switch mode {
				case "headers only":
					return
				case "metadata flush then invalid":
					_, err := io.WriteString(c.Writer, role)
					require.NoError(t, err)
					require.NoError(t, service.FlushGatewayResponse(c.Writer))
					require.False(t, sink.Written())
					_, err = io.WriteString(c.Writer, "data: invalid\n\n")
					require.Error(t, err)
				case "cancel tail":
					_, err := io.WriteString(c.Writer, "data: {")
					require.NoError(t, err)
					cancel()
				default:
					sink.failWrite = mode == "write failure"
					sink.failFlush = mode == "flush failure"
					_, err := io.WriteString(c.Writer, text)
					if sink.failFlush {
						require.NoError(t, err)
						err = service.FlushGatewayResponse(c.Writer)
					}
					require.ErrorIs(t, err, io.ErrClosedPipe)
					_, err = io.WriteString(c.Writer, text)
					require.ErrorIs(t, err, io.ErrClosedPipe)
				}
			})
			require.NotEmpty(t, c.Errors)
			if mode == "headers only" || mode == "metadata flush then invalid" {
				require.Equal(t, 502, rec.Code)
				require.Equal(t, 1, strings.Count(rec.Body.String(), `"error"`))
				require.Contains(t, rec.Header().Get("Content-Type"), "application/json")
			}
			if mode == "cancel tail" {
				require.Empty(t, rec.Body.String())
				require.ErrorIs(t, c.Errors.Last().Err, context.Canceled)
			}
			collector.Finish(rec.Code, ctx.Err() != nil)
			collector.WhenFinished(func(s requesttiming.Snapshot) {
				_, textWritten := s.Events["downstream_first_text_write"]
				_, writeFailed := s.Events["downstream_write_failed"]
				_, flushFailed := s.Events["downstream_flush_failed"]
				require.Equal(t, mode == "flush failure", textWritten)
				require.Equal(t, mode == "write failure", writeFailed)
				require.Equal(t, mode == "flush failure", flushFailed)
			})
		})
	}
}
