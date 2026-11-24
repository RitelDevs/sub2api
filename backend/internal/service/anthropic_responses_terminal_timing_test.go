//go:build unit

package service

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type terminalReceiptWriter struct {
	gin.ResponseWriter
	shortWrite, failFlush, sawTerminal bool
}

func (w *terminalReceiptWriter) Write(p []byte) (int, error) {
	if bytes.HasPrefix(p, []byte("event: response.completed\n")) {
		w.sawTerminal = true
		if w.shortWrite {
			return w.ResponseWriter.Write(p[:12])
		}
	}
	return w.ResponseWriter.Write(p)
}

func (w *terminalReceiptWriter) FlushError() error {
	if w.failFlush && w.sawTerminal {
		return io.ErrClosedPipe
	}
	return FlushGatewayResponse(w.ResponseWriter)
}

func TestAnthropicResponsesTerminalTiming(t *testing.T) {
	const start = `data: {"type":"message_start","message":{"id":"msg","role":"assistant","model":"fixture","usage":{"input_tokens":3}}}

data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"visible"}}

data: {"type":"content_block_stop","index":0}

`
	for _, tc := range []struct {
		name, stop, terminal  string
		shortWrite, failFlush bool
	}{
		{name: "completed", stop: "end_turn", terminal: "response.completed"},
		{name: "incomplete", stop: "max_tokens", terminal: "response.incomplete"},
		{name: "upstream_error", terminal: "response.failed"},
		{name: "short_write", stop: "end_turn", shortWrite: true},
		{name: "flush_failure", stop: "end_turn", terminal: "response.completed", failFlush: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tail := "data: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"fixture\"}}\n\n"
			if tc.stop != "" {
				tail = `data: {"type":"message_delta","delta":{"stop_reason":"` + tc.stop + `"},"usage":{"output_tokens":4}}` + "\n\ndata: {\"type\":\"message_stop\"}\n\n"
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Writer = &terminalReceiptWriter{ResponseWriter: c.Writer, shortWrite: tc.shortWrite, failFlush: tc.failFlush}
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			now := time.Now()
			collector := requesttiming.New(now, 0)
			c.Request = c.Request.WithContext(requesttiming.With(c.Request.Context(), collector))
			resp := &http.Response{Body: io.NopCloser(strings.NewReader(start + tail)), Header: make(http.Header)}
			_, err := (&GatewayService{}).handleResponsesStreamingResponse(resp, c, "fixture", "fixture", nil, now, apicompat.ResponsesClientToolMapping{})
			if tc.shortWrite || tc.failFlush || tc.stop == "" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tc.terminal != "" {
				require.Equal(t, 1, strings.Count(recorder.Body.String(), "event: "+tc.terminal+"\n"))
			}
			collector.Finish(200, false)
			collector.WhenFinished(func(s requesttiming.Snapshot) {
				_, terminal := s.Events["downstream_first_terminal_write"]
				_, writeFailure := s.Events["downstream_write_failed"]
				_, flushFailure := s.Events["downstream_flush_failed"]
				require.Equal(t, tc.terminal != "", terminal)
				require.Equal(t, tc.shortWrite, writeFailure)
				require.Equal(t, tc.failFlush, flushFailure)
			})
		})
	}
}
