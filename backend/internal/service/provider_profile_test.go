//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProviderProfile_Mistral(t *testing.T) {
	profile := LookupProviderProfile(PlatformMistral)
	require.NotNil(t, profile)
	require.True(t, IsMultiProtocolAPIKeyProvider(PlatformMistral))
	require.False(t, IsCNProvider(PlatformMistral))
	require.Equal(t, AccountModePayG, profile.DefaultMode)
	require.Equal(t, "mistral-small-latest", profile.DefaultTestModel)
	require.Len(t, profile.Modes, 1)
	require.Equal(t, map[string]string{APIProtocolChatCompletions: "https://api.mistral.ai/v1"}, profile.Modes[AccountModePayG].BaseURLs)
	require.False(t, profile.SupportsProtocol(AccountModePayG, APIProtocolResponses))
	require.False(t, profile.SupportsProtocol(AccountModePayG, APIProtocolAnthropic))
	require.True(t, upstreamBillingProbeTargetIsOfficialAPI(DefaultMistralBaseURL))
	for _, protocol := range []string{"", APIProtocolAdaptive, APIProtocolChatCompletions} {
		account := &Account{Platform: PlatformMistral, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": protocol}}
		require.Equal(t, DefaultMistralBaseURL, account.GetOpenAIBaseURL())
		for _, inbound := range []string{APIProtocolChatCompletions, APIProtocolResponses, APIProtocolAnthropic} {
			require.Equal(t, APIProtocolChatCompletions, resolveUpstreamProtocol(account, inbound, "zai-glm-5-3", nil))
		}
	}
	for _, accountType := range []string{AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken, AccountTypeUpstream, ""} {
		t.Run(accountType, func(t *testing.T) {
			account, err := buildAccountForCreate(&CreateAccountInput{
				Platform: PlatformMistral, Type: accountType,
				Credentials: map[string]any{"api_key": "fixture-mistral-key"},
			}, map[string]any{})
			if accountType == AccountTypeAPIKey {
				require.NoError(t, err)
				require.Equal(t, PlatformMistral, account.Platform)
			} else {
				require.EqualError(t, err, "mistral accounts only support apikey credentials")
			}
		})
	}
}

func TestMistralAccountUpdatePreservesAPIKey(t *testing.T) {
	for _, accountType := range []string{"", AccountTypeAPIKey, AccountTypeOAuth, AccountTypeSetupToken} {
		t.Run(accountType, func(t *testing.T) {
			stored := &Account{ID: 7, Platform: PlatformMistral, Type: AccountTypeAPIKey, Status: StatusActive,
				Credentials: map[string]any{"api_key": "fixture-stored-key", "base_url": DefaultMistralBaseURL}, Extra: map[string]any{}}
			repo := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{7: stored}}
			svc := &adminServiceImpl{accountRepo: repo}
			_, err := svc.UpdateAccount(context.Background(), 7, &UpdateAccountInput{
				Type: accountType, Credentials: map[string]any{"base_url": "https://relay.example.com/v1"},
			})
			if accountType == "" || accountType == AccountTypeAPIKey {
				require.NoError(t, err)
				require.Equal(t, "fixture-stored-key", stored.GetCredential("api_key"))
				require.Equal(t, "https://relay.example.com/v1", stored.GetCredential("base_url"))
			} else {
				require.EqualError(t, err, "mistral accounts only support apikey credentials")
				require.Empty(t, repo.updatedAccounts)
			}
		})
	}
}

func TestProviderProfile_ProfilesMatchMultiProtocolPlatforms(t *testing.T) {
	t.Parallel()

	for _, platform := range []string{
		PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok,
		PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo, PlatformComposite, "",
	} {
		want := IsCNProvider(platform) || platform == PlatformOpenCodeGo
		require.Equal(t, want, IsMultiProtocolAPIKeyProvider(platform), platform)
		require.Equal(t, want, LookupProviderProfile(platform) != nil, platform)
	}

	for platform, profile := range providerProfiles {
		require.Equal(t, platform, profile.Platform)
		_, ok := profile.Modes[profile.DefaultMode]
		require.True(t, ok, "%s default mode %q must be registered", platform, profile.DefaultMode)
	}
}

func TestProviderProfile_BaseURLKeysAreNativeProtocols(t *testing.T) {
	t.Parallel()

	for platform, profile := range providerProfiles {
		for mode, endpoints := range profile.Modes {
			require.NotEmpty(t, endpoints.BaseURLs[APIProtocolChatCompletions], "%s/%s must have a chat_completions base", platform, mode)
			for protocol, baseURL := range endpoints.BaseURLs {
				require.True(t, isNativeUpstreamProtocol(protocol), "%s/%s has unknown protocol key %q", platform, mode, protocol)
				require.NotEmpty(t, baseURL, "%s/%s/%s", platform, mode, protocol)
			}
		}
	}
}
