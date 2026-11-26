package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tidwall/gjson"
)

// A CR ends a line immediately. Its optional LF may arrive on a later read.
func newOpenAIResponsesLineSplit() bufio.SplitFunc {
	var skipLF bool
	var scanned int
	return func(data []byte, atEOF bool) (int, []byte, error) {
		if skipLF && len(data) > 0 {
			skipLF = false
			if data[0] == '\n' {
				return 1, nil, nil
			}
		}
		if i := bytes.IndexAny(data[scanned:], "\r\n"); i >= 0 {
			i += scanned
			scanned = 0
			skipLF = data[i] == '\r'
			return i + 1, data[:i], nil
		}
		scanned = len(data)
		if atEOF && len(data) > 0 {
			scanned = 0
			return len(data), data, nil
		}
		return 0, nil, nil
	}
}

// The physical-line configuration belongs to the scanner. Bound the separate
// allocation needed to observe a logical event, including its slice entries.
func openAIResponsesFrameLimit(maxLineSize int) int64 {
	if maxLineSize <= 0 {
		maxLineSize = defaultMaxLineSize
	}
	return int64(maxLineSize) + openAIFirstOutputStageMaxBytes
}

type openAIResponsesFrame struct {
	lines        []string
	payload      string
	eventType    string
	dataIndex    int
	delimited    bool
	undispatched bool // A read error interrupted this event before its delimiter.
}

type openAIResponsesFrameReader struct {
	lines []string
	data  []string
	name  string
	first int
	size  int64
	limit int64
}

func responsesDataField(line string) (string, bool) {
	if line == "data" {
		return "", true
	}
	value, ok := strings.CutPrefix(line, "data:")
	return strings.TrimPrefix(value, " "), ok
}

func (r *openAIResponsesFrameReader) emit(delimited bool, emit func(*openAIResponsesFrame)) {
	frame := &openAIResponsesFrame{lines: r.lines, dataIndex: -1, eventType: r.name, delimited: delimited}
	if len(r.data) > 0 {
		frame.dataIndex = r.first
		frame.payload = strings.Join(r.data, "\n")
	}
	*r = openAIResponsesFrameReader{limit: r.limit}
	emit(frame)
}

func (r *openAIResponsesFrameReader) push(line string, emit func(*openAIResponsesFrame)) error {
	if line == "" {
		r.emit(true, emit)
		return nil
	}
	value, data := responsesDataField(line)
	// Compact compatible upstreams sometimes put complete typed JSON objects
	// on adjacent data lines. Two such objects cannot be one valid JSON value.
	if data && len(r.data) == 1 && (strings.HasPrefix(value, "{") || value == "[DONE]") &&
		strings.HasSuffix(strings.TrimSpace(r.data[0]), "}") && gjson.Valid(r.data[0]) && gjson.Get(r.data[0], "type").Exists() &&
		(value == "[DONE]" || (gjson.Valid(value) && gjson.Get(value, "type").Exists())) {
		r.emit(false, emit)
	}
	limit := r.limit
	if limit <= 0 {
		limit = openAIResponsesFrameLimit(0)
	}
	if r.size+int64(len(line))+1+int64(len(r.lines)+len(r.data)+2)*16 > limit {
		return errors.New("Responses SSE event exceeds assembly limit")
	}
	r.size += int64(len(line)) + 1
	if data {
		if len(r.data) == 0 {
			r.first = len(r.lines)
		}
		r.data = append(r.data, value)
	} else if line == "event" {
		r.name = ""
	} else if name, ok := extractOpenAISSEEventLine(line); ok {
		r.name = name
	}
	r.lines = append(r.lines, line)
	return nil
}

func (r *openAIResponsesFrameReader) finish(cleanEOF bool, emit func(*openAIResponsesFrame)) {
	if len(r.lines) > 0 {
		if cleanEOF {
			r.emit(false, emit)
		} else if len(r.data) > 0 {
			// Preserve an already-read, complete content delta on read error. A
			// truncation cannot establish an unterminated success or failure.
			payload := strings.Join(r.data, "\n")
			kind := effectiveOpenAISSEEventType([]byte(payload), r.name)
			if gjson.Valid(payload) {
				r.emit(false, func(frame *openAIResponsesFrame) {
					frame.undispatched = kind == "error" || openAIStreamEventTypeIsTerminal(kind) || !openAIStreamDataStartsClientOutput(payload, kind)
					emit(frame)
				})
			}
		}
	}
	*r = openAIResponsesFrameReader{limit: r.limit}
}

var errOpenAIResponsesPluginStage = errors.New("plugin SSE event exceeds guarded staging limit")

func (r *openAIResponsesFrameReader) read(readLine func() (string, bool), cleanEOF func() bool, budget int64, committed bool) (*openAIResponsesFrame, error) {
	var frame *openAIResponsesFrame
	capture := func(f *openAIResponsesFrame) { frame = f }
	for frame == nil {
		line, ok := readLine()
		if !ok {
			r.finish(cleanEOF(), capture)
			break
		}
		if err := r.push(line, capture); err != nil {
			return nil, err
		}
		if budget >= 0 && r.size > budget {
			// The existing pre-output path permits a complete large first output
			// to go directly to the client. Metadata and partial events stay bounded.
			payload := strings.Join(r.data, "\n")
			if committed || !gjson.Valid(payload) || !openAIStreamDataStartsClientOutput(payload, effectiveOpenAISSEEventType([]byte(payload), r.name)) {
				return nil, errOpenAIResponsesPluginStage
			}
			budget = -1
		}
	}
	return frame, nil
}

func openAIResponsesTerminalSuccessful(payload []byte, eventType string) bool {
	if eventType == "response.completed" || eventType == "response.done" {
		return openAIProtectionSuccessfulTerminal(payload, eventType)
	}
	return IsSuccessfulStreamTerminal(payload)
}

// Render changed JSON as a single data field. Newline-bearing JSON must be
// compacted before it can be written as a physical SSE line.
func openAIResponsesRewrittenLine(data []byte) (string, error) {
	if bytes.IndexAny(data, "\r\n") >= 0 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, data); err != nil {
			return "", err
		}
		data = compact.Bytes()
	}
	return "data: " + string(data), nil
}
