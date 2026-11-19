package repository

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// The adapter retains its wiring interfaces, but no longer accepts a ticket
// source or exposes a pool to HTTP, WebSocket, verification or status callers.
type astraRoutingUpstream struct {
	delegate          service.HTTPUpstream
	cfg               *config.Config
	publishedSettings atomic.Pointer[config.AstraRoutingSettings]
	quotaMu           sync.Mutex
	quotaBackoff      map[astraTargetQuotaKey]astraTargetQuotaBackoff
	tickets           service.AstraTicketLifecycle
	rotationCache     astraRotationCache
	listNodes         func() ([]mihomo.AstraNode, error)
	pinNode           func(context.Context, mihomo.AstraNode) (string, func(), error)
	preparer          func(context.Context, int64) error
	preparing         atomic.Bool
	mu                sync.Mutex
	pool              *codexGatewayPinUpstream
}

func (*astraRoutingUpstream) SetAstraTicketLifecycle(service.AstraTicketLifecycle) {}
func (*astraRoutingUpstream) unifiedTickets(context.Context) bool                  { return false }
func (*astraRoutingUpstream) current(context.Context) *codexGatewayPinUpstream     { return nil }
func (*astraRoutingUpstream) SetAstraGatewayHistoryRecorder(func(service.AstraGatewayHistoryRecord, bool)) {
}
func (*astraRoutingUpstream) SetAstraGatewayPreparer(func(context.Context, int64) error) {}

func (s *astraRoutingUpstream) ApplyAstraRoutingSettings(_, settings config.AstraRoutingSettings) {
	encoded, _ := json.Marshal(settings)
	var immutable config.AstraRoutingSettings
	_ = json.Unmarshal(encoded, &immutable)
	s.publishedSettings.Store(&immutable)
}

func (s *astraRoutingUpstream) settings(ctx context.Context) config.AstraRoutingSettings {
	settings := s.cfg.AstraRouting(ctx)
	if published := s.publishedSettings.Load(); published != nil {
		return *published
	}
	return settings
}

func (s *astraRoutingUpstream) strictBorrowRoute(ctx context.Context, id int64, model string) bool {
	if _, required := service.GatewayBorrowRequiredModelFromContext(ctx); required || service.IsAstraSourceAcquisition(ctx) {
		return true
	}
	pool := s.requiredPool(ctx, id)
	return pool != nil && (model == "" || pool.TargetRequiresModel(id, model))
}

// Consult membership only as a deny policy; never open the pool.
func (s *astraRoutingUpstream) requiredPool(ctx context.Context, id int64) *config.CodexGatewayPinConfig {
	settings := s.settings(ctx)
	if settings.CookiePool.Enabled && (settings.AutoQuality || settings.AccountScheduling || settings.QualityMode == config.GatewayBorrowAccountQualityMode) && slices.Contains(settings.CookiePool.TargetAccountIDs, id) {
		return &settings.CookiePool
	}
	return nil
}

func (s *astraRoutingUpstream) Do(r *http.Request, p string, a int64, n int) (*http.Response, error) {
	return s.DoWithTLS(r, p, a, n, nil)
}

func (s *astraRoutingUpstream) CheckRetiredAstraWSRoute(ctx context.Context, id int64, model string) error {
	if s.strictBorrowRoute(ctx, id, model) {
		return &service.OpenAITurnAdmissionError{Reason: "gateway_borrow_retired"}
	}
	return nil
}

func (s *astraRoutingUpstream) CheckRetiredAstraRoute(r *http.Request, id int64) error {
	ctx := r.Context()
	if _, required := service.GatewayBorrowRequiredModelFromContext(ctx); required || service.IsAstraSourceAcquisition(ctx) {
		return &service.OpenAITurnAdmissionError{Reason: "gateway_borrow_retired"}
	}
	if codexGatewayPinRequest(r) {
		if pool := s.requiredPool(ctx, id); pool != nil {
			if model := codexGatewayPinModel(r); model == "" || pool.TargetRequiresModel(id, model) {
				return &service.OpenAITurnAdmissionError{Reason: "gateway_borrow_retired"}
			}
		}
	}
	return nil
}

func (s *astraRoutingUpstream) DoWithTLS(r *http.Request, p string, a int64, n int, f *tlsfingerprint.Profile) (*http.Response, error) {
	if r != nil {
		if err := s.CheckRetiredAstraRoute(r, a); err != nil {
			return nil, err
		}
		if _, err := service.ValidateOpenAIRequestProxyEgress(r.Context(), a, p); err != nil {
			return nil, err
		}
	}
	return s.delegate.DoWithTLS(r, p, a, n, f)
}

func (s *astraRoutingUpstream) CodexGatewayPinWSHeader(id int64) (string, error) {
	if s.strictBorrowRoute(context.Background(), id, "") {
		return "", &service.OpenAITurnAdmissionError{Reason: "gateway_borrow_retired"}
	}
	return "", nil
}

func (*astraRoutingUpstream) PrepareAstraGateway(context.Context) error {
	return &service.OpenAITurnAdmissionError{Reason: "gateway_borrow_retired"}
}

func (s *astraRoutingUpstream) AstraGatewaySnapshot(ctx context.Context) service.AstraGatewayRuntime {
	settings := s.settings(ctx)
	result := service.AstraGatewayRuntime{GeneratedAt: time.Now(), Revision: settings.Revision, Sources: []service.AstraRouteStatus{}, Targets: []service.AstraRouteStatus{}, WS: []service.AstraWSStatus{}}
	for _, id := range settings.CookiePool.SourceAccountIDs {
		result.Sources = append(result.Sources, service.AstraRouteStatus{AccountID: id, State: "disabled", Reason: "gateway_borrow_retired"})
	}
	for _, id := range settings.CookiePool.TargetAccountIDs {
		for _, model := range settings.CookiePool.ModelsForTarget(id) {
			result.Targets = append(result.Targets, service.AstraRouteStatus{AccountID: id, Model: model, State: "disabled", Reason: "gateway_borrow_retired"})
		}
	}
	return result
}

// Only a hostname matching the known gateway namespace is exposed. JWT decoding
// is descriptive metadata, not signature verification or proof of actual routing.
var astraGatewayHostPattern = regexp.MustCompile(`^chat\.gateway\.unified-[0-9]+\.api\.openai\.com$`)

func astraRoutingHost(value string) string {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Host string `json:"host"`
	}
	if json.Unmarshal(raw, &claims) != nil {
		return ""
	}
	if !astraGatewayHostPattern.MatchString(claims.Host) {
		return ""
	}
	return claims.Host
}
