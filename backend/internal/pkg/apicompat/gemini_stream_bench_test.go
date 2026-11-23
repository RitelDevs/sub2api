package apicompat

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func geminiToolBenchmarkFrames(argumentBytes, fragmentBytes int) [][]byte {
	arguments := `{"text":"` + strings.Repeat("x", argumentBytes) + `","n":9007199254740993}`
	var frames [][]byte
	for offset := 0; offset < len(arguments); offset += fragmentBytes {
		end := min(offset+fragmentBytes, len(arguments))
		chunk := ChatCompletionsChunk{Choices: []ChatChunkChoice{{
			Delta: ChatDelta{ToolCalls: []ChatToolCall{{Function: ChatFunctionCall{
				Name: "lookup", Arguments: arguments[offset:end],
			}}}},
		}}}
		wire, err := json.Marshal(chunk)
		if err != nil {
			panic(err)
		}
		frames = append(frames, append(append([]byte("data: "), wire...), '\n', '\n'))
	}
	return append(frames, []byte("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n"))
}

func BenchmarkGeminiStreamToolFragments(b *testing.B) {
	for _, size := range []int{1024, 32 * 1024, 256 * 1024} {
		for _, fragment := range []int{64, 1024} {
			b.Run(fmt.Sprintf("args_%d/fragment_%d", size, fragment), func(b *testing.B) {
				frames := geminiToolBenchmarkFrames(size, fragment)
				b.ReportAllocs()
				b.SetBytes(int64(size))
				b.ResetTimer()
				for b.Loop() {
					translator := NewGeminiSSETranslator("fixture")
					for _, frame := range frames {
						translator.Push(frame)
					}
					translator.Flush()
					if translator.Err() != nil {
						b.Fatal(translator.Err())
					}
				}
			})
		}
	}
}

func BenchmarkGeminiStreamText(b *testing.B) {
	for _, size := range []int{32, 4096} {
		b.Run(fmt.Sprintf("text_%d", size), func(b *testing.B) {
			frame := []byte(`data: {"choices":[{"index":0,"delta":{"content":"` + strings.Repeat("x", size) + `"}}]}` + "\n\n")
			translator := NewGeminiSSETranslator("fixture")
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			for b.Loop() {
				if len(translator.Push(frame)) == 0 || translator.Err() != nil {
					b.Fatal("text event was not delivered", translator.Err())
				}
			}
		})
	}
}
