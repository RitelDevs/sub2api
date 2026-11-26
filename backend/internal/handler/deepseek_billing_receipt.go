package handler

import (
	"context"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// This state follows the native handler's final billing decision across retries.
// A new send invalidates the previous failure's claim to a zero receipt.
type deepSeekBillingReceipt struct {
	started  bool
	unbilled *service.Account
}

func newDeepSeekBillingReceipt(platform, model string, body []byte) *deepSeekBillingReceipt {
	if platform != service.PlatformDeepseek || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(model)), "deepseek-") ||
		gjson.GetBytes(body, "n").Int() > 1 || gjson.GetBytes(body, "previous_response_id").Exists() {
		return nil
	}
	return &deepSeekBillingReceipt{}
}

func (r *deepSeekBillingReceipt) beforeForward(c *gin.Context, account *service.Account, stream bool, streamStarted *bool) error {
	if r == nil {
		return nil
	}
	r.unbilled = nil
	if r.started {
		return nil
	}
	r.started = true
	if !stream || c.GetHeader("X-Sub2API-Billing-Receipt") != "1" {
		return nil
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	const prelude = ": keep-alive\n\n"
	n, err := c.Writer.Write([]byte(prelude))
	if err == nil && n != len(prelude) {
		err = io.ErrShortWrite
	}
	if err != nil {
		r.unbilled = account
		return err
	}
	c.Writer.Flush()
	*streamStarted = true
	return nil
}

func (r *deepSeekBillingReceipt) observeFailure(account *service.Account, result *service.OpenAIForwardResult, err error) {
	if r != nil && result == nil && err != nil {
		r.unbilled = account
	}
}

func (r *deepSeekBillingReceipt) finish(h *OpenAIGatewayHandler, c *gin.Context, key *service.APIKey, model string, body []byte, log *zap.Logger) {
	if r == nil || r.unbilled == nil {
		return
	}
	input := &service.OpenAIRecordUsageInput{
		Result: &service.OpenAIForwardResult{Model: model}, APIKey: key, User: key.User,
		Account: r.unbilled, RequestPayloadHash: service.HashUsageRequestPayload(body), APIKeyService: h.apiKeyService,
		InboundEndpoint: GetInboundEndpoint(c), UpstreamEndpoint: resolveOpenAIUpstreamEndpoint(c, r.unbilled, nil),
	}
	h.submitMandatoryUsageRecordTask(c.Request.Context(), func(ctx context.Context) {
		if err := h.gatewayService.RecordConfirmedUnbilledDeepSeek(ctx, input); err != nil {
			log.Error("deepseek.unbilled_receipt_pending", zap.Error(err))
		}
	})
}
