package apicompat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// GeminiGenerateContentToChatCompletions 把 Gemini generateContent 请求体转成
// Chat Completions 请求体。model 来自 URL，Gemini 请求体本身不带 model。
// 转换结果交给现有 Chat Completions 入口后，再由上游协议分流决定真正的上游端点。
func GeminiGenerateContentToChatCompletions(model string, body []byte, stream bool) ([]byte, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("model is required")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, fmt.Errorf("request body is empty")
	}
	if !gjson.ValidBytes(body) {
		return nil, fmt.Errorf("failed to parse request body")
	}
	if cached := strings.TrimSpace(firstJSONString(body, "cachedContent", "cached_content")); cached != "" {
		return nil, fmt.Errorf("explicit cached content is not supported for this upstream")
	}

	contents := gjson.GetBytes(body, "contents")
	if !contents.Exists() || !contents.IsArray() {
		return nil, fmt.Errorf("contents is required")
	}

	messages := make([]any, 0, contents.Get("#").Int()+1)
	if system := geminiInstructionText(body, "systemInstruction", "system_instruction"); system != "" {
		messages = append(messages, map[string]any{"role": "system", "content": system})
	}

	var pending geminiPendingCalls
	var contentErr error
	contents.ForEach(func(_, content gjson.Result) bool {
		role := strings.ToLower(strings.TrimSpace(content.Get("role").String()))
		if role == "" {
			role = "user"
		}
		converted, err := geminiContentToChatMessages(role, content.Get("parts"), &pending)
		if err != nil {
			contentErr = err
			return false
		}
		messages = append(messages, converted...)
		return true
	})
	if contentErr != nil {
		return nil, contentErr
	}
	onlySystem := false
	if len(messages) == 1 {
		msg, ok := messages[0].(map[string]any)
		onlySystem = ok && msg["role"] == "system"
	}
	if len(messages) == 0 || onlySystem {
		return nil, fmt.Errorf("contents is required")
	}

	out := map[string]any{
		"model":    model,
		"messages": messages,
		"stream":   stream,
	}
	if stream {
		out["stream_options"] = map[string]any{"include_usage": true}
	}
	tools, err := geminiToolsToChatTools(body)
	if err != nil {
		return nil, err
	}
	if len(tools) > 0 {
		mode, allowed := geminiFunctionCallingConfig(body)
		// NONE 不调用工具，允许名单不必收窄。其余模式只把名单里的函数发给上游。
		if len(allowed) > 0 && mode != "NONE" {
			tools = filterGeminiChatTools(tools, allowed)
			if len(tools) == 0 {
				return nil, fmt.Errorf("allowedFunctionNames did not match any declared tool")
			}
		}
		out["tools"] = tools
		if choice := geminiToolChoiceValue(mode, allowed); choice != nil {
			out["tool_choice"] = choice
		}
	}
	if err := applyGeminiGenerationConfig(body, out); err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("marshal chat completions request: %w", err)
	}
	return encoded, nil
}

// EstimateGeminiTotalTokens 按文本长度估算 countTokens。桥接分组没有 Gemini
// countTokens 上游，避免为此再打一次生成请求。
func EstimateGeminiTotalTokens(body []byte) (tokens int, characters int) {
	if !gjson.ValidBytes(body) {
		return 0, 0
	}
	var b strings.Builder
	appendText := func(text string) {
		if text == "" {
			return
		}
		if b.Len() > 0 {
			_ = b.WriteByte('\n')
		}
		_, _ = b.WriteString(text)
	}
	appendText(geminiInstructionText(body, "systemInstruction", "system_instruction"))
	gjson.GetBytes(body, "contents").ForEach(func(_, content gjson.Result) bool {
		content.Get("parts").ForEach(func(_, part gjson.Result) bool {
			appendText(part.Get("text").String())
			if call := geminiObject(part, "functionCall", "function_call"); call.Exists() {
				appendText(call.Get("name").String())
				appendText(call.Get("args").Raw)
			}
			if resp := geminiObject(part, "functionResponse", "function_response"); resp.Exists() {
				appendText(resp.Get("name").String())
				appendText(resp.Get("response").Raw)
			}
			return true
		})
		return true
	})
	characters = utf8.RuneCountInString(b.String())
	if characters == 0 {
		return 0, 0
	}
	tokens = (characters + 3) / 4
	return tokens, characters
}

