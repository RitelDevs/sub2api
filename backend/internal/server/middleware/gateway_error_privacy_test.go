package middleware

import (
	"bufio"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const privacyErrorJSON = `{"type":"error","request_id":"outer-secret","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"Context window is full. (request id: upstream-1) (request id: upstream-2)","request_id":"inner-secret"}}`

func privacyTestRouter(handler gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogger(), ClientRequestID(), GatewayErrorPrivacy())
	r.GET("/v1/responses", handler)
	return r
}

func TestGatewayErrorPrivacyHTTPJSON(t *testing.T) {
	for _, fragmented := range []bool{false, true} {
		t.Run(map[bool]string{false: "whole", true: "fragmented"}[fragmented], func(t *testing.T) {
			r := privacyTestRouter(func(c *gin.Context) {
				c.Writer.Header()["x-request-id"] = []string{"upstream-header-1", "upstream-header-2"}
				c.Header("Request-ID", "provider-id")
				c.Header("X-Amzn-Requestid", "aws-id")
				c.Header("Retry-After", "3")
				c.Header("Content-Type", "application/json")
				c.Header("Content-Length", "9999")
				c.Status(http.StatusBadRequest)
				if fragmented {
					for _, b := range []byte(privacyErrorJSON) {
						n, err := c.Writer.Write([]byte{b})
						require.NoError(t, err)
						require.Equal(t, 1, n)
						c.Writer.Flush()
					}
				} else {
					n, err := c.Writer.WriteString(privacyErrorJSON)
					require.NoError(t, err)
					require.Equal(t, len(privacyErrorJSON), n)
				}
			})
			server := httptest.NewServer(r)
			defer server.Close()
			resp, err := http.Get(server.URL + "/v1/responses")
			require.NoError(t, err)
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			require.Equal(t, "3", resp.Header.Get("Retry-After"))
			require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
			for key := range resp.Header {
				require.NotContains(t, strings.ToLower(key), "request")
			}
			require.True(t, gjson.ValidBytes(body), string(body))
			require.Equal(t, "Context window is full.", gjson.GetBytes(body, "error.message").String())
			require.Equal(t, "context_length_exceeded", gjson.GetBytes(body, "error.code").String())
			require.Equal(t, "invalid_request_error", gjson.GetBytes(body, "error.type").String())
			require.NotContains(t, string(body), "secret")
			require.NotContains(t, string(body), "upstream-")
		})
	}
}

func TestGatewayErrorPrivacyFragmentedSSE(t *testing.T) {
	success := "event: response.output_text.delta\r\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Keep (request id: user-prose)\",\"response_id\":\"resp_keep\",\"call_id\":\"call_keep\"}\r\n\r\n"
	failed := "event: response.failed\r\ndata: {\"type\":\"response.failed\",\"request_id\":\"outer-secret\",\"response\":{\"id\":\"resp_keep\",\"status\":\"failed\",\"error\":{\"code\":\"context_length_exceeded\",\"message\":\"Context window is full. (request id: upstream-1) (request id: upstream-2)\"}}}\r\n\r\n"
	advance := make(chan struct{})
	r := privacyTestRouter(func(c *gin.Context) {
		c.Header("Content-Type", "text/event-stream")
		c.Header("X-Request-ID", "stream-secret")
		_, err := c.Writer.WriteString(success)
		require.NoError(t, err)
		c.Writer.Flush()
		<-advance
		for _, b := range []byte(failed) {
			n, err := c.Writer.Write([]byte{b})
			require.NoError(t, err)
			require.Equal(t, 1, n)
			c.Writer.Flush()
		}
	})
	server := httptest.NewServer(r)
	defer server.Close()
	resp, err := http.Get(server.URL + "/v1/responses")
	require.NoError(t, err)
	defer resp.Body.Close()
	reader := bufio.NewReader(resp.Body)
	first := make([]byte, len(success))
	_, err = io.ReadFull(reader, first)
	require.NoError(t, err)
	require.Equal(t, success, string(first), "successful event must arrive before terminal error exists")
	close(advance)
	rest, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NotContains(t, string(rest), "upstream-")
	require.NotContains(t, string(rest), "outer-secret")
	require.Contains(t, string(rest), `"id":"resp_keep"`)
	require.Contains(t, string(rest), `"code":"context_length_exceeded"`)
	require.Contains(t, string(rest), `"message":"Context window is full."`)
	require.Empty(t, resp.Header.Get("X-Request-ID"))
	require.Empty(t, resp.Header.Get("X-Client-Request-ID"))
}

