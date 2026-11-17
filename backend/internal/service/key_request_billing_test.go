package service

import (
	"github.com/stretchr/testify/require"
	"net/http"
	"testing"
)

func TestKeyRequestBillingMetadataReadBoundary(t *testing.T) {
	require.True(t, IsAPIKeyRequestBillingPath(http.MethodGet, APIKeyRequestBillingPrefix+"child-1"))
	require.False(t, IsAPIKeyRequestBillingPath(http.MethodPost, APIKeyRequestBillingPrefix+"child-1"))
	require.False(t, IsAPIKeyRequestBillingPath(http.MethodGet, APIKeyRequestBillingPrefix+"child-1/extra"))
	require.False(t, IsAPIKeyRequestBillingPath(http.MethodGet, APIKeyRequestBillingPrefix))
}
