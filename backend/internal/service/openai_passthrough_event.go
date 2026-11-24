package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/tidwall/gjson"
)

// The relay writes the original lines. Protocol decisions consume the joined
// data fields exactly once, even when event: follows the data fields.
var errPassthroughPluginEventTooLarge = errors.New("plugin SSE event exceeds guarded staging limit")

type openAIPassthroughEvent struct {
	// Common events need no allocation for line storage. Pack extra lines so
	// a long run of tiny comments does not allocate a slice header per line.
	head       [4]string
	packed     strings.Builder
	remaining  string
	count      int
	line       string
	index      int
	firstData  int
	dataFields int
	payload    string
	eventType  string
	validJSON  bool
	rewritten  bool
	delimited  bool
	lookahead  string
	exhausted  bool
	err        error
}

func (e *openAIPassthroughEvent) appendLine(line string) {
	if e.count < len(e.head) {
		e.head[e.count] = line
	} else {
		e.packed.WriteString(line)
		e.packed.WriteByte('\n')
	}
	e.count++
}

func (e *openAIPassthroughEvent) advance() {
	if e.index < len(e.head) {
		e.line = e.head[e.index]
	} else {
		e.line, e.remaining, _ = strings.Cut(e.remaining, "\n")
	}
	e.index++
}

func (e *openAIPassthroughEvent) next(read func() (string, bool), limit int, pluginBudget int64, pluginCommitted bool) bool {
	if e.index < e.count {
		e.advance()
		return true
	}
	if e.exhausted || e.err != nil {
		return false
	}
	lookahead := e.lookahead
	*e = openAIPassthroughEvent{firstData: -1}
	var data strings.Builder
	size := 0
	for {
		line := lookahead
		lookahead = ""
		if line == "" {
			var ok bool
			line, ok = read()
			if !ok {
				e.exhausted = true
				break
			}
		}
		if line == "" {
			e.appendLine(line)
			e.delimited = true
			break
		}
		value, isData := extractOpenAISSEDataLine(line)
		if isData && e.dataFields == 1 && (strings.HasPrefix(value, "{") || value == "[DONE]") &&
			strings.HasSuffix(strings.TrimSpace(e.payload), "}") &&
			gjson.Valid(e.payload) && gjson.Get(e.payload, "type").Exists() &&
			(value == "[DONE]" || (gjson.Valid(value) && gjson.Get(value, "type").Exists())) {
			// Some providers omit the separator between complete Responses objects.
			// Keep the original wire and process each independent object once.
			e.lookahead = line
			break
		}
		if len(line) >= limit-size {
			e.err = bufio.ErrTooLong
			return false
		}
		size += len(line) + 1
		if name, ok := extractOpenAISSEEventLine(line); ok {
			e.eventType = name
		}
		if isData {
			switch e.dataFields {
			case 0:
				e.firstData = e.count
				e.payload = value
			case 1:
				data.WriteString(e.payload)
				data.WriteByte('\n')
				data.WriteString(value)
			default:
				data.WriteByte('\n')
				data.WriteString(value)
			}
			e.dataFields++
		}
		if pluginBudget >= 0 && int64(size) > pluginBudget {
			// Prior to metadata commitment a complete first output can exceed
			// the staging budget. Incomplete events must remain bounded.
			payload := e.payload
			if e.dataFields > 1 {
				payload = data.String()
			}
			if pluginCommitted || !isData || !gjson.Valid(payload) || !openAIStreamDataStartsClientOutput(payload, effectiveOpenAISSEEventType([]byte(payload), e.eventType)) {
				e.err = errPassthroughPluginEventTooLarge
				return false
			}
			pluginBudget = -1
		}
		e.appendLine(line)
	}
	if e.dataFields > 1 {
		e.payload = data.String()
	}
	if e.count == 0 {
		return false
	}
	e.remaining = e.packed.String()
	e.advance()
	return true
}

// Modified multiline JSON must remain a legal SSE data field. The other data
// lines are then redundant; comments, id and other fields still pass through.
func (e *openAIPassthroughEvent) rewrite(data []byte) (string, error) {
	if e.dataFields > 1 {
		var compact bytes.Buffer
		if err := json.Compact(&compact, data); err != nil {
			return "", err
		}
		data = compact.Bytes()
	}
	e.rewritten = true
	return "data: " + string(data), nil
}
