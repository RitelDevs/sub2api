package service

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

// OpenAIAccountDefaultsConfig extends the existing create-time OAuth policy.
// Exclusions are keyed by existing account ID and used by model reconciliation.
type OpenAIAccountDefaultsConfig struct {
	Enabled                    bool                            `json:"enabled"`
	SelfPriority               *int                            `json:"self_priority,omitempty"`
	Templates                  map[string]OAuthAccountTemplate `json:"templates"`
	ModelExclusions            map[string][]string             `json:"model_exclusions,omitempty"`
	DedicatedProxyCountryCodes []string                        `json:"dedicated_proxy_country_codes,omitempty"`
	LegacyAccountIDs           []int64                         `json:"legacy_account_ids,omitempty"`
}

type OAuthAccountTemplate struct {
	NamePrefix    string                  `json:"name_prefix,omitempty"`
	Concurrency   int                     `json:"concurrency"`
	LoadFactor    *int                    `json:"load_factor"`
	Priority      int                     `json:"priority"`
	ModelMappings []OAuthModelMappingRule `json:"model_mappings"`
}

func oauthAccountDefaultsPlanKey(plan string) string {
	switch plan {
	case "self_serve_business_prolite", "business_premium":
		return "business_premium"
	case "self_serve_business_usage_based", "team", "business":
		return "business"
	case "free", "go", "plus", "prolite", "pro", "promax":
		return plan
	default:
		return ""
	}
}

// OAuthAccountDefaultsTemplateFor resolves supported provider plan names and
// returns a copy so per-account changes cannot mutate the saved policy.
func OAuthAccountDefaultsTemplateFor(c *OpenAIAccountDefaultsConfig, plan string) (OAuthAccountTemplate, bool) {
	if c == nil || !c.Enabled {
		return OAuthAccountTemplate{}, false
	}
	key := oauthAccountDefaultsPlanKey(plan)
	if key == "" {
		return OAuthAccountTemplate{}, false
	}
	template, ok := c.Templates[key]
	if !ok && key == "business_premium" {
		// Existing seven-tier policies keep their Business behavior until split.
		template, ok = c.Templates["business"]
	}
	if !ok {
		return OAuthAccountTemplate{}, false
	}
	template.ModelMappings = slices.Clone(template.ModelMappings)
	if template.LoadFactor != nil {
		lf := *template.LoadFactor
		template.LoadFactor = &lf
	}
	return template, true
}

// OAuthAccountDefaultsModelMapping is an exact whitelist projection. A new
// account has no ID exclusions; existing accounts supply their saved exclusions.
func OAuthAccountDefaultsModelMapping(template OAuthAccountTemplate, exclusions []string) map[string]any {
	mapping := make(map[string]any, len(template.ModelMappings))
	for _, rule := range template.ModelMappings {
		from := strings.TrimSpace(rule.From)
		if !slices.Contains(exclusions, from) {
			mapping[from] = strings.TrimSpace(rule.To)
		}
	}
	return mapping
}

func validateOpenAIAccountDefaults(c *OpenAIAccountDefaultsConfig) error {
	if c == nil {
		return nil
	}
	bad := func(message string) error {
		return infraerrors.BadRequest("AUTO_CONFIG_OPENAI_DEFAULTS_INVALID", message)
	}
	legacyIDs := make(map[int64]bool, len(c.LegacyAccountIDs))
	for _, id := range c.LegacyAccountIDs {
		if id <= 0 || legacyIDs[id] {
			return bad("legacy account IDs must be positive and unique")
		}
		legacyIDs[id] = true
	}
	if c.SelfPriority != nil && (*c.SelfPriority < 0 || *c.SelfPriority > 10000) {
		return bad("Self priority must be 0–10000")
	}
	if len(c.Templates) > 8 || (c.Enabled && len(c.Templates) == 0) {
		return bad("select between one and eight OpenAI account templates")
	}
	for key, template := range c.Templates {
		if len(template.NamePrefix) > 100 || strings.IndexFunc(template.NamePrefix, unicode.IsControl) >= 0 {
			return bad("template name prefix must be at most 100 bytes without control characters")
		}
		if key == "" || oauthAccountDefaultsPlanKey(key) != key {
			return bad("unsupported OpenAI account template key")
		}
		if template.Priority < 0 || template.Priority > 10000 || template.Concurrency < 1 || template.Concurrency > 10000 ||
			(template.LoadFactor != nil && (*template.LoadFactor < 1 || *template.LoadFactor > 10000)) {
			return bad("template priority must be 0–10000; concurrency and load factor must be 1–10000")
		}
		if len(template.ModelMappings) == 0 {
			return bad("select at least one model for each OpenAI account template")
		}
		if err := validateOAuthModelMappings(template.ModelMappings); err != nil {
			return err
		}
	}
	for id, exclusions := range c.ModelExclusions {
		accountID, err := strconv.ParseInt(id, 10, 64)
		if err != nil || accountID <= 0 || strconv.FormatInt(accountID, 10) != id {
			return bad("model exclusions require positive account IDs")
		}
		if len(exclusions) > 100 {
			return bad("at most 100 model exclusions per account")
		}
		seen := make(map[string]bool, len(exclusions))
		for _, model := range exclusions {
			if model != strings.TrimSpace(model) || seen[model] {
				return bad("model exclusions must be unique model names")
			}
			if err := validateOAuthModelMappings([]OAuthModelMappingRule{{From: model, To: model}}); err != nil {
				return err
			}
			seen[model] = true
		}
	}
	if len(c.DedicatedProxyCountryCodes) > 249 {
		return bad("too many dedicated proxy country codes")
	}
	seen := make(map[string]bool, len(c.DedicatedProxyCountryCodes))
	for _, code := range c.DedicatedProxyCountryCodes {
		if len(code) != 2 || code[0] < 'A' || code[0] > 'Z' || code[1] < 'A' || code[1] > 'Z' || seen[code] {
			return bad("dedicated proxy countries must be unique uppercase two-letter codes")
		}
		seen[code] = true
	}
	return nil
}

