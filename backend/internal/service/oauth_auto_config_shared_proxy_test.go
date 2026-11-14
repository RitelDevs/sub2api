//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/adminaccess"
	"github.com/stretchr/testify/require"
)

func TestOAuthSharedProxyBalancesOccupiedPoolWithStableIDAndPagination(t *testing.T) {
	// Every eligible proxy is already occupied and has the same exit IP.
	items := make([]ProxyWithAccountCount, 0, 103)
	for id := int64(1); id <= 100; id++ {
		items = append(items, oauthProxyFixture(id, 0))
	}
	items = append(items, oauthProxyFixture(103, 4), oauthProxyFixture(102, 2), oauthProxyFixture(101, 2))
	repo := &oauthDedicatedProxyRepoStub{items: items}
	prober := &oauthDedicatedProberStub{results: map[string]*ProxyExitInfo{
		"proxy-101.test": oauthExitFixture("8.8.8.8"), "proxy-102.test": oauthExitFixture("8.8.8.8"), "proxy-103.test": oauthExitFixture("8.8.8.8"),
	}}
	svc := &adminServiceImpl{proxyRepo: repo, proxyProber: prober}
	extra := map[string]any{"keep": true, openAIRequestTimezoneExtraKey: "Europe/London"}
	for _, want := range []int64{101, 102, 101, 102, 101, 102, 103} {
		input := &CreateAccountInput{Extra: extra}
		require.NoError(t, svc.applyOAuthSharedProxy(oauthProxyTestContext(), input, []int64{103, 102, 101}))
		require.Equal(t, want, *input.ProxyID)
		require.Equal(t, true, input.Extra["keep"])
		require.Equal(t, "America/Los_Angeles", input.Extra[openAIRequestTimezoneExtraKey])
		for i := range repo.items {
			if repo.items[i].ID == want {
				repo.items[i].AccountCount++
			}
		}
	}
	require.Equal(t, []int{1, 2, 1, 2, 1, 2, 1, 2, 1, 2, 1, 2, 1, 2}, repo.pages)
	require.Equal(t, []string{"proxy-101.test", "proxy-102.test", "proxy-101.test", "proxy-102.test", "proxy-101.test", "proxy-102.test", "proxy-103.test"}, prober.calls)
	require.Equal(t, "Europe/London", extra[openAIRequestTimezoneExtraKey])
}

func TestOAuthSharedProxySkipsUnavailableUnlistedAndFailedProbes(t *testing.T) {
	inactive, expired := oauthProxyFixture(1, 0), oauthProxyFixture(2, 0)
	inactive.Status = "inactive"
	past := time.Now().Add(-time.Minute)
	expired.ExpiresAt = &past
	repo := &oauthDedicatedProxyRepoStub{items: []ProxyWithAccountCount{inactive, expired, oauthProxyFixture(3, 0), oauthProxyFixture(4, 1), oauthProxyFixture(5, 2), oauthProxyFixture(6, 3), oauthProxyFixture(7, 4)}}
	invalidZone := oauthExitFixture("8.8.8.8")
	invalidZone.Timezone = "Untrusted/Zone"
	prober := &oauthDedicatedProberStub{results: map[string]*ProxyExitInfo{
		"proxy-5.test": oauthExitFixture("127.0.0.1"), "proxy-6.test": invalidZone, "proxy-7.test": oauthExitFixture("9.9.9.9"),
	}, errors: map[string]error{"proxy-4.test": errors.New("secret endpoint")}}
	svc := &adminServiceImpl{proxyRepo: repo, proxyProber: prober}
	input := &CreateAccountInput{}
	// ID 3 is unlisted; ID 99 represents a deleted/missing allowlisted row.
	require.NoError(t, svc.applyOAuthSharedProxy(oauthProxyTestContext(), input, []int64{1, 2, 4, 5, 6, 7, 99}))
	require.EqualValues(t, 7, *input.ProxyID)
	require.Equal(t, []string{"proxy-4.test", "proxy-5.test", "proxy-6.test", "proxy-7.test"}, prober.calls)
}

