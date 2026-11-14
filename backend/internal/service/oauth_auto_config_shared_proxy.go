package service

import (
	"cmp"
	"context"
	"maps"
	"slices"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// Shared selection uses only the approved IDs, including occupied proxies and
// reused exit IPs. A non-nil proxy is explicit, including zero for direct access.
func (s *adminServiceImpl) applyOAuthSharedProxy(ctx context.Context, input *CreateAccountInput, ids []int64) error {
	if input.ProxyID != nil || len(ids) == 0 {
		return nil
	}
	unavailable := func(message string) error {
		return infraerrors.ServiceUnavailable("AUTO_CONFIG_PROXY_UNAVAILABLE", message)
	}
	if dbent.TxFromContext(ctx) == nil {
		return unavailable("shared proxy selection requires an account transaction")
	}
	selector, ok := s.proxyRepo.(oauthAccountProxySelector)
	if !ok || s.proxyProber == nil {
		return unavailable("shared proxy selection is unavailable")
	}
	// Match the existing dedicated mode's administrator request and probe bounds.
	selectionCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := selector.LockAccountProxySelection(selectionCtx); err != nil {
		return unavailable("could not lock shared proxy selection")
	}
	allowed := make(map[int64]bool, len(ids))
	for _, id := range ids {
		allowed[id] = true
	}
	var candidates []ProxyWithAccountCount
	var listed int64
	now := time.Now()
	for page := 1; ; page++ {
		items, meta, err := selector.ListAccountProxySelection(selectionCtx, pagination.PaginationParams{Page: page, PageSize: 100, SortBy: "id", SortOrder: "asc"})
		if err != nil {
			return unavailable("could not read shared proxy candidates")
		}
		for _, p := range items {
			if allowed[p.ID] && p.IsActive() && !p.IsExpired(now) {
				candidates = append(candidates, p)
			}
		}
		listed += int64(len(items))
		if meta == nil {
			return unavailable("shared proxy candidate pagination is unavailable")
		}
		if listed >= meta.Total {
			break
		}
		if len(items) == 0 {
			return unavailable("shared proxy candidate pagination is incomplete")
		}
	}
	slices.SortFunc(candidates, func(a, b ProxyWithAccountCount) int {
		if count := cmp.Compare(a.AccountCount, b.AccountCount); count != 0 {
			return count
		}
		return cmp.Compare(a.ID, b.ID)
	})
	for _, p := range candidates[:min(5, len(candidates))] {
		if selectionCtx.Err() != nil {
			return unavailable("shared proxy verification timed out or was cancelled")
		}
		info, _, err := s.proxyProber.ProbeProxy(selectionCtx, p.URL())
		if _, valid := oauthProxyPublicIP(info); err != nil || !valid {
			continue
		}
		if info.Timezone != "" && !isAllowedOpenAIRequestTimezone(info.Timezone) {
			continue
		}
		id := p.ID
		input.ProxyID = &id
		if info.Timezone != "" {
			input.Extra = maps.Clone(input.Extra)
			if input.Extra == nil {
				input.Extra = map[string]any{}
			}
			input.Extra[openAIRequestTimezoneExtraKey] = info.Timezone
		}
		return nil
	}
	return unavailable("no healthy proxy in the configured shared pool")
}
