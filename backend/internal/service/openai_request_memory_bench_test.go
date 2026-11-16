package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
)

// Synthetic bodies only. Keep this fixture and command identical for before/after
// comparisons; no model calls or client contents are used.
func memoryBenchmarkBody(size int, shape string) []byte {
	prefix := []byte("{\"type\":\"response.create\",\"model\":\"gpt-6.1-sol\",\"input\":[")
	var item []byte
	switch shape {
	case "dense":
		item = []byte("{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"x\"}]}")
	case "image":
		prefix = append(prefix, []byte("{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_image\",\"image_url\":\"data:image/png;base64,")...)
		suffix := []byte("\"}]}]}")
		return append(append(prefix, bytes.Repeat([]byte("A"), size-len(prefix)-len(suffix))...), suffix...)
	default:
		prefix = append(prefix, []byte("{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"")...)
		suffix := []byte("\"}]}]}")
		return append(append(prefix, bytes.Repeat([]byte("x"), size-len(prefix)-len(suffix))...), suffix...)
	}
	body := make([]byte, 0, size)
	body = append(body, prefix...)
	for len(body)+len(item)+3 <= size {
		if len(body) > len(prefix) {
			body = append(body, ',')
		}
		body = append(body, item...)
	}
	return append(body, ']', '}')
}

func BenchmarkOpenAIRequestMemory(b *testing.B) {
	for _, size := range []int{32 << 20, 64 << 20} {
		for _, shape := range []string{"text", "image", "dense"} {
			body := memoryBenchmarkBody(size, shape)
			for _, path := range []string{"bridge", "responses_chat", "responses_oauth"} {
				b.Run(fmt.Sprintf("%s/%s/%dMiB", path, shape, size>>20), func(b *testing.B) {
					runtime.GC()
					var before runtime.MemStats
					runtime.ReadMemStats(&before)
					var peak atomic.Uint64
					peak.Store(before.HeapAlloc)
					done, joined := make(chan struct{}), make(chan struct{})
					go func() {
						defer close(joined)
						ticker := time.NewTicker(time.Millisecond)
						defer ticker.Stop()
						for {
							select {
							case <-done:
								return
							case <-ticker.C:
								var m runtime.MemStats
								runtime.ReadMemStats(&m)
								for old := peak.Load(); m.HeapAlloc > old && !peak.CompareAndSwap(old, m.HeapAlloc); old = peak.Load() {
								}
							}
						}
					}()
					b.ReportAllocs()
					b.SetBytes(int64(len(body)))
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						var out []byte
						var err error
						if path == "bridge" {
							out, err = prepareOpenAIWSHTTPBridgeSessionBody(nil, body, false)
						} else if path == "responses_oauth" {
							var req map[string]any
							err = decodeOpenAIJSONUseNumber(body, &req)
							if err == nil {
								result := applyCodexOAuthTransformWithOptions(req, codexOAuthTransformOptions{IsCodexCLI: true})
								err = result.Error
								if err == nil {
									out, err = json.Marshal(req)
								}
							}
						} else {
							var req apicompat.ResponsesRequest
							err = json.Unmarshal(body, &req)
							if err == nil {
								var chat *apicompat.ChatCompletionsRequest
								chat, err = apicompat.ResponsesToChatCompletionsRequestWithOptions(&req, nil)
								if err == nil {
									out, err = json.Marshal(chat)
								}
							}
						}
						if err != nil {
							b.Fatal(err)
						}
						runtime.KeepAlive(out)
					}
					b.StopTimer()
					close(done)
					<-joined
					var after runtime.MemStats
					runtime.ReadMemStats(&after)
					observed := peak.Load()
					if after.HeapAlloc > observed {
						observed = after.HeapAlloc
					}
					b.ReportMetric(float64(observed-before.HeapAlloc)/float64(len(body)), "peak-extra/input")
					copies := OpenAINativeProcessingCopies
					if path == "responses_chat" {
						copies = openAIConversionProcessingCopies
					}
					b.ReportMetric(float64(pkghttputil.RequestBodyProcessingEstimate(body, copies))/float64(len(body)), "reserved/input")
					runtime.KeepAlive(body)
				})
			}
		}
	}
}