func TestGatewayErrorPrivacyMultilineSSEAndSuccessBytes(t *testing.T) {
	normal := `{"id":"resp_keep","output":[{"type":"function_call","call_id":"call_keep","arguments":"(request id: user-prose)"}],"request_id":"business-field"}`
	require.Equal(t, normal, string(sanitizeGatewaySSEFrame([]byte(normal))))
	frame := "event: error\ndata: {\"type\":\"error\",\ndata: \"error\":{\"message\":\"Bad input (request id: private)\"}}\n\n"
	sanitized := string(sanitizeGatewaySSEFrame([]byte(frame)))
	require.NotContains(t, sanitized, "private")
	require.Contains(t, sanitized, "Bad input")
	r := privacyTestRouter(func(c *gin.Context) { c.Data(200, "application/json", []byte(normal)) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
	require.Equal(t, normal, rec.Body.String())
}

func TestGatewayErrorPrivacyPreservesRawCapture(t *testing.T) {
	var captured string
	r := privacyTestRouter(func(c *gin.Context) {
		original := c.Writer
		capture := &privacyCaptureWriter{ResponseWriter: original}
		c.Writer = capture
		c.Data(400, "application/json", []byte(privacyErrorJSON))
		captured = capture.body.String()
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
	require.Equal(t, privacyErrorJSON, captured)
	require.NotContains(t, rec.Body.String(), "upstream-")
}

type privacyCaptureWriter struct {
	gin.ResponseWriter
	body strings.Builder
}

func (w *privacyCaptureWriter) Write(p []byte) (int, error) {
	w.body.Write(p)
	return w.ResponseWriter.Write(p)
}
func (w *privacyCaptureWriter) WriteString(s string) (int, error) {
	w.body.WriteString(s)
	return w.ResponseWriter.WriteString(s)
}

func TestGatewayErrorPrivacyBareHTTPError(t *testing.T) {
	r := privacyTestRouter(func(c *gin.Context) {
		c.Data(400, "application/json", []byte(`{"message":"Bad input (request id: private)","request_id":"private-field"}`))
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/responses", nil))
	require.JSONEq(t, `{"message":"Bad input"}`, rec.Body.String())
}

func TestGatewayErrorPrivacyKeepsSuccessfulContentLength(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(GatewayErrorPrivacy())
	r.HEAD("/v1/videos/test/content", func(c *gin.Context) { c.Header("Content-Length", "1000"); c.Status(200); c.Writer.WriteHeaderNow() })
	r.GET("/v1/videos/test/content", func(c *gin.Context) {
		c.Header("Content-Length", "3")
		c.Header("Content-Range", "bytes 0-2/1000")
		c.Data(206, "video/mp4", []byte("abc"))
	})
	server := httptest.NewServer(r)
	defer server.Close()
	head, err := http.Head(server.URL + "/v1/videos/test/content")
	require.NoError(t, err)
	defer head.Body.Close()
	require.Equal(t, int64(1000), head.ContentLength)
	partial, err := http.Get(server.URL + "/v1/videos/test/content")
	require.NoError(t, err)
	defer partial.Body.Close()
	body, err := io.ReadAll(partial.Body)
	require.NoError(t, err)
	require.Equal(t, 206, partial.StatusCode)
	require.Equal(t, int64(3), partial.ContentLength)
	require.Equal(t, "bytes 0-2/1000", partial.Header.Get("Content-Range"))
	require.Equal(t, "abc", string(body))
}

func TestGatewayErrorPrivacyPlainSSEError(t *testing.T) {
	frame := "event: error\ndata: Bad input (request id: upstream-1) (request id: upstream-2)\n\n"
	require.Equal(t, "event: error\ndata: Bad input\n\n", string(sanitizeGatewaySSEFrame([]byte(frame))))
}

func TestGatewayErrorPrivacyCoversEarlyBodyLimitRejection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogger())
	r.POST("/v1/responses", GatewayErrorPrivacy(), RequestBodyLimit(1), func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		require.Error(t, err)
		c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": gin.H{"message": "body too large"}})
	})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("oversize")))
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.Empty(t, rec.Header().Get("X-Request-ID"))
	require.Contains(t, rec.Body.String(), "body too large")
}
