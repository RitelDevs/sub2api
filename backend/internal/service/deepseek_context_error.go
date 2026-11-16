package service

import (
	"github.com/tidwall/gjson"
	"strings"
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