type geminiPendingCall struct {
	name     string
	sourceID string
}

type geminiPendingCalls struct {
	byChatID   map[string]geminiPendingCall
	bySourceID map[string]string
	next       int
}

func geminiContentToChatMessages(role string, parts gjson.Result, pending *geminiPendingCalls) ([]any, error) {
	var textParts []string
	var mixedParts []map[string]any
	var toolCalls []any
	var toolMessages []any
	var partErr error
	parts.ForEach(func(_, part gjson.Result) bool {
		if part.Get("thought").Bool() {
			return true
		}
		if text := part.Get("text").String(); text != "" {
			if mixedParts == nil {
				textParts = append(textParts, text)
			} else {
				mixedParts = append(mixedParts, map[string]any{"type": "text", "text": text})
			}
		}
		mediaPart, err := geminiInlineDataToChatPart(part)
		if err != nil {
			partErr = err
			return false
		}
		if mediaPart != nil {
			if mixedParts == nil {
				for _, text := range textParts {
					mixedParts = append(mixedParts, map[string]any{"type": "text", "text": text})
				}
				textParts = nil
			}
			mixedParts = append(mixedParts, mediaPart)
		}
		if call := geminiObject(part, "functionCall", "function_call"); call.Exists() {
			name := strings.TrimSpace(call.Get("name").String())
			id, err := pending.add(name, call.Get("id"))
			if err != nil {
				partErr = err
				return false
			}
			args := "{}"
			if raw := strings.TrimSpace(call.Get("args").Raw); raw != "" && raw != "null" {
				args = raw
			}
			toolCalls = append(toolCalls, map[string]any{
				"id":   id,
				"type": "function",
				"function": map[string]any{
					"name":      name,
					"arguments": args,
				},
			})
		}
		if resp := geminiObject(part, "functionResponse", "function_response"); resp.Exists() {
			if len(textParts) > 0 || len(mixedParts) > 0 {
				partErr = fmt.Errorf("Chat requires all tool responses before user content; cannot preserve Gemini part order")
				return false
			}
			if resp.Get("parts.#").Int() > 0 {
				partErr = fmt.Errorf("multimodal functionResponse.parts cannot be represented in a Chat tool message")
				return false
			}
			name := strings.TrimSpace(resp.Get("name").String())
			id, err := pending.take(name, resp.Get("id"))
			if err != nil {
				partErr = err
				return false
			}
			content := geminiFunctionResponseContent(resp.Get("response"))
			toolMessages = append(toolMessages, map[string]any{
				"role":         "tool",
				"tool_call_id": id,
				"content":      content,
			})
		}
		return true
	})
	if partErr != nil {
		return nil, partErr
	}

	var out []any
	chatRole := "user"
	if role == "model" || role == "assistant" {
		chatRole = "assistant"
	}
	if chatRole == "assistant" || len(toolMessages) == 0 {
		if msg := geminiTextMessage(chatRole, textParts, mixedParts, toolCalls); msg != nil {
			out = append(out, msg)
		}
		out = append(out, toolMessages...)
		return out, nil
	}
	// 用户回合里的 functionResponse 在 Chat Completions 里必须是独立的 tool 消息。
	out = append(out, toolMessages...)
	if msg := geminiTextMessage("user", textParts, mixedParts, nil); msg != nil {
		out = append(out, msg)
	}
	return out, nil
}

