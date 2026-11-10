// Package clienterror removes diagnostic request identifiers at client error
// boundaries. Callers retain the original payload for logging and scheduling.
package clienterror

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const requestIDLabel = `(?:(?:upstream[\s_-]*)?request[\s_-]*id|(?:上游\s*)?请求\s*(?:id|编号|标识符?))`

var (
	parenthesizedID = regexp.MustCompile(`(?i)[\t ]*[（(]\s*` + requestIDLabel + `\s*[:：=]\s*[^\s()（）\[\]【】,;，；]+\s*[)）]`)
	bracketedID     = regexp.MustCompile(`(?i)[\t ]*[\[【]\s*` + requestIDLabel + `\s*[:：=]\s*[^\s()（）\[\]【】,;，；]+\s*[\]】]`)
	bareID          = regexp.MustCompile(`(?i)\b` + requestIDLabel + `\s*[:：=]\s*[^\s()（）\[\]【】,;，；]+`)
	bareChineseID   = regexp.MustCompile(`(?:上游\s*)?请求\s*(?:[iI][dD]|编号|标识符?)\s*[:：=]\s*[^\s()（）\[\]【】,;，；]+`)
)

// Message removes request-ID annotations from a client-visible error message,
// including repeated annotations and JSON encoded inside an error message.
func Message(message string) string {
	trimmed := strings.TrimSpace(message)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		if gjson.Valid(message) {
			clean, _ := cleanJSON([]byte(message), true)
			return string(clean)
		}
	}
	clean := parenthesizedID.ReplaceAllString(message, "")
	clean = bracketedID.ReplaceAllString(clean, "")
	clean = bareID.ReplaceAllString(clean, "")
	clean = bareChineseID.ReplaceAllString(clean, "")
	if clean == message {
		return message
	}
	clean = strings.TrimSpace(clean)
	if clean == "" {
		return "Request failed"
	}
	return clean
}

// JSON cleans a recognized error envelope. Successful payloads and ordinary
// model/tool output are untouched. The bool reports whether bytes changed.
func JSON(payload []byte) ([]byte, bool) {
	return cleanJSON(payload, false)
}

// ErrorJSON cleans a body already identified as an error by its owning HTTP
// status or protocol boundary. It also accepts bare {message, request_id}
// objects that lack an error discriminator.
func ErrorJSON(payload []byte) ([]byte, bool) {
	return cleanJSON(payload, true)
}

func cleanJSON(payload []byte, messageJSON bool) ([]byte, bool) {
	if !gjson.ValidBytes(payload) {
		return payload, false
	}
	root := gjson.ParseBytes(payload)
	if messageJSON && root.Type == gjson.String {
		if clean := Message(root.String()); clean != root.String() {
			return []byte(strconv.Quote(clean)), true
		}
	}
	if !root.IsObject() && !root.IsArray() {
		return payload, false
	}
	if !messageJSON && !errorEnvelope(root) {
		return payload, false
	}
	type patch struct {
		path   string
		remove bool
		value  string
	}
	var patches []patch
	var walk func(gjson.Result, string, bool)
	walk = func(node gjson.Result, path string, inError bool) {
		if node.IsArray() {
			for index, child := range node.Array() {
				walk(child, joinPath(path, strconv.Itoa(index)), inError)
			}
			return
		}
		if !node.IsObject() {
			if node.Type == gjson.String && inError && path != "" {
				if clean := Message(node.String()); clean != node.String() {
					patches = append(patches, patch{path: path, value: clean})
				}
			}
			return
		}
		node.ForEach(func(key, value gjson.Result) bool {
			name := normalizeKey(key.String())
			childPath := joinPath(path, escapePath(key.String()))
			if requestIDKey(name) {
				patches = append(patches, patch{path: childPath, remove: true})
				return true
			}
			// A failed response can still contain real model/tool output. Do not
			// inspect or rewrite user-owned text, arguments, or tool schemas.
			if !inError && outputKey(name) {
				return true
			}
			childInError := inError || name == "error" || name == "errors"
			if value.Type == gjson.String && (childInError || name == "message" || name == "detail" || name == "details") {
				if clean := Message(value.String()); clean != value.String() {
					patches = append(patches, patch{path: childPath, value: clean})
				}
				return true
			}
			walk(value, childPath, childInError)
			return true
		})
	}
	walk(root, "", false)
	if len(patches) == 0 {
		return payload, false
	}
	updated := payload
	for _, p := range patches {
		var err error
		if p.remove {
			updated, err = sjson.DeleteBytes(updated, p.path)
		} else {
			updated, err = sjson.SetBytes(updated, p.path, p.value)
		}
		if err != nil {
			return payload, false
		}
	}
	return updated, true
}

func errorEnvelope(root gjson.Result) bool {
	if !root.IsObject() {
		return false
	}
	switch root.Get("type").String() {
	case "error", "response.failed":
		return true
	}
	for _, path := range []string{"status", "response.status"} {
		if root.Get(path).String() == "failed" {
			return true
		}
	}
	for _, path := range []string{"error", "response.error"} {
		if value := root.Get(path); value.Exists() && value.Type != gjson.Null {
			return true
		}
	}
	return false
}

func requestIDKey(key string) bool {
	switch key {
	case "requestid", "upstreamrequestid", "xrequestid", "clientrequestid", "xclientrequestid":
		return true
	}
	return false
}

func outputKey(key string) bool {
	switch key {
	case "output", "input", "content", "tools", "toolcalls", "functioncall", "arguments", "instructions":
		return true
	}
	return false
}

func normalizeKey(key string) string {
	return strings.ToLower(strings.NewReplacer("_", "", "-", "", " ", "").Replace(key))
}

func escapePath(key string) string {
	return strings.NewReplacer(`\`, `\\`, `.`, `\.`, `:`, `\:`).Replace(key)
}

func joinPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}
