package service

import (
	"iter"
	"strings"

	"github.com/tidwall/gjson"
)

// Buffered bodies already have a read limit. Slice their physical SSE lines
// in place, consuming a BOM only at the start and treating CRLF as one ending.
func bufferedOpenAISSELines[T string | []byte](body T) iter.Seq2[int, T] {
	return func(yield func(int, T) bool) {
		start := 0
		if len(body) >= 3 && string(body[:3]) == "\ufeff" {
			start = 3
		}
		for i := start; i < len(body); i++ {
			if body[i] != '\n' && body[i] != '\r' {
				continue
			}
			line := body[start:i]
			if body[i] == '\r' && i+1 < len(body) && body[i+1] == '\n' {
				i++
			}
			start = i + 1
			if !yield(start, line) {
				return
			}
		}
		if start < len(body) {
			yield(len(body), body[start:])
		}
	}
}

type openAISSEDataAccumulator struct {
	lines []string
}

func (a *openAISSEDataAccumulator) AddLine(line string, fn func([]byte)) {
	if fn == nil {
		return
	}
	trimmedLine := strings.TrimRight(line, "\r\n")
	if data, ok := extractOpenAISSEDataLine(trimmedLine); ok {
		a.lines = append(a.lines, data)
		return
	}
	if strings.TrimSpace(trimmedLine) == "" {
		a.Flush(fn)
	}
}

func (a *openAISSEDataAccumulator) Flush(fn func([]byte)) {
	if fn == nil || len(a.lines) == 0 {
		return
	}
	emitOpenAISSEDataPayloads(a.lines, fn)
	a.lines = a.lines[:0]
}

func forEachOpenAISSEDataPayload(body string, fn func([]byte)) {
	if fn == nil || strings.TrimSpace(body) == "" {
		return
	}
	var acc openAISSEDataAccumulator
	for _, line := range bufferedOpenAISSELines(body) {
		acc.AddLine(line, fn)
	}
	acc.Flush(fn)
}

func forEachOpenAISSEFrame(body string, fn func(string, []byte)) {
	if fn == nil || strings.TrimSpace(body) == "" {
		return
	}
	var parser openAICompatSSEFrameParser
	emit := func(frame openAICompatSSEFrame, ok bool) {
		if !ok {
			return
		}
		emitData := func(value string) {
			value = strings.TrimSpace(value)
			if value == "" || value == "[DONE]" {
				return
			}
			data := []byte(value)
			fn(effectiveOpenAISSEEventType(data, frame.EventType), data)
		}
		if !strings.Contains(frame.Data, "\n") || gjson.Valid(frame.Data) {
			emitData(frame.Data)
			return
		}
		// Some upstreams omit blank separators between complete JSON events.
		// A broken multiline event must not publish one of its fragments as a terminal.
		for value := range strings.SplitSeq(frame.Data, "\n") {
			value = strings.TrimSpace(value)
			if value != "" && value != "[DONE]" && !gjson.Valid(value) {
				emitData(frame.Data)
				return
			}
		}
		for value := range strings.SplitSeq(frame.Data, "\n") {
			emitData(value)
		}
	}
	for _, line := range bufferedOpenAISSELines(body) {
		emit(parser.AddLine(line))
	}
	emit(parser.Finish())
}

func emitOpenAISSEDataPayloads(lines []string, fn func([]byte)) {
	if fn == nil || len(lines) == 0 {
		return
	}
	if len(lines) == 1 {
		emitOpenAISSEDataPayload(lines[0], fn)
		return
	}
	joined := strings.Join(lines, "\n")
	if gjson.Valid(joined) {
		emitOpenAISSEDataPayload(joined, fn)
		return
	}
	for _, line := range lines {
		emitOpenAISSEDataPayload(line, fn)
	}
}

func emitOpenAISSEDataPayload(data string, fn func([]byte)) {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	fn([]byte(data))
}