func geminiTextMessage(role string, textParts []string, mixedParts []map[string]any, toolCalls []any) map[string]any {
	if len(textParts) == 0 && len(mixedParts) == 0 && len(toolCalls) == 0 {
		return nil
	}
	msg := map[string]any{"role": role}
	switch {
	case len(mixedParts) > 0:
		msg["content"] = mixedParts
	case len(textParts) > 0:
		msg["content"] = strings.Join(textParts, "\n")
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	return msg
}

func geminiInlineDataToChatPart(part gjson.Result) (map[string]any, error) {
	inline := geminiObject(part, "inlineData", "inline_data")
	if !inline.Exists() {
		file := geminiObject(part, "fileData", "file_data")
		if !file.Exists() {
			return nil, nil
		}
		uri := strings.TrimSpace(file.Get("fileUri").String())
		if uri == "" {
			uri = strings.TrimSpace(file.Get("file_uri").String())
		}
		mime := strings.TrimSpace(file.Get("mimeType").String())
		if mime == "" {
			mime = strings.TrimSpace(file.Get("mime_type").String())
		}
		if uri == "" {
			return nil, fmt.Errorf("fileData.fileUri is required")
		}
		if strings.HasPrefix(mime, "image/") || mime == "" {
			return map[string]any{"type": "image_url", "image_url": map[string]any{"url": uri}}, nil
		}
		return nil, fmt.Errorf("remote fileData of type %q cannot be represented as a Chat attachment", mime)
	}
	mime := strings.TrimSpace(inline.Get("mimeType").String())
	if mime == "" {
		mime = strings.TrimSpace(inline.Get("mime_type").String())
	}
	data := strings.TrimSpace(inline.Get("data").String())
	if data == "" {
		return nil, fmt.Errorf("inlineData.data is required")
	}
	uri := "data:" + mime + ";base64," + data
	if mime == "application/pdf" {
		filename := geminiObject(inline, "displayName", "display_name").String()
		if filename == "" {
			filename = "document.pdf"
		}
		return map[string]any{"type": "file", "file": map[string]any{"filename": filename, "file_data": uri}}, nil
	}
	if !strings.HasPrefix(mime, "image/") {
		return nil, fmt.Errorf("inlineData MIME type %q is unsupported on the Chat bridge", mime)
	}
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": uri}}, nil
}

func geminiToolsToChatTools(body []byte) ([]any, error) {
	var tools []any
	var convErr error
	gjson.GetBytes(body, "tools").ForEach(func(_, tool gjson.Result) bool {
		if convErr != nil {
			return false
		}
		decls := tool.Get("functionDeclarations")
		if !decls.Exists() {
			decls = tool.Get("function_declarations")
		}
		decls.ForEach(func(_, decl gjson.Result) bool {
			if convErr != nil {
				return false
			}
			name := strings.TrimSpace(decl.Get("name").String())
			if name == "" {
				return true
			}
			fn := map[string]any{"name": name}
			if desc := decl.Get("description").String(); desc != "" {
				fn["description"] = desc
			}
			params, err := geminiChatSchema(decl.Get("parameters"), geminiObject(decl, "parametersJsonSchema", "parameters_json_schema"))
			if err != nil {
				convErr = fmt.Errorf("tool %s parameters: %w", name, err)
				return false
			}
			if params != nil {
				fn["parameters"] = params
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
			return true
		})
		return convErr == nil
	})
	if convErr != nil {
		return nil, convErr
	}
	return tools, nil
}

func geminiFunctionCallingConfig(body []byte) (mode string, allowed []string) {
	cfg := gjson.GetBytes(body, "toolConfig.functionCallingConfig")
	if !cfg.Exists() {
		cfg = gjson.GetBytes(body, "tool_config.function_calling_config")
	}
	if !cfg.Exists() {
		return "", nil
	}
	mode = strings.ToUpper(strings.TrimSpace(cfg.Get("mode").String()))
	names := cfg.Get("allowedFunctionNames")
	if !names.Exists() {
		names = cfg.Get("allowed_function_names")
	}
	names.ForEach(func(_, name gjson.Result) bool {
		if text := strings.TrimSpace(name.String()); text != "" {
			allowed = append(allowed, text)
		}
		return true
	})
	return mode, allowed
}

func filterGeminiChatTools(tools []any, allowed []string) []any {
	allow := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allow[name] = struct{}{}
	}
	filtered := make([]any, 0, len(allowed))
	for _, tool := range tools {
		item, ok := tool.(map[string]any)
		if !ok {
			continue
		}
		fn, ok := item["function"].(map[string]any)
		if !ok {
			continue
		}
		name, ok := fn["name"].(string)
		if !ok {
			continue
		}
		if _, ok := allow[name]; !ok {
			continue
		}
		filtered = append(filtered, tool)
	}
	return filtered
}

// geminiToolChoiceValue 只在必须调用函数时设置 tool_choice。
// AUTO 即使 allowedFunctionNames 只有一项，也只收窄工具集，不改成强制调用。
func geminiToolChoiceValue(mode string, allowed []string) any {
	switch mode {
	case "NONE":
		return "none"
	case "ANY":
		if len(allowed) == 1 {
			return map[string]any{
				"type":     "function",
				"function": map[string]any{"name": allowed[0]},
			}
		}
		return "required"
	default:
		return nil
	}
}

func applyGeminiGenerationConfig(body []byte, out map[string]any) error {
	cfg := gjson.GetBytes(body, "generationConfig")
	if !cfg.Exists() {
		cfg = gjson.GetBytes(body, "generation_config")
	}
	if !cfg.Exists() {
		return nil
	}
	if cfg.Get("temperature").Exists() {
		out["temperature"] = cfg.Get("temperature").Float()
	}
	if cfg.Get("topP").Exists() {
		out["top_p"] = cfg.Get("topP").Float()
	} else if cfg.Get("top_p").Exists() {
		out["top_p"] = cfg.Get("top_p").Float()
	}
	if cfg.Get("maxOutputTokens").Exists() {
		out["max_tokens"] = cfg.Get("maxOutputTokens").Int()
	} else if cfg.Get("max_output_tokens").Exists() {
		out["max_tokens"] = cfg.Get("max_output_tokens").Int()
	}
	if cfg.Get("candidateCount").Exists() && cfg.Get("candidateCount").Int() > 0 {
		out["n"] = cfg.Get("candidateCount").Int()
	} else if cfg.Get("candidate_count").Int() > 0 {
		out["n"] = cfg.Get("candidate_count").Int()
	}
	var stops []string
	stop := cfg.Get("stopSequences")
	if !stop.Exists() {
		stop = cfg.Get("stop_sequences")
	}
	stop.ForEach(func(_, item gjson.Result) bool {
		if text := item.String(); text != "" {
			stops = append(stops, text)
		}
		return true
	})
	if len(stops) == 1 {
		out["stop"] = stops[0]
	} else if len(stops) > 1 {
		out["stop"] = stops
	}
	mime := strings.TrimSpace(cfg.Get("responseMimeType").String())
	if mime == "" {
		mime = strings.TrimSpace(cfg.Get("response_mime_type").String())
	}
	rawSchema := geminiObject(cfg, "responseJsonSchema", "response_json_schema")
	if !strings.EqualFold(mime, "application/json") {
		if rawSchema.Type != gjson.Null {
			return fmt.Errorf("responseJsonSchema requires responseMimeType application/json")
		}
		return nil
	}
	schema, err := geminiChatSchema(geminiObject(cfg, "responseSchema", "response_schema"), rawSchema)
	if err != nil {
		return fmt.Errorf("responseSchema: %w", err)
	}
	if schema != nil {
		out["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "response",
				"schema": schema,
			},
		}
		return nil
	}
	out["response_format"] = map[string]any{"type": "json_object"}
	return nil
}

