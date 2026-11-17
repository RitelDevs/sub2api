package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"github.com/Wei-Shaw/sub2api/internal/config"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
)

// KeyRequestBilling exposes one final receipt, never user-wide logs or admin capabilities.
func (h *GatewayHandler) KeyRequestBilling(c *gin.Context) {
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key.ID <= 0 {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if h.cfg != nil && h.cfg.RunMode == config.RunModeSimple {
		h.errorResponse(c, http.StatusNotFound, "not_found_error", "Billing information is not supported in simple mode")
		return
	}
	clientID := c.Param("clientRequestId")
	if !service.ValidClientBillingRequestID(clientID) || len(c.Request.URL.Query()) != 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Invalid billing request ID or query")
		return
	}
	if h.usageService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Billing receipt is unavailable")
		return
	}
	requestID := "client:" + clientID
	receipt, err := h.usageService.RequestBilling(c.Request.Context(), key.ID, requestID)
	if err != nil || receipt == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Billing receipt is unavailable")
		return
	}
	out := gin.H{"object": "sub2api.request_billing", "schema_version": 1, "api_key_id": key.ID,
		"client_request_id": clientID, "request_id": requestID, "currency": "USD", "status": receipt.Status}
	if receipt.Status == "final" {
		digest := sha256.Sum256([]byte(requestID))
		out["bill_id"] = "usage:" + strconv.FormatInt(key.ID, 10) + ":" + hex.EncodeToString(digest[:])
		out["actual_cost"] = receipt.ActualCost
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, out)
}
