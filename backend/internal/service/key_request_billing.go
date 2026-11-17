package service

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
)

const APIKeyRequestBillingPrefix = "/v1/sub2api/billing/requests/"

var clientBillingRequestID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,200}$`)

func ValidClientBillingRequestID(id string) bool { return clientBillingRequestID.MatchString(id) }
func IsAPIKeyRequestBillingPath(method, path string) bool {
	return method == http.MethodGet && strings.HasPrefix(path, APIKeyRequestBillingPrefix) &&
		ValidClientBillingRequestID(strings.TrimPrefix(path, APIKeyRequestBillingPrefix))
}

type APIKeyRequestBilling struct {
	Status     string
	ActualCost string
}

type APIKeyRequestBillingReader interface {
	GetAPIKeyRequestBilling(context.Context, int64, string) (*APIKeyRequestBilling, error)
}

// RequestBilling reads only the authenticated key's exact request. Absence is not a zero charge.
func (s *UsageService) RequestBilling(ctx context.Context, keyID int64, requestID string) (*APIKeyRequestBilling, error) {
	reader, ok := s.usageRepo.(APIKeyRequestBillingReader)
	if !ok {
		return nil, errors.New("request billing reader unavailable")
	}
	return reader.GetAPIKeyRequestBilling(ctx, keyID, requestID)
}
