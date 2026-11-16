package service

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// DeepSeekRecoveryDeliveryError identifies the local stream adapter boundary.
// Cause stays in local diagnostic logs; safe Ops details contain only a category.
type DeepSeekRecoveryDeliveryError struct{ Cause error }

func (e *DeepSeekRecoveryDeliveryError) Error() string { return e.Cause.Error() }
func (e *DeepSeekRecoveryDeliveryError) Unwrap() error { return e.Cause }

// DeepSeekRecoveryFailureReason returns a credential-free structured reason;
// untyped transport errors retain the generic classification.
func DeepSeekRecoveryFailureReason(cause error) string {
	var local *DeepSeekRecoveryDeliveryError
	if errors.As(cause, &local) {
		return "deepseek_delivery_error"
	}
	var failover *UpstreamFailoverError
	if errors.As(cause, &failover) && failover.Reason != "" {
		return string(failover.Reason)
	}
	return "deepseek_attempt_failed"
}

// Failed attempts are provider diagnostics, never customer usage commands.
// Keep known usage and explicitly unknown cost available for reconciliation;
// do not invent a zero-cost customer row or consume the winner's dedup key.
func RecordDeepSeekRecoveryFailure(c *gin.Context, account *Account, result *OpenAIForwardResult, ordinal int, cause error, outcome DeepSeekRecoveryOutcome) {
	status := outcome.HTTPStatus
	if status == 0 {
		status = http.StatusBadGateway
	}
	var failover *UpstreamFailoverError
	if outcome.HTTPStatus == 0 && errors.As(cause, &failover) && failover.StatusCode > 0 {
		status = failover.StatusCode
	}
	detail := map[string]any{"attempt": ordinal, "observation_only": true, "provider_cost": "unknown", "source": outcome, "failure_reason": DeepSeekRecoveryFailureReason(cause)}
	requestID := outcome.UpstreamRequestID
	if result != nil {
		requestID = result.RequestID
	}
	payload, _ := json.Marshal(detail)
	proxyID, proxyName := opsUpstreamProxyAttribution(account)
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: "deepseek", AccountID: account.ID, ProxyID: proxyID, ProxyName: proxyName,
		UpstreamStatusCode: status, UpstreamRequestID: requestID, Kind: "failover",
		Stage: "inference", Reason: DeepSeekRecoveryFailureReason(cause), Message: "DeepSeek attempt failed", Detail: string(payload),
	})
}
