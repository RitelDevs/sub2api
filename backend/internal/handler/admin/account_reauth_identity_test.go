package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestApplyOAuthCredentialsPreservesBoundWorkspace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, workspace, user string
		want                  int
	}{
		{"existing business workspace", "business", "owner", http.StatusOK},
		{"personal workspace of same owner", "personal", "owner", http.StatusConflict},
		{"different member of same workspace", "business", "another", http.StatusConflict},
		{"workspace missing in new token", "", "owner", http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := newStubAdminService()
			stub.getAccountResult = &service.Account{ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
				Credentials: map[string]any{"chatgpt_account_id": "business", "chatgpt_user_id": "owner", "email": "owner@example.test"}}
			token := buildCodexImportTestJWT(t, time.Now().Add(time.Hour), map[string]any{
				"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": tc.workspace, "chatgpt_user_id": tc.user}, "email": "Owner@example.test",
			})
			body, err := json.Marshal(map[string]any{"type": "oauth", "credentials": map[string]any{"access_token": token, "chatgpt_account_id": "business"}, "extra": map[string]any{"email": "Owner@example.test"}})
			require.NoError(t, err)
			handler := NewAccountHandler(stub, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.POST("/accounts/:id/apply-oauth-credentials", handler.ApplyOAuthCredentials)
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/accounts/1/apply-oauth-credentials", bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(recorder, request)
			require.Equal(t, tc.want, recorder.Code)
			if tc.want == http.StatusOK {
				require.Equal(t, 1, stub.updateAccountCalls)
				require.Equal(t, "business", stub.lastUpdateAccountInput.Credentials["chatgpt_account_id"])
			} else {
				require.Zero(t, stub.updateAccountCalls)
				require.Zero(t, stub.updateAccountExtraCalls)
			}
		})
	}
}
