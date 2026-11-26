package service

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
)

const gpt61SolSubscriptionLongContextThreshold = 500_000

// openAISubscriptionLongContextExempt 按实际凭据和最终发送模型判断订阅豁免。
// GPT-6.2 Sol 和 Astra 的订阅请求免模型附加费，GPT-6.1 Sol 在 500,000
// 输入 token 以内免附加费。GPT-6.1 Terra 和 API Key 沿用模型价卡。
// 使用发送模型，避免客户别名、渠道计费模型或响应模型计费切换改变上游订阅规则。
func openAISubscriptionLongContextExempt(account *Account, result *OpenAIForwardResult, tokens UsageTokens) bool {
	if !account.IsOpenAIOAuthLike() || result == nil {
		return false
	}
	model := strings.TrimSpace(result.UpstreamModel)
	if model == "" {
		model = result.Model
	}
	if openai.IsGPT61SolModelSpelling(model) {
		return longContextBillableInputTokens(tokens) <= gpt61SolSubscriptionLongContextThreshold
	}
	return openai.GPT62ModelID(model) != ""
}