func geminiChatSchema(typed, raw gjson.Result) (json.RawMessage, error) {
	if raw.Type != gjson.Null {
		if typed.Type != gjson.Null {
			return nil, fmt.Errorf("Gemini Schema and JSON Schema are mutually exclusive")
		}
		if !raw.IsObject() {
			return nil, fmt.Errorf("JSON Schema must be an object for Chat Completions")
		}
		return json.RawMessage(raw.Raw), nil
	}
	if typed.Type == gjson.Null {
		return nil, nil
	}
	return geminiSchemaToJSONSchema(typed.Raw)
}

// geminiSchemaToJSONSchema 把 Gemini Schema 的 OBJECT/STRING 等类型递归转成 JSON Schema。
// 无法识别的 type 在发给上游前拒绝，避免原样进入 json_schema。
func geminiSchemaToJSONSchema(raw string) (json.RawMessage, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var node any
	if err := dec.Decode(&node); err != nil {
		return nil, fmt.Errorf("invalid schema JSON")
	}
	obj, ok := node.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema must be an object")
	}
	converted, err := convertGeminiSchemaObject(obj)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(converted)
	if err != nil {
		return nil, fmt.Errorf("marshal schema: %w", err)
	}
	return encoded, nil
}

func convertGeminiSchemaObject(obj map[string]any) (any, error) {
	nullable := false
	if raw, ok := obj["nullable"]; ok {
		flag, ok := raw.(bool)
		if !ok {
			return nil, fmt.Errorf("nullable must be a boolean")
		}
		nullable = flag
	}
	out := make(map[string]any, len(obj))
	for key, value := range obj {
		switch key {
		case "nullable", "propertyOrdering", "property_ordering":
			continue
		case "type":
			converted, err := convertGeminiSchemaType(value, nullable)
			if err != nil {
				return nil, err
			}
			out["type"] = converted
		case "minItems", "maxItems", "minProperties", "maxProperties", "minLength", "maxLength":
			converted, err := convertGeminiSchemaInteger(value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out[key] = converted
		case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas":
			converted, err := convertGeminiSchemaMap(value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out[key] = converted
		case "items", "additionalProperties", "additionalItems", "contains", "not", "if", "then", "else", "propertyNames", "unevaluatedProperties", "unevaluatedItems":
			converted, err := convertGeminiSchemaOrBool(key, value)
			if err != nil {
				return nil, err
			}
			out[key] = converted
		case "anyOf", "oneOf", "allOf", "prefixItems":
			converted, err := convertGeminiSchemaArray(value)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			out[key] = converted
		default:
			out[key] = value
		}
	}
	if nullable {
		if _, ok := out["type"]; !ok {
			return nil, fmt.Errorf("nullable schema is missing type")
		}
	}
	return out, nil
}

// Gemini REST encodes these int64 constraints as strings; JSON Schema requires
// non-negative integers. Parse without floating point to preserve int64 limits.
func convertGeminiSchemaInteger(value any) (int64, error) {
	var raw string
	switch typed := value.(type) {
	case string:
		raw = typed
	case json.Number:
		raw = typed.String()
	default:
		return 0, fmt.Errorf("must be a non-negative int64")
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("must be a non-negative int64")
	}
	return n, nil
}

func convertGeminiSchemaType(value any, nullable bool) (any, error) {
	switch typed := value.(type) {
	case string:
		mapped, ok := jsonSchemaTypeName(typed)
		if !ok {
			return nil, fmt.Errorf("unsupported schema type %q", typed)
		}
		if nullable && mapped != "null" {
			return []any{mapped, "null"}, nil
		}
		return mapped, nil
	case []any:
		out := make([]any, 0, len(typed)+1)
		seenNull := false
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("unsupported schema type entry")
			}
			mapped, ok := jsonSchemaTypeName(text)
			if !ok {
				return nil, fmt.Errorf("unsupported schema type %q", text)
			}
			if mapped == "null" {
				seenNull = true
			}
			out = append(out, mapped)
		}
		if nullable && !seenNull {
			out = append(out, "null")
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported schema type shape")
	}
}

func jsonSchemaTypeName(raw string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "OBJECT":
		return "object", true
	case "STRING":
		return "string", true
	case "NUMBER":
		return "number", true
	case "INTEGER":
		return "integer", true
	case "BOOLEAN":
		return "boolean", true
	case "ARRAY":
		return "array", true
	case "NULL":
		return "null", true
	default:
		return "", false
	}
}

