package requesttiming

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func BenchmarkDeliveredSSE(b *testing.B) {
	for _, protocol := range []string{"gemini", "chat", "responses"} {
		for _, size := range []int{32, 4096} {
			text := strings.Repeat("x", size)
			var payload string
			switch protocol {
			case "gemini":
				payload = `{"candidates":[{"content":{"parts":[{"text":"` + text + `"}]}}]}`
			case "chat":
				payload = `{"choices":[{"delta":{"content":"` + text + `"}}]}`
			case "responses":
				payload = `{"type":"response.output_text.delta","delta":"` + text + `"}`
			}
			wire := []byte("data: " + payload + "\n\n")
			b.Run(fmt.Sprintf("%s/text_%d", protocol, size), func(b *testing.B) {
				ctx := With(context.Background(), New(time.Now(), 0))
				b.ReportAllocs()
				for b.Loop() {
					DeliveredSSE(ctx, wire)
				}
			})
		}
	}
}
