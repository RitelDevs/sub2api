package handler

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestGeminiBridgeEquivalentSSEFraming(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const payload = `{"choices":[{"index":0,"delta":{"content":"你好"},"finish_reason":"stop"}]}`
	const usage = `data: {"choices":[],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		for _, multiline := range []bool{false, true} {
			for _, step := range []int{1, 17, 4096} {
				t.Run(strings.ReplaceAll(strings.ReplaceAll(newline, "\r", "CR"), "\n", "LF")+"/"+strconv.Itoa(step)+"/multi="+map[bool]string{true: "yes", false: "no"}[multiline], func(t *testing.T) {
					data := payload
					if multiline {
						data = strings.Replace(data, `,"delta"`, ","+newline+`data: "delta"`, 1)
					}
					wire := "\xef\xbb\xbfevent: message" + newline + "id: fixture" + newline + "data:" + data + newline + newline + usage + newline + newline + "data: [DONE]" + newline + newline
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/fixture:streamGenerateContent", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
					c.Params = gin.Params{{Key: "modelAction", Value: "/fixture:streamGenerateContent"}}
					calls := 0
					ServeGeminiViaChatCompletions(c, func(c *gin.Context) {
						calls++
						c.Header("Content-Type", "text/event-stream")
						for start := 0; start < len(wire); start += step {
							_, err := c.Writer.Write([]byte(wire[start:min(start+step, len(wire))]))
							require.NoError(t, err)
						}
					})
					require.Equal(t, 1, calls)
					var texts, terminals, usages int
					for _, line := range strings.Split(recorder.Body.String(), "\n") {
						if data, ok := strings.CutPrefix(line, "data: "); ok {
							require.True(t, json.Valid([]byte(data)))
							if gjson.Get(data, "candidates.0.content.parts.0.text").String() == "你好" {
								texts++
							}
							if gjson.Get(data, "candidates.0.finishReason").String() == "STOP" {
								terminals++
							}
							if gjson.Get(data, "usageMetadata.totalTokenCount").Int() == 7 {
								usages++
							}
						}
					}
					require.Equal(t, 1, texts, recorder.Body.String())
					require.Equal(t, 1, terminals)
					require.Equal(t, 1, usages)
				})
			}
		}
	}
}

func TestGeminiBridgeDeliversBeforeUpstreamFinishes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	proceed := make(chan struct{})
	var calls atomic.Int32
	router := gin.New()
	router.POST("/v1beta/models/*modelAction", func(c *gin.Context) {
		ServeGeminiViaChatCompletions(c, func(c *gin.Context) {
			calls.Add(1)
			c.Header("Content-Type", "text/event-stream")
			_, _ = io.WriteString(c.Writer, ": heartbeat\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"first\"}}]}\n\n")
			c.Writer.Flush()
			select {
			case <-proceed:
			case <-c.Request.Context().Done():
				return
			}
			_, _ = io.WriteString(c.Writer, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		})
	})
	server := httptest.NewServer(router)
	defer server.Close()
	defer close(proceed)
	client := server.Client()
	client.Timeout = 5 * time.Second
	response, err := client.Post(server.URL+"/v1beta/models/fixture:streamGenerateContent", "application/json", strings.NewReader(`{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`))
	require.NoError(t, err)
	defer response.Body.Close()
	scanner := bufio.NewScanner(response.Body)
	var visible bool
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"text":"first"`) {
			visible = true
			break
		}
	}
	require.NoError(t, scanner.Err())
	require.True(t, visible)
	require.Equal(t, int32(1), calls.Load())
}

type geminiFailureWriter struct {
	gin.ResponseWriter
	writes    int
	flushes   int
	failWrite bool
}

func (w *geminiFailureWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.failWrite {
		return 0, io.ErrClosedPipe
	}
	return w.ResponseWriter.Write(p)
}

func (w *geminiFailureWriter) FlushError() error { w.flushes++; return io.ErrClosedPipe }

func TestGeminiBridgeStopsAfterWriteAndFlushFailures(t *testing.T) {
	for _, failWrite := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		base := &geminiFailureWriter{ResponseWriter: c.Writer, failWrite: failWrite}
		w := newGeminiChatResponseWriter(base, "fixture")
		w.Header().Set("Content-Type", "text/event-stream")
		_, err := w.Write([]byte(": heartbeat\n\n"))
		require.ErrorIs(t, err, io.ErrClosedPipe)
		writes, flushes := base.writes, base.flushes
		_, err = w.Write([]byte("data: {}\n\n"))
		require.ErrorIs(t, err, io.ErrClosedPipe)
		w.finish()
		require.Equal(t, writes, base.writes)
		require.Equal(t, flushes, base.flushes)
	}
}

func TestGeminiBridgeProtocolFailureAndCleanTail(t *testing.T) {
	for _, wire := range []string{"data: {\n\n", "data: {", "data: {\"error\":{\"message\":\"upstream secret\"}}\n\n"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		w := newGeminiChatResponseWriter(c.Writer, "fixture")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(wire))
		w.finish()
		require.Error(t, w.err)
		require.Equal(t, 1, strings.Count(recorder.Body.String(), `"error"`))
		require.NotContains(t, recorder.Body.String(), "upstream secret")
		require.True(t, w.Written())
		_, err := w.Write([]byte("data: {}\n\n"))
		require.True(t, errors.Is(err, w.err))
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	w := newGeminiChatResponseWriter(c.Writer, "fixture")
	w.Header().Set("Content-Type", "text/event-stream")
	_, err := w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"content":"tail"},"finish_reason":"stop"}]}`))
	require.NoError(t, err)
	require.Empty(t, recorder.Body.String())
	w.finish()
	require.NoError(t, w.err)
	require.Contains(t, recorder.Body.String(), `"text":"tail"`)
	previous := recorder.Body.String()
	w.finish()
	require.Equal(t, previous, recorder.Body.String())
}
