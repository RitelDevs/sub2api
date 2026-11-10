package middleware

import (
	"bufio"
	"bytes"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/pkg/clienterror"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// GatewayErrorPrivacy keeps correlation identifiers on the server side. It is
// outside the gateway's capture writer so diagnostics retain the original error.
func GatewayErrorPrivacy() gin.HandlerFunc {
	return func(c *gin.Context) {
		original := c.Writer
		w := &gatewayPrivacyWriter{ResponseWriter: original}
		c.Writer = w
		defer func() { c.Writer = original }()
		c.Next()
		if err := w.finish(); err != nil {
			_ = c.Error(err)
		}
	}
}

type gatewayPrivacyWriter struct {
	gin.ResponseWriter
	mu           sync.Mutex
	pending      []byte
	sseScanned   int
	sseLineStart int
	writeErr     error
}

func removeGatewayRequestIDHeaders(h http.Header) {
	for key := range h {
		normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
		switch normalized {
		case "requestid", "xrequestid", "clientrequestid", "xclientrequestid", "upstreamrequestid", "xupstreamrequestid", "xamznrequestid":
			delete(h, key)
		}
	}
}

func (w *gatewayPrivacyWriter) WriteHeader(code int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prepareHeaders()
	if code >= http.StatusBadRequest {
		w.Header().Del("Content-Length")
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *gatewayPrivacyWriter) WriteHeaderNow() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prepareHeaders()
	w.ResponseWriter.WriteHeaderNow()
}
func (w *gatewayPrivacyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	w.prepareHeaders()
	switch {
	case strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream"):
		w.pending = append(w.pending, p...)
		for {
			end := w.sseFrameEnd()
			if end == 0 {
				break
			}
			frame := sanitizeGatewaySSEFrame(w.pending[:end])
			if _, err := w.ResponseWriter.Write(frame); err != nil {
				w.writeErr = err
				return 0, err
			}
			w.pending = w.pending[end:]
			w.sseScanned, w.sseLineStart = 0, 0
		}
		if len(w.pending) == 0 {
			w.pending = nil
		}
	case w.Status() >= http.StatusBadRequest:
		w.pending = append(w.pending, p...)
		if gjson.ValidBytes(w.pending) {
			body := cleanGatewayErrorJSON(w.pending)
			if _, err := w.ResponseWriter.Write(body); err != nil {
				w.writeErr = err
				return 0, err
			}
			w.pending = nil
		}
	default:
		body, changed := clienterror.JSON(p)
		if changed {
			w.Header().Del("Content-Length")
		}
		if _, err := w.ResponseWriter.Write(body); err != nil {
			w.writeErr = err
			return 0, err
		}
	}
	return len(p), nil
}
func (w *gatewayPrivacyWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }
func (w *gatewayPrivacyWriter) Flush()                            { _ = w.FlushError() }
func (w *gatewayPrivacyWriter) FlushError() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return w.writeErr
	}
	w.prepareHeaders()
	// A partial SSE frame or JSON error stays private until it is complete.
	if f, ok := w.ResponseWriter.(interface{ FlushError() error }); ok {
		w.writeErr = f.FlushError()
		return w.writeErr
	}
	w.ResponseWriter.Flush()
	return nil
}
func (w *gatewayPrivacyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.prepareHeaders()
	return w.ResponseWriter.Hijack()
}
func (w *gatewayPrivacyWriter) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.writeErr != nil {
		return w.writeErr
	}
	w.prepareHeaders()
	if len(w.pending) == 0 {
		return nil
	}
	body := w.pending
	if strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream") {
		body = sanitizeGatewaySSEFrame(body)
	} else if gjson.ValidBytes(body) {
		body = cleanGatewayErrorJSON(body)
	} else {
		body = []byte(clienterror.Message(string(body)))
	}
	_, err := w.ResponseWriter.Write(body)
	w.pending = nil
	return err
}

// Track scanned bytes so a large, fragmented image event is examined once.
func (w *gatewayPrivacyWriter) sseFrameEnd() int {
	for i := w.sseScanned; i < len(w.pending); i++ {
		if w.pending[i] != '\n' {
			continue
		}
		line := bytes.TrimSuffix(w.pending[w.sseLineStart:i], []byte{'\r'})
		w.sseLineStart = i + 1
		w.sseScanned = i + 1
		if len(line) == 0 {
			return i + 1
		}
	}
	w.sseScanned = len(w.pending)
	return 0
}
func sanitizeGatewaySSEFrame(frame []byte) []byte {
	lines := bytes.SplitAfter(frame, []byte{'\n'})
	var data [][]byte
	event := ""
	for _, raw := range lines {
		line := bytes.TrimRight(raw, "\r\n")
		if bytes.HasPrefix(line, []byte("event:")) {
			event = strings.TrimSpace(string(line[6:]))
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			data = append(data, bytes.TrimPrefix(line[5:], []byte{' '}))
		}
	}
	if len(data) == 0 {
		return frame
	}
	payload := bytes.Join(data, []byte{'\n'})
	cleaned, changed := clienterror.JSON(payload)
	if !changed && (event == "error" || event == "response.failed") && gjson.ValidBytes(payload) {
		cleaned = cleanGatewayErrorJSON(payload)
		changed = !bytes.Equal(cleaned, payload)
	}
	if !changed && (event == "error" || event == "response.failed") && !gjson.ValidBytes(payload) {
		cleaned = []byte(clienterror.Message(string(payload)))
		changed = !bytes.Equal(cleaned, payload)
	}
	if !changed {
		return frame
	}
	var out bytes.Buffer
	emitted := false
	for _, raw := range lines {
		if bytes.HasPrefix(raw, []byte("data:")) {
			if !emitted {
				out.WriteString("data: ")
				out.Write(cleaned)
				if bytes.HasSuffix(raw, []byte("\r\n")) {
					out.WriteString("\r\n")
				} else if bytes.HasSuffix(raw, []byte{'\n'}) {
					out.WriteByte('\n')
				}
				emitted = true
			}
		} else {
			out.Write(raw)
		}
	}
	return out.Bytes()
}

var _ gin.ResponseWriter = (*gatewayPrivacyWriter)(nil)

func (w *gatewayPrivacyWriter) prepareHeaders() {
	removeGatewayRequestIDHeaders(w.Header())
	if w.Status() >= http.StatusBadRequest || strings.Contains(strings.ToLower(w.Header().Get("Content-Type")), "text/event-stream") {
		w.Header().Del("Content-Length")
	}
}

// The HTTP status or SSE event already established this as an error, even when
// the provider used only top-level message/request_id fields.
func cleanGatewayErrorJSON(payload []byte) []byte {
	cleaned, _ := clienterror.ErrorJSON(payload)
	return cleaned
}
