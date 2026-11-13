package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func isMultiGroupBillingInfoRequest(c *gin.Context, key *service.APIKey) bool {
	return c.Request.URL.Path == "/v1/sub2api/billing" && len(key.ConfiguredGroupIDs()) > 1
}

const modelListingRefreshKey = "api_key.model_listing_refresh"
const modelListingClientPolicyKey = "api_key.model_listing_client_policy"

type modelListingRefresh func(context.Context) ([]*service.APIKey, error)

func GetModelListingGroups(c *gin.Context) ([]*service.APIKey, bool) {
	return GetAPIKeyModelDiscoveryGroups(c)
}

// RefreshModelListingGroups runs synchronously after a queued directory request
// acquires its key slot. Queue revalidation pins one billing group; discovery
// must also recheck every other bound group's current permissions.
func RefreshModelListingGroups(c *gin.Context) ([]*service.APIKey, error) {
	value, _ := c.Get(modelListingRefreshKey)
	refresh, ok := value.(modelListingRefresh)
	if !ok {
		groups, _ := GetAPIKeyModelDiscoveryGroups(c)
		return groups, nil
	}
	return refresh(c.Request.Context())
}

func selectAPIKeyRequestGroup(c *gin.Context, keys *service.APIKeyService, subscriptions *service.SubscriptionService, cfg *config.Config, key *service.APIKey, google bool) (*service.APIKey, error) {
	var err error
	key, err = applySecurityPenalty(c, keys, key)
	if err != nil {
		if google {
			abortWithGoogleError(c, infraerrors.Code(err), infraerrors.Message(err))
		} else {
			AbortWithError(c, infraerrors.Code(err), infraerrors.Reason(err), infraerrors.Message(err))
		}
		return nil, err
	}
	if key == nil || c.IsAborted() {
		return nil, nil
	}
	if target, ok := c.Get("security_penalty.original_group_id"); ok && target != nil {
		return key, nil
	}
	if len(key.ConfiguredGroupIDs()) < 2 || isMultiGroupBillingInfoRequest(c, key) || c.Request.URL.Path == "/v1/usage" || isAsyncImageTaskRead(c.Request.Method, c.Request.URL.Path) {
		return key, nil
	}
	var models []string
	var body []byte
	if model := groupModelAllowlistModelFromParams(c); model != "" {
		models = []string{model}
	}
	if c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPatch {
		var ok bool
		body, ok = readAdmissionRequestBody(c)
		if !ok {
			return nil, nil
		}
		if len(models) == 0 {
			models = requestmodel.FromBodyCandidates(c.FullPath(), c.GetHeader("Content-Type"), body)
		}
	}
	if len(models) == 0 && c.Query("model") != "" {
		models = []string{c.Query("model")}
	}
	if service.IsAPIKeyModelPolicyPath(c.Request.URL.Path) {
		models = service.APIKeyModelCandidates(key, models)
	}
	kind := "token"
	path := c.Request.URL.Path
	if strings.Contains(path, "/images/") {
		kind = "image"
	}
	if strings.Contains(path, "/videos") {
		kind = "video"
	}
	checkBilling := cfg.RunMode != config.RunModeSimple && c.Request.Method != http.MethodGet && !strings.HasSuffix(path, "/count_tokens")
	penaltyConfig, penaltyErr := keys.SecurityPenaltyConfig(c.Request.Context())
	if penaltyErr != nil {
		return nil, infraerrors.ServiceUnavailable("security_penalty_unavailable", "请求权限检查暂时不可用")
	}
	canRecoverBalance := false
	if checkBilling && key.User != nil && groupBalanceNeedsRecovery(key.User.Balance, cfg) {
		canRecoverBalance, err = keys.CanRecoverAutoCreditAdmission(c.Request.Context(), key)
		if err != nil {
			return nil, infraerrors.ServiceUnavailable("AUTO_CREDIT_UNAVAILABLE", "Credit authorization unavailable")
		}
	}
	accepts := func(candidate *service.APIKey) bool {
		group := candidate.Group
		if (path == "/v1/tools/search" || path == "/v1/tools/fetch") && group.Platform != service.PlatformDeepseek {
			return false
		}
		// Binding the restricted target must not make the normal cheapest-group
		// selector send healthy conversations there. Forced penalties returned
		// above already passed the normal target authorization checks.
		if penaltyConfig.Applies(key.UserID) && !penaltyConfig.ObserveOnly && penaltyConfig.Policy == "session_route_then_block" && penaltyConfig.TargetGroupID != nil && group.ID == *penaltyConfig.TargetGroupID {
			return false
		}
		if checkBilling && !group.IsSubscriptionType() && groupBalanceNeedsRecovery(candidate.User.Balance, cfg) && !canRecoverBalance {
			return false
		}
		if google && !groupAllowsGooglePlatform(path, group) {
			return false
		}
		if strings.HasPrefix(path, "/antigravity/") && group.Platform != service.PlatformAntigravity {
			return false
		}
		if kind == "image" && !service.GroupAllowsImageGeneration(group) {
			return false
		}
		if group.StreamOnly && c.Request.Method == http.MethodPost {
			if streamOnlyJSONRoutes[c.FullPath()] && !jsonBodyRequestsStream(body) {
				return false
			}
			if streamOnlyGeminiRoutes[c.FullPath()] && strings.HasSuffix(c.Param("modelAction"), ":generateContent") {
				return false
			}
		}
		if !isAPIKeyModelDiscoveryRequest(c) && group.BlockSubagents && service.IsCodexSubagentRequest(c, body) {
			return false
		}
		if group.CodexCLIOnly {
			policy := service.CodexRestrictionPolicy{EngineFingerprintSignals: openai.DefaultEngineFingerprintSignals}
			checkClient := !isAPIKeyModelDiscoveryRequest(c)
			if value, exists := c.Get(modelListingClientPolicyKey); exists {
				if loadPolicy, ok := value.(func() service.CodexRestrictionPolicy); ok {
					policy = loadPolicy()
					checkClient = true
				}
			}
			if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
				policy.EngineFingerprintSignals = nil
			}
			if checkClient && !service.DetectCodexGroupClient(c, group, policy, body).Matched {
				return false
			}
		}
		return true
	}
	ctx := context.WithValue(c.Request.Context(), ctxkey.UserID, key.UserID)
	var selected *service.APIKey
	if isAPIKeyModelDiscoveryRequest(c) {
		var groups []*service.APIKey
		groups, err = keys.ModelDiscoveryGroups(ctx, key, accepts)
		if err == nil {
			if len(groups) == 0 {
				err = infraerrors.Forbidden("NO_ELIGIBLE_API_KEY_GROUP", "No selected group permits model discovery")
			} else {
				SetAPIKeyModelDiscoveryGroups(c, groups)
				if key.ConcurrencyLimit > 0 {
					c.Set(modelListingRefreshKey, modelListingRefresh(func(ctx context.Context) ([]*service.APIKey, error) {
						latest, err := keys.GetByKeyUncached(ctx, key.Key)
						if err != nil {
							return nil, err
						}
						if latest == nil || latest.ID != key.ID || latest.UserID != key.UserID || latest.User == nil ||
							!latest.User.IsActive() || !latest.IsActive() || latest.IsExpired() || latest.IsQuotaExhausted() {
							return nil, infraerrors.Forbidden("MODEL_LISTING_UNAVAILABLE", "Model listing authorization changed")
						}
						return keys.ModelDiscoveryGroups(ctx, latest, accepts)
					}))
				}

				selected = groups[0]
			}
		}
	} else {
		selected, err = keys.SelectRequestGroup(ctx, key, models, kind, subscriptions, accepts, checkBilling)
	}
	if err != nil {
		status := infraerrors.Code(err)
		message, reason := infraerrors.Message(err), infraerrors.Reason(err)
		if status >= 500 {
			message = "No selected group is currently available for this request"
		}
		if google {
			abortWithGoogleError(c, status, message)
		} else {
			AbortWithError(c, status, reason, message)
		}
		return nil, err
	}
	SetOpsFallbackAPIKey(c, selected)
	return selected, nil
}

// Multi-group choice must use the same spendable floor as billing preflight;
// authentication's <=0 threshold remains unchanged for ordinary single groups.
func groupBalanceNeedsRecovery(balance float64, cfg *config.Config) bool {
	return balance <= 0 || cfg != nil && cfg.Billing.MinimumBalanceReserve > 0 && balance < cfg.Billing.MinimumBalanceReserve
}

// groupAllowsGooglePlatform 决定 Google 入站（/v1beta）能否使用这个分组。
// Gemini、Antigravity、Composite 维持原语义。其他分组只在 /v1beta/models 上放行，
// 后续把请求转成该分组上游实际支持的协议，而不是一律打上游 /v1beta/models。
func groupAllowsGooglePlatform(path string, group *service.Group) bool {
	if group == nil {
		return false
	}
	switch group.Platform {
	case service.PlatformGemini, service.PlatformAntigravity, service.PlatformComposite:
		return true
	}
	if strings.HasPrefix(path, "/antigravity/") {
		return false
	}
	if path != "/v1beta/models" && !strings.HasPrefix(path, "/v1beta/models/") {
		return false
	}
	return service.PlatformAcceptsGeminiInboundBridge(group.Platform)
}