func TestOAuthSharedProxyFailsClosedAndBoundsProbes(t *testing.T) {
	for _, scenario := range []string{"empty", "inactive", "missing", "five failures"} {
		t.Run(scenario, func(t *testing.T) {
			repo := &oauthDedicatedProxyRepoStub{}
			prober := &oauthDedicatedProberStub{results: map[string]*ProxyExitInfo{}}
			ids := []int64{1}
			switch scenario {
			case "inactive":
				p := oauthProxyFixture(1, 0)
				p.Status = "inactive"
				repo.items = []ProxyWithAccountCount{p}
			case "missing":
				repo.items = []ProxyWithAccountCount{oauthProxyFixture(2, 0)}
			case "five failures":
				ids = nil
				for id := int64(1); id <= 6; id++ {
					ids = append(ids, id)
					repo.items = append(repo.items, oauthProxyFixture(id, 1))
				}
				prober.results["proxy-6.test"] = oauthExitFixture("8.8.8.8")
			}
			input := &CreateAccountInput{}
			err := (&adminServiceImpl{proxyRepo: repo, proxyProber: prober}).applyOAuthSharedProxy(oauthProxyTestContext(), input, ids)
			require.ErrorContains(t, err, "no healthy proxy in the configured shared pool")
			require.Nil(t, input.ProxyID)
			if scenario == "five failures" {
				require.Len(t, prober.calls, 5)
				require.NotContains(t, prober.calls, "proxy-6.test")
			} else {
				require.Empty(t, prober.calls)
			}
		})
	}
}

func TestOAuthSharedProxyRequiresTransactionAndHonorsCancellation(t *testing.T) {
	repo := &oauthDedicatedProxyRepoStub{}
	prober := &oauthDedicatedProberStub{}
	svc := &adminServiceImpl{proxyRepo: repo, proxyProber: prober}
	require.ErrorContains(t, svc.applyOAuthSharedProxy(context.Background(), &CreateAccountInput{}, []int64{1}), "requires an account transaction")
	require.False(t, repo.locked)
	ctx, cancel := context.WithCancel(oauthProxyTestContext())
	cancel()
	require.Error(t, svc.applyOAuthSharedProxy(ctx, &CreateAccountInput{}, []int64{1}))
	require.Empty(t, prober.calls)
}

