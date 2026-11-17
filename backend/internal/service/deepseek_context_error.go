package service

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// Keep public classification separate from account policy and retry reasons.
// Arbitrary vendor messages are never retained or reflected to the client.
func deepSeekPublicErrorCode(payload []byte, status int) string {
	r := gjson.ParseBytes(payload)
	for _, path := range []string{"error.code", "error.type", "response.error.code", "code"} {
		switch strings.ToLower(r.Get(path).String()) {
		case "context_length_exceeded", "max_context_length_exceeded", "context_limit", "prompt_too_long":
			return "context_limit"
		case "request_too_large", "payload_too_large":
			return "request_too_large"
		case "invalid_content", "invalid_image":
			return "invalid_content"
		case "rate_limit_exceeded", "rate_limited":
			return "rate_limited"
		}
	}
	if status == 429 {
		return "rate_limited"
	}
	return "upstream_unavailable"
}

func (a *DeepSeekRecoveryAttempt) observePublicError(payload []byte, status int) {
	code := deepSeekPublicErrorCode(payload, status)
	a.mu.Lock()
	a.out.PublicErrorCode = code
	a.mu.Unlock()
}

// HTTP diagnostics describe the provider error without retaining free text.
// These observations never participate in public errors, retry or account policy.
func (a *DeepSeekRecoveryAttempt) observeHTTPError(platform string, payload []byte, status int, headers http.Header) {
	category, reason := classifyAccountOpsFailure(platform, status, headers, payload)
	root := gjson.ParseBytes(payload)
	field := func(paths ...string) string {
		for _, path := range paths {
			if v := root.Get(path); v.Type == gjson.String && v.String() != "" {
				return v.String()
			}
		}
		return ""
	}
	code := deepSeekDiagnosticErrorToken(field("error.code", "response.error.code", "code"))
	typ := deepSeekDiagnosticErrorToken(field("error.type", "response.error.type", "type"))
	param := deepSeekDiagnosticParam(field("error.param", "response.error.param", "param"))
	message := field("error.message", "response.error.message", "message", "error")
	if !gjson.ValidBytes(payload) && !strings.Contains(string(payload), "<") {
		message = string(payload) // Same plain-text fallback as account classification.
	}
	var hash string
	var hints []string
	if message != "" {
		sum := sha256.Sum256([]byte(message))
		hash = hex.EncodeToString(sum[:])
		text := strings.ToLower(message)
		for _, hint := range []struct {
			name    string
			phrases []string
		}{
			{"precharge", []string{"预扣", "预冻结", "precharge", "pre-charge", "pre-deduct", "pre_deduct", "pre-consum", "preconsum"}},
			{"context", []string{"上下文", "context length", "context window", "context_length"}},
			{"output_budget", []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "output token", "输出预算", "输出长度", "输出令牌"}},
			{"thinking", []string{"thinking", "budget_tokens", "思考预算", "思考令牌"}},
			{"tool_pairing", []string{"tool_use_id", "tool_result", "tool_call_id", "tool pairing", "工具配对"}},
			{"image", []string{"image", "图片", "图像"}},
		} {
			for _, phrase := range hint.phrases {
				if strings.Contains(text, phrase) {
					hints = append(hints, hint.name)
					break
				}
			}
		}
	}
	a.mu.Lock()
	a.out.HTTPErrorClass = category
	a.out.HTTPErrorReason = reason
	a.out.HTTPErrorCode = code
	a.out.HTTPErrorType = typ
	a.out.HTTPErrorParam = param
	a.out.HTTPErrorMessageHash = hash
	a.out.HTTPErrorMessageChars = utf8.RuneCountInString(message)
	a.out.HTTPErrorMessageHints = hints
	a.mu.Unlock()
}

func deepSeekDiagnosticErrorToken(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "insufficient_balance", "balance_not_enough", "insufficient_credit", "credit_balance_too_low", "account_balance_insufficient", "balance_exhausted",
		"insufficient_quota", "weekly_limit_exceeded", "weekly_quota_exceeded", "weekly_quota_exhausted", "weekly_usage_limit_reached",
		"context_length_exceeded", "max_context_length_exceeded", "context_limit", "prompt_too_long", "request_too_large", "payload_too_large",
		"invalid_request_error", "invalid_request", "invalid_parameter", "invalid_value", "invalid_content", "invalid_image",
		"authentication_error", "permission_error", "permission_denied", "forbidden", "rate_limit_exceeded", "rate_limited", "rate_limit_error", "api_error", "server_error", "overloaded_error":
		return value
	default:
		return ""
	}
}

func deepSeekDiagnosticParam(value string) string {
	// Canonicalize array indexes, then accept only complete known API paths.
	// Unknown values (including arbitrary prose) are never copied into Outcome.
	if len(value) > 128 {
		return ""
	}
	var path strings.Builder
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == '.' || r == '[' || r == ']' }) {
		if strings.Trim(part, "0123456789") == "" {
			path.WriteString("[]")
		} else {
			if path.Len() > 0 {
				path.WriteByte('.')
			}
			path.WriteString(part)
		}
	}
	switch path.String() {
	case "model", "stream", "max_tokens", "max_completion_tokens", "max_output_tokens", "temperature", "top_p", "stop",
		"thinking", "thinking.type", "thinking.budget_tokens", "tool_choice", "tools", "tools[].type", "tools[].name", "tools[].input_schema", "tools[].function.name", "tools[].function.parameters",
		"messages", "messages[].role", "messages[].content", "messages[].content[].type", "messages[].content[].text", "messages[].content[].tool_use_id", "messages[].content[].source", "messages[].content[].source.type":
		return path.String()
	default:
		return ""
	}
}