func convertGeminiSchemaMap(value any) (any, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("must be an object")
	}
	out := make(map[string]any, len(obj))
	for key, item := range obj {
		child, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s must be an object", key)
		}
		converted, err := convertGeminiSchemaObject(child)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		out[key] = converted
	}
	return out, nil
}

func convertGeminiSchemaArray(value any) (any, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array")
	}
	out := make([]any, 0, len(items))
	for _, item := range items {
		child, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("must contain schema objects")
		}
		converted, err := convertGeminiSchemaObject(child)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func convertGeminiSchemaOrBool(key string, value any) (any, error) {
	if flag, ok := value.(bool); ok {
		return flag, nil
	}
	if key == "items" {
		if _, ok := value.([]any); ok {
			converted, err := convertGeminiSchemaArray(value)
			if err != nil {
				return nil, fmt.Errorf("items: %w", err)
			}
			return converted, nil
		}
	}
	child, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s has an unsupported schema shape", key)
	}
	converted, err := convertGeminiSchemaObject(child)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return converted, nil
}

func chatMessageText(message ChatMessage) string {
	if len(message.Content) == 0 || string(message.Content) == "null" {
		return ""
	}
	if message.Content[0] == '"' {
		var text string
		if err := json.Unmarshal(message.Content, &text); err == nil {
			return text
		}
	}
	var parts []ChatContentPart
	if err := json.Unmarshal(message.Content, &parts); err == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type == "" || part.Type == "text" {
				if part.Text != "" {
					texts = append(texts, part.Text)
				}
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

func geminiStreamCandidate(index int, parts []any, finish string) map[string]any {
	candidate := map[string]any{
		"index": index,
		"content": map[string]any{
			"role":  "model",
			"parts": parts,
		},
	}
	if finish != "" {
		candidate["finishReason"] = finish
	}
	return candidate
}

func geminiInstructionText(body []byte, keys ...string) string {
	for _, key := range keys {
		value := gjson.GetBytes(body, key)
		if !value.Exists() {
			continue
		}
		if text := geminiPartsText(value.Get("parts")); text != "" {
			return text
		}
		if value.Type == gjson.String && strings.TrimSpace(value.String()) != "" {
			return strings.TrimSpace(value.String())
		}
	}
	return ""
}

func geminiPartsText(parts gjson.Result) string {
	if !parts.Exists() {
		return ""
	}
	texts := make([]string, 0, parts.Get("#").Int())
	parts.ForEach(func(_, part gjson.Result) bool {
		if part.Get("thought").Bool() {
			return true
		}
		if text := part.Get("text").String(); strings.TrimSpace(text) != "" {
			texts = append(texts, text)
		}
		return true
	})
	return strings.Join(texts, "\n")
}

func geminiObject(part gjson.Result, keys ...string) gjson.Result {
	for _, key := range keys {
		value := part.Get(key)
		if value.Exists() {
			return value
		}
	}
	return gjson.Result{}
}

func geminiFunctionResponseContent(response gjson.Result) string {
	if !response.Exists() || response.Raw == "" || response.Raw == "null" {
		return ""
	}
	if response.Type == gjson.String {
		return response.String()
	}
	// Unwrap common text-only envelopes. Preserve structured tool results intact.
	if response.IsObject() {
		var count int
		var text gjson.Result
		response.ForEach(func(key, value gjson.Result) bool {
			count++
			if key.String() == "content" || key.String() == "output" {
				text = value
			}
			return count == 1
		})
		if count == 1 && text.Type == gjson.String {
			return text.String()
		}
	}
	return response.Raw
}

func (p *geminiPendingCalls) add(name string, id gjson.Result) (string, error) {
	if id.Type != gjson.Null && id.Type != gjson.String {
		return "", fmt.Errorf("functionCall.id must be a string")
	}
	if name == "" {
		return "", fmt.Errorf("functionCall.name is required")
	}
	if p.byChatID == nil {
		p.byChatID = make(map[string]geminiPendingCall)
		p.bySourceID = make(map[string]string)
	}
	sourceID := id.String()
	if _, exists := p.bySourceID[sourceID]; exists {
		return "", fmt.Errorf("duplicate pending functionCall.id")
	}
	chatID := sourceID
	// An explicit ID can have the same spelling as a generated legacy ID.
	// Keep the source lookup independent of the IDs used on the Chat wire.
	for chatID == "" || p.byChatID[chatID].name != "" {
		chatID = fmt.Sprintf("call_%s_%d", sanitizeToolID(name), p.next)
		p.next++
	}
	p.byChatID[chatID] = geminiPendingCall{name: name, sourceID: sourceID}
	if sourceID != "" {
		p.bySourceID[sourceID] = chatID
	}
	return chatID, nil
}

func (p *geminiPendingCalls) take(name string, id gjson.Result) (string, error) {
	if id.Type != gjson.Null && id.Type != gjson.String {
		return "", fmt.Errorf("functionResponse.id must be a string")
	}
	var chatID string
	if sourceID := id.String(); sourceID != "" {
		chatID = p.bySourceID[sourceID]
		if chatID == "" {
			return "", fmt.Errorf("functionResponse.id has no matching pending call")
		}
		if name != "" && p.byChatID[chatID].name != name {
			return "", fmt.Errorf("functionResponse.name differs from its functionCall.name")
		}
	} else {
		for key, pending := range p.byChatID {
			if pending.name == name {
				if chatID != "" {
					return "", fmt.Errorf("functionResponse.id is required for ambiguous parallel calls")
				}
				chatID = key
			}
		}
		if chatID == "" {
			return "", fmt.Errorf("functionResponse has no matching pending call")
		}
	}
	delete(p.bySourceID, p.byChatID[chatID].sourceID)
	delete(p.byChatID, chatID)
	return chatID, nil
}

func sanitizeToolID(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "tool"
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_' || r == '-':
			_, _ = b.WriteRune(r)
		default:
			_ = b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "tool"
	}
	return b.String()
}

func firstJSONString(body []byte, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(gjson.GetBytes(body, key).String()); value != "" {
			return value
		}
	}
	return ""
}