func TestOpenAIAccountDefaultsSharedPoolValidation(t *testing.T) {
	for _, tc := range []struct {
		name      string
		ids       []int64
		countries []string
		invalid   bool
	}{
		{name: "absent"}, {name: "empty", ids: []int64{}}, {name: "valid", ids: []int64{2, 1}},
		{name: "zero", ids: []int64{0}, invalid: true}, {name: "negative", ids: []int64{-1}, invalid: true},
		{name: "duplicate", ids: []int64{1, 1}, invalid: true}, {name: "conflict", ids: []int64{1}, countries: []string{"US"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := nativeAccountDefaultsFixture()
			c.OpenAIAccountDefaults.SharedProxyIDs = tc.ids
			c.OpenAIAccountDefaults.DedicatedProxyCountryCodes = tc.countries
			err := ValidateOAuthAutoConfig(c)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestOpenAIAccountDefaultsSharedPoolPreservesExplicitProxyAndDirect(t *testing.T) {
	for _, id := range []int64{0, 77} {
		t.Run(fmt.Sprint(id), func(t *testing.T) {
			c := nativeAccountDefaultsFixture()
			c.OpenAIAccountDefaults.SharedProxyIDs = []int64{1}
			input := nativeCreationInput()
			input.ProxyID = &id
			// No selector or transaction: explicit inputs must bypass shared selection.
			require.NoError(t, nativeAccountDefaultsService(t, c).ApplyOAuthAutoConfig(t.Context(), input))
			require.Equal(t, id, *input.ProxyID)
			a, err := buildAccountForCreate(input, input.Extra)
			require.NoError(t, err)
			if id == 0 {
				require.Nil(t, a.ProxyID)
			} else {
				require.Equal(t, id, *a.ProxyID)
			}
		})
	}
}

func TestOpenAIAccountDefaultsSharedPoolKeepsTemplateAndScopeContracts(t *testing.T) {
	c := nativeAccountDefaultsFixture()
	c.OpenAIAccountDefaults.SharedProxyIDs = []int64{1}
	self := 6
	c.OpenAIAccountDefaults.SelfPriority = &self
	svc := nativeAccountDefaultsService(t, c)
	svc.proxyRepo = &oauthDedicatedProxyRepoStub{items: []ProxyWithAccountCount{oauthProxyFixture(1, 9)}}
	svc.proxyProber = &oauthDedicatedProberStub{results: map[string]*ProxyExitInfo{"proxy-1.test": oauthExitFixture("8.8.8.8")}}
	input := nativeCreationInput()
	input.Name = "#Self native"
	input.Credentials["plan_type"] = "free"
	load := 99
	input.LoadFactor = &load
	require.NoError(t, svc.ApplyOAuthAutoConfig(oauthProxyTestContext(), input))
	require.EqualValues(t, 1, *input.ProxyID)
	require.Equal(t, "#Self native", input.Name)
	require.Equal(t, 6, input.Priority)
	require.Equal(t, 2, input.Concurrency)
	require.Nil(t, input.LoadFactor)
	require.Equal(t, []int64{2, 9}, input.GroupIDs)
	require.Equal(t, map[string]any{"gpt-6-luna": "gpt-6-luna"}, input.Credentials["model_mapping"])
	require.Equal(t, "whitelist", input.Credentials[OpenAIModelMappingModeKey])
	for _, scope := range []string{"PAT", "AgentIdentity", "observer", "scoped admin"} {
		t.Run(scope, func(t *testing.T) {
			input := nativeCreationInput()
			ctx := t.Context()
			switch scope {
			case "PAT":
				input.Credentials["auth_mode"] = OpenAIAuthModePersonalAccessToken
			case "AgentIdentity":
				input.Credentials["auth_mode"] = OpenAIAuthModeAgentIdentity
			case "observer":
				ctx = WithObserverScope(ctx, []int64{9})
			case "scoped admin":
				ctx = adminaccess.WithPrincipal(ctx, &adminaccess.Principal{})
			}
			require.NoError(t, nativeAccountDefaultsService(t, c).ApplyOAuthAutoConfig(ctx, input))
			require.Nil(t, input.ProxyID)
			require.Equal(t, []int64{9}, input.GroupIDs)
			require.NotContains(t, input.Credentials, "model_mapping")
			require.Nil(t, input.InitialQualityPlan)
			require.Nil(t, input.InitialAdmissionPlan)
		})
	}
}

func TestNativeOAuthCreationSharedPoolPersistsBindingAndRollsBackFailure(t *testing.T) {
	for _, healthy := range []bool{false, true} {
		t.Run(fmt.Sprint(healthy), func(t *testing.T) {
			svc, repo, m, settings := nativeCreationFixture(t)
			var c OAuthAutoConfig
			require.NoError(t, json.Unmarshal([]byte(settings.raw), &c))
			c.OpenAIAccountDefaults.SharedProxyIDs = []int64{1}
			raw, err := json.Marshal(c)
			require.NoError(t, err)
			settings.raw = string(raw)
			svc.proxyRepo = &oauthDedicatedProxyRepoStub{items: []ProxyWithAccountCount{oauthProxyFixture(1, 8)}}
			prober := &oauthDedicatedProberStub{results: map[string]*ProxyExitInfo{}}
			if healthy {
				prober.results["proxy-1.test"] = oauthExitFixture("8.8.8.8")
			}
			svc.proxyProber = prober
			m.ExpectBegin()
			if healthy {
				m.ExpectCommit()
			} else {
				m.ExpectRollback()
			}
			a, err := svc.CreateAccount(t.Context(), nativeCreationInput())
			if healthy {
				require.NoError(t, err)
				require.EqualValues(t, 1, *a.ProxyID)
				require.EqualValues(t, 1, *repo.a.ProxyID)
				require.True(t, GatewayBorrowInitialReadyMatchesCurrent(a))
				require.Equal(t, 1, repo.recorded)
			} else {
				require.Error(t, err)
				require.Nil(t, a)
				require.Nil(t, repo.a)
				require.Zero(t, repo.recorded)
			}
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