func openAIAccountDefaultsInputEligible(input *CreateAccountInput) bool {
	if input == nil || input.Platform != PlatformOpenAI || input.Type != AccountTypeOAuth {
		return false
	}
	a := &Account{Platform: input.Platform, Type: input.Type, Credentials: input.Credentials}
	return !a.IsOpenAIAgentIdentity() && !a.IsOpenAIPersonalAccessToken()
}

func (s *adminServiceImpl) applyOpenAIAccountDefaults(ctx context.Context, input *CreateAccountInput, c *OpenAIAccountDefaultsConfig) error {
	a := &Account{Platform: input.Platform, Type: input.Type, Credentials: input.Credentials}
	template, ok := OAuthAccountDefaultsTemplateFor(c, a.GetCredential("plan_type"))
	if !ok {
		return infraerrors.BadRequest("AUTO_CONFIG_OPENAI_PLAN_UNSUPPORTED", "no automatic configuration template for this OpenAI plan")
	}
	applyOpenAIAccountTemplateFields(input, c, template)
	input.Credentials = maps.Clone(input.Credentials)
	if input.Credentials == nil {
		input.Credentials = map[string]any{}
	}
	input.Credentials["model_mapping"] = OAuthAccountDefaultsModelMapping(template, nil)
	input.Credentials[OpenAIModelMappingModeKey] = "whitelist"
	if len(c.DedicatedProxyCountryCodes) > 0 {
		return s.applyOAuthDedicatedProxy(ctx, input, c.DedicatedProxyCountryCodes)
	}
	return nil
}

// Capacity/name policy is shared with token-only accounts without enrolling them
// in model/group/proxy initialization or initial-quality admission.
func applyOpenAIAccountTemplateFields(input *CreateAccountInput, c *OpenAIAccountDefaultsConfig, template OAuthAccountTemplate) {
	input.Priority = template.Priority
	input.Concurrency = template.Concurrency
	// nil is an explicit "use concurrency" default, including imported accounts.
	input.LoadFactor = template.LoadFactor
	if strings.HasPrefix(input.Name, "#Self") {
		if c.SelfPriority != nil {
			input.Priority = *c.SelfPriority
		}
		return
	}
	if template.NamePrefix != "" && !strings.HasPrefix(input.Name, template.NamePrefix) {
		input.Name = template.NamePrefix + input.Name
	}
}

// This name rule applies to every platform, but only to creation or explicit
// name/priority edits. It does not turn ordinary updates into a template sync.
func (s *adminServiceImpl) configuredSelfPriority(ctx context.Context, name string) (*int, error) {
	if !strings.HasPrefix(name, "#Self") || s.settingService == nil {
		return nil, nil
	}
	c, err := getOAuthAutoConfigForCreation(ctx, s.settingService)
	if err != nil {
		return nil, err
	}
	if c.OpenAIAccountDefaults == nil || c.OpenAIAccountDefaults.SelfPriority == nil {
		return nil, nil
	}
	priority := *c.OpenAIAccountDefaults.SelfPriority
	return &priority, nil
}
