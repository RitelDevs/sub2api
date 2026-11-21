package repository

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func consumeAffinityResponse(t *testing.T, resp *http.Response, err error) {
	t.Helper()
	require.NoError(t, err)
	require.NotNil(t, resp)
	_, err = io.Copy(io.Discard, resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}
