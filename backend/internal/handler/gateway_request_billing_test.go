package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

type keyRequestBillingRepo struct {
	service.UsageLogRepository
	key     int64
	request string
}

func (r *keyRequestBillingRepo) GetAPIKeyRequestBilling(_ context.Context, key int64, request string) (*service.APIKeyRequestBilling, error) {
	r.key = key
	r.request = request
	return &service.APIKeyRequestBilling{Status: "final", ActualCost: "0.00007813"}, nil
}
func TestKeyRequestBillingUsesAuthenticatedKeyAndCanonicalDecimal(t *testing.T) {
	repo := &keyRequestBillingRepo{}
	h := &GatewayHandler{usageService: service.NewUsageService(repo, nil, nil, nil)}
	c, w := newKeyBillingContext(&service.APIKey{ID: 82, UserID: 7})
	c.Params = gin.Params{{Key: "clientRequestId", Value: "child-1"}}
	h.KeyRequestBilling(c)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(82), repo.key)
	require.Equal(t, "client:child-1", repo.request)
	require.Contains(t, w.Body.String(), `"actual_cost":"0.00007813"`)
	require.Contains(t, w.Body.String(), `"api_key_id":82`)
	require.Equal(t, "no-store", w.Header().Get("Cache-Control"))
	require.NotContains(t, w.Body.String(), "user_id")
}
func TestKeyRequestBillingRejectsMissingAuthAndCallerOwnerOverride(t *testing.T) {
	h := &GatewayHandler{}
	c, w := newKeyBillingContext(nil)
	h.KeyRequestBilling(c)
	require.Equal(t, http.StatusUnauthorized, w.Code)
	c, w = newKeyBillingContext(&service.APIKey{ID: 82})
	c.Params = gin.Params{{Key: "clientRequestId", Value: "child-1"}}
	c.Request.URL.RawQuery = "api_key_id=99"
	h.KeyRequestBilling(c)
	require.Equal(t, http.StatusBadRequest, w.Code)
}
