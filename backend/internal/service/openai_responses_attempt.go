package service

import (
	"bytes"
	"errors"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/tidwall/gjson"
)

// An upstream may have generated output before the client receives its first
// byte. Keep replay authorization independent of downstream delivery and of
// the numeric totals (an explicitly reported zero is still an observation).
type openAIResponsesAttemptEvidence struct {
	noReplay      bool
	usageReported bool
}

func (e *openAIResponsesAttemptEvidence) observe(payload []byte, eventType string) {
	if !e.usageReported && bytes.Contains(payload, []byte(`"usage"`)) {
		for _, path := range []string{"usage", "response.usage", "data.usage", "data.response.usage"} {
			meter := gjson.GetBytes(payload, path)
			if !meter.IsObject() {
				continue
			}
			e.noReplay = true
			for _, key := range []string{"input_tokens", "output_tokens", "prompt_tokens", "completion_tokens", "cache_read_input_tokens", "cache_creation_input_tokens"} {
				v := meter.Get(key)
				if v.Type == gjson.Number && v.Num >= 0 && math.Trunc(v.Num) == v.Num {
					e.usageReported = true
					return
				}
			}
		}
	}
	if e.noReplay {
		return
	}
	if openAIStreamEventTypeIsTerminal(eventType) || eventType == "error" {
		e.noReplay = gjson.GetBytes(payload, "response.output.0").Exists() || gjson.GetBytes(payload, "output.0").Exists()
		return
	}
	if eventType == "response.output_item.added" || eventType == "response.output_item.done" {
		item := gjson.GetBytes(payload, "item")
		switch item.Get("type").String() {
		case "function_call", "custom_tool_call":
			if item.Get("name").String() != "" || item.Get("call_id").String() != "" {
				e.noReplay = true
				return
			}
		case "web_search_call", "file_search_call", "image_generation_call", "code_interpreter_call", "computer_call", "mcp_call":
			e.noReplay = true
			return
		}
	}
	e.noReplay = openai.ResponseStartsSemanticOutput(payload, eventType)
}

// Keep the original error identity except for retry instructions that must
// not escape an attempt with observed work.
func (e openAIResponsesAttemptEvidence) failure(err error) error {
	var retry *UpstreamFailoverError
	var encrypted *openAIEncryptedContentRecoverySignal
	if e.noReplay && (errors.As(err, &retry) || errors.As(err, &encrypted)) {
		return errors.New("upstream response interrupted after observed generation or usage")
	}
	if _, compact := asOpenAICompactFallbackSignal(err); e.noReplay && compact {
		return errors.New("upstream compact response failed after observed generation or usage")
	}
	return err
}

func (e openAIResponsesAttemptEvidence) applyPartial(r *OpenAIForwardResult) {
	r.streamReadIncomplete = true
	r.ObservedUsageOnError = e.usageReported || openAIUsageHasTokens(&r.Usage)
	r.UsageUnavailable = !r.ObservedUsageOnError && r.ImageCount == 0 && r.SearchCount == 0
}

// Buffered Responses requests can also receive an SSE stream. A terminal
// failure must consider all preceding events before authorizing another call.
func (s *OpenAIGatewayService) bufferedResponsesFailure(body string) *openaiNonStreamingResult {
	usage := &OpenAIUsage{}
	r := &openaiNonStreamingResult{usage: usage, OpenAIUsage: usage}
	images := newOpenAIImageOutputCounter()
	searches := make(map[string]struct{})
	forEachOpenAISSEFrame(body, func(eventType string, data []byte) {
		r.evidence.observe(data, eventType)
		s.parseSSEUsageBytesWithType(data, eventType, usage)
		if r.responseID == "" {
			r.responseID = extractOpenAIResponseIDFromJSONBytes(data)
		}
		images.AddSSEData(data)
		r.searchCount += countGrokNativeSearchCallsInSSEDataDedup(data, searches)
	})
	r.imageCount, r.imageOutputSizes = images.Count(), images.Sizes()
	if !r.evidence.noReplay {
		return nil
	}
	return r
}
