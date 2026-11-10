package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestWriteCapturedWSClientErrorPrivacy(t *testing.T) {
	failed := []byte(`{"type":"response.failed","request_id":"outer-private","response":{"id":"resp_keep","status":"failed","error":{"code":"context_length_exceeded","message":"Context window is full. (request id: first-private) (request id: second-private)","request_id":"inner-private"}}}`)
	original := string(failed)
	success := []byte(`{"type":"response.output_text.delta","delta":"Explain request id: example","response_id":"resp_keep","call_id":"call_keep"}`)
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := coderws.Accept(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		for _, payload := range [][]byte{failed, success} {
			if err = WriteCapturedWSClient(ctx, conn, coderws.MessageText, payload); err != nil {
				serverErr <- err
				return
			}
		}
		serverErr <- nil
		_ = conn.Close(coderws.StatusNormalClosure, "")
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	defer conn.CloseNow()
	_, payload, err := conn.Read(ctx)
	require.NoError(t, err)
	require.NotContains(t, string(payload), "private")
	require.False(t, gjson.GetBytes(payload, "request_id").Exists())
	require.Equal(t, "resp_keep", gjson.GetBytes(payload, "response.id").String())
	require.Equal(t, "Context window is full.", gjson.GetBytes(payload, "response.error.message").String())
	require.Equal(t, "context_length_exceeded", gjson.GetBytes(payload, "response.error.code").String())
	_, payload, err = conn.Read(ctx)
	require.NoError(t, err)
	require.Equal(t, success, payload)
	require.NoError(t, <-serverErr)
	require.Equal(t, original, string(failed), "upstream payload must remain available for internal diagnostics")
}
