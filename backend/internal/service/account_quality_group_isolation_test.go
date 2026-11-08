package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func isolatedBorrowAccount() (*OpenAIGatewayService, *Account, *borrowSafetyProvider) {
	s, a, provider := borrowSafetyService()
	a.GroupIDs = []int64{25}
	a.Extra[QualityGroupIsolationKey] = map[string]any{"version": 1, "isolation_group_id": 25}
	s.cfg.Gateway.CodexGatewayPin.Enabled = false
	return s, a, provider
}

func TestQualityGroupIsolationUsesNativeAllSupportedModels(t *testing.T) {
	s, a, provider := isolatedBorrowAccount()
	// Match Redis JSON number representation as well as the full account.
	raw, err := json.Marshal(a.Extra)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &a.Extra))
	scheduler := newDefaultOpenAIAccountScheduler(s, nil).(*defaultOpenAIAccountScheduler)
	for _, model := range []string{"gpt-6-astra", "gpt-6-sol", "gpt-6.1-sol", "gpt-6-luna"} {
		require.False(t, a.RequiresGatewayBorrow(model), model)
		require.Empty(t, gatewayBorrowEligibilityReason(context.Background(), a, model, false), model)
		compatible, reason := scheduler.isAccountRequestCompatibleReason(context.Background(), a, OpenAIAccountScheduleRequest{RequestedModel: model, RequireCompact: true})
		require.True(t, compatible, "%s: %s", model, reason)
		headers := make(http.Header)
		scope, proxy, release, err := s.prepareGatewayBorrowWS(context.Background(), a, model, headers, "http://own-proxy.invalid")
		require.NoError(t, err)
		release()
		require.Empty(t, scope)
		require.Equal(t, "http://own-proxy.invalid", proxy)
		require.Empty(t, headers.Get("Cookie"))
	}
	require.Zero(t, provider.calls)
}

func TestQualityGroupIsolationRetainsPendingAndUnrelatedRestrictions(t *testing.T) {
	s, a, _ := isolatedBorrowAccount()
	a.Extra[GatewayBorrowAccountQualityModeKey] = GatewayBorrowAccountQualityMode
	a.Extra[GatewayBorrowAccountQualityPendingKey] = true
	a.Credentials["model_mapping"] = map[string]any{"gpt-6-astra": "gpt-6-astra"}
	require.Equal(t, "gateway_borrow_initial_quality_pending", s.gatewayBorrowUpstreamPolicyReason(context.Background(), a, "gpt-6-astra", false))
	a.Extra[QualityModelIsolationKey] = map[string]any{"gpt-6-astra": map[string]any{"reason": "quality_rule:7"}}
	require.True(t, a.qualityModelBlocked(context.Background(), "gpt-6-astra"))
	a.Extra[ExcelBPSRequiredGroupIDsKey] = []int64{25}
	a.Extra[ExcelBPSRequiredModelsKey] = []string{"gpt-6-astra"}
	require.True(t, a.excelBPSRequiredForModel("gpt-6-astra"), "an independent required BPS policy is not bypassed")
}

func TestQualityGroupIsolationDoesNotOpenBusinessGroups(t *testing.T) {
	for _, change := range []string{"missing marker", "wrong group", "multiple groups", "bad version", "bad group", "other platform"} {
		t.Run(change, func(t *testing.T) {
			_, a, _ := isolatedBorrowAccount()
			marker := a.Extra[QualityGroupIsolationKey].(map[string]any)
			switch change {
			case "missing marker":
				delete(a.Extra, QualityGroupIsolationKey)
			case "wrong group":
				a.GroupIDs = []int64{12}
			case "multiple groups":
				a.GroupIDs = []int64{25, 12}
			case "bad version":
				marker["version"] = 2
			case "bad group":
				marker["isolation_group_id"] = "25"
			case "other platform":
				a.Platform = PlatformAnthropic
			}
			require.True(t, a.RequiresGatewayBorrowUpstream("gpt-6-astra"))
		})
	}
}

func TestQualityGroupIsolationNativeFinalSendRechecksMembership(t *testing.T) {
	for _, change := range []bool{false, true} {
		t.Run(map[bool]string{false: "native send", true: "rejoined business group"}[change], func(t *testing.T) {
			s, a, provider := isolatedBorrowAccount()
			repo := &finalBorrowSendRepo{account: a}
			s.accountRepo = repo
			upstream := &delayedBorrowFinalSendUpstream{borrowSafetyProvider: provider}
			if change {
				latest := *a
				latest.GroupIDs = []int64{25, 12}
				repo.account = &latest
			}
			s.httpUpstream = upstream
			req, err := http.NewRequestWithContext(context.Background(), "POST", "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-6-astra"}`))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+a.GetOpenAIAccessToken())
			response, err := s.doOpenAIProxyAttempt(req, a, runtimeProxyEgress{})
			if change {
				require.Error(t, err)
				require.Zero(t, upstream.business)
			} else {
				require.NoError(t, err)
				require.Equal(t, 1, upstream.business)
				_ = response.Body.Close()
			}
			require.Zero(t, provider.calls)
		})
	}
}
