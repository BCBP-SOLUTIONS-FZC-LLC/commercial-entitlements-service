package service

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// AddonService manages quota addon packs and drawdowns with full shortfall lifecycle.
type AddonService struct {
	addons     port.AddonRepository
	drawdowns  port.DrawdownRepository
	shortfalls port.ShortfallRepository
	effectSvc  *EffectiveSetService
	txRunner   port.TxRunner
	log        port.Logger
}

// Ensure AddonService implements port.AddonService at compile time.
var _ port.AddonService = (*AddonService)(nil)

// NewAddonService wires up the AddonService.
func NewAddonService(
	addons port.AddonRepository,
	drawdowns port.DrawdownRepository,
	shortfalls port.ShortfallRepository,
	effectSvc *EffectiveSetService,
	txRunner port.TxRunner,
) *AddonService {
	return &AddonService{
		addons:     addons,
		drawdowns:  drawdowns,
		shortfalls: shortfalls,
		effectSvc:  effectSvc,
		txRunner:   txRunner,
	}
}

// WithLogger attaches a logger.
func (s *AddonService) WithLogger(l port.Logger) *AddonService {
	s.log = l
	return s
}

func (s *AddonService) logger() port.Logger {
	if s.log != nil {
		return s.log
	}
	return port.NopLogger{}
}

// AddAddon grants a new quota addon pack to a tenant.
func (s *AddonService) AddAddon(ctx context.Context, req port.AddAddonRequest) (*domain.QuotaAddon, error) {
	if req.TotalTokens <= 0 {
		return nil, domain.NewError(domain.ErrTokensRequired, "total_tokens must be positive")
	}
	if req.Reason == "" {
		return nil, domain.NewError(domain.ErrReasonRequired, "reason is required")
	}
	if err := validateLimitKey(req.LimitKey); err != nil {
		return nil, err
	}
	if req.ExpiresAt != nil && req.ExpiresAt.Before(time.Now().UTC()) {
		return nil, domain.NewError(domain.ErrExpiresAtInPast, "expires_at must be in the future")
	}

	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID:              uuid.New(),
		TenantID:        req.TenantID,
		LimitKey:        req.LimitKey,
		TotalTokens:     req.TotalTokens,
		RemainingTokens: req.TotalTokens,
		GrantedBy:       req.GrantedBy,
		Reason:          req.Reason,
		Status:          domain.AddonStatusActive,
		ExpiresAt:       req.ExpiresAt,
		RecordVersion:   1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	var final *domain.QuotaAddon
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.addons.Insert(txCtx, a); err != nil {
			return fmt.Errorf("insert addon: %w", err)
		}
		final = a
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}

	// Recompute (advisory).
	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseAddonChange); err != nil {
		s.logger().Warn("add addon: recompute failed",
			map[string]interface{}{"tenant_id": req.TenantID, "error": err.Error()})
	}
	return final, nil
}

// RemoveAddon revokes an active addon pack (must be fully unused).
func (s *AddonService) RemoveAddon(ctx context.Context, req port.RemoveAddonRequest) error {
	if req.Reason == "" {
		return domain.NewError(domain.ErrReasonRequired, "reason is required")
	}
	a, err := s.addons.GetByID(ctx, req.AddonID)
	if err != nil {
		return domain.NewError(domain.ErrAddonNotFound, "addon not found")
	}
	if a.TenantID != req.TenantID {
		return domain.NewError(domain.ErrForbidden, "addon does not belong to tenant")
	}
	if a.RemainingTokens < a.TotalTokens {
		return domain.NewError(domain.ErrPackPartiallyUsed, "cannot remove a partially-used addon pack")
	}

	now := time.Now().UTC()
	a.Status = domain.AddonStatusRevoked
	a.RemainingTokens = 0
	a.RecordVersion++
	a.UpdatedAt = now

	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		return s.addons.Update(txCtx, a)
	})
	if txErr != nil {
		return txErr
	}

	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseAddonChange); err != nil {
		s.logger().Warn("remove addon: recompute failed",
			map[string]interface{}{"tenant_id": req.TenantID, "error": err.Error()})
	}
	return nil
}

// ListAddons returns all quota addon packs for a tenant.
func (s *AddonService) ListAddons(ctx context.Context, tenantID uuid.UUID) ([]domain.QuotaAddon, error) {
	return s.addons.ListByTenant(ctx, tenantID, nil)
}

// RecordDrawdown draws against active addon packs FIFO by expiry (ENT-D11/D12).
// When a shortfall occurs it auto-accepts within tolerance or raises for operator
// review based on ShortfallAutoAcceptPolicy.
func (s *AddonService) RecordDrawdown(ctx context.Context, req port.RecordDrawdownRequest) (*domain.QuotaAddonDrawdown, error) {
	if req.PeriodStart.IsZero() {
		return nil, domain.NewError(domain.ErrInvalidPeriodStart, "period_start is required")
	}
	if req.TokensConsumed <= 0 {
		return nil, domain.NewError(domain.ErrTokensRequired, "tokens_consumed must be positive")
	}
	if err := validateLimitKey(req.LimitKey); err != nil {
		return nil, err
	}

	// Idempotency: check if request was already recorded.
	if req.RequestID != nil {
		if existing, err := s.drawdowns.GetByRequestID(ctx, req.TenantID, *req.RequestID); err == nil && existing != nil {
			return existing, nil
		}
	}

	// Load active addons ordered by expiry (soonest-expiring first).
	activeAddons, err := s.addons.ListActiveByTenantAndKey(ctx, req.TenantID, req.LimitKey)
	if err != nil {
		return nil, fmt.Errorf("list active addons: %w", err)
	}
	sortAddonsByExpiry(activeAddons)

	remaining := req.TokensConsumed
	var packDraws []domain.PackDraw
	var updatedAddons []*domain.QuotaAddon

	now := time.Now().UTC()
	for i := range activeAddons {
		if remaining <= 0 {
			break
		}
		a := &activeAddons[i]
		if a.RemainingTokens <= 0 {
			continue
		}
		draw := min64(a.RemainingTokens, remaining)
		packDraws = append(packDraws, domain.PackDraw{AddonID: a.ID, Drawn: draw})
		a.RemainingTokens -= draw
		remaining -= draw
		if a.RemainingTokens == 0 {
			a.Status = domain.AddonStatusConsumed
		}
		a.RecordVersion++
		a.UpdatedAt = now
		updatedAddons = append(updatedAddons, a)
	}

	tokensDrawn := req.TokensConsumed - remaining
	shortfall := remaining

	drawdown := &domain.QuotaAddonDrawdown{
		ID:             uuid.New(),
		TenantID:       req.TenantID,
		LimitKey:       req.LimitKey,
		TokensConsumed: tokensDrawn,
		PeriodStart:    req.PeriodStart,
		RequestID:      req.RequestID,
		RecordedAt:     now,
	}

	var sfRecord *domain.QuotaAddonShortfall
	if shortfall > 0 {
		sfRecord, err = s.buildShortfall(ctx, drawdown.ID, req.TenantID, req.LimitKey, req.TokensConsumed, tokensDrawn, shortfall)
		if err != nil {
			return nil, err
		}
	}

	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		for _, a := range updatedAddons {
			if err := s.addons.Update(txCtx, a); err != nil {
				return fmt.Errorf("update addon %s: %w", a.ID, err)
			}
		}
		if err := s.drawdowns.Insert(txCtx, drawdown); err != nil {
			return fmt.Errorf("insert drawdown: %w", err)
		}
		if sfRecord != nil {
			if err := s.shortfalls.Insert(txCtx, sfRecord); err != nil {
				return fmt.Errorf("insert shortfall: %w", err)
			}
		}
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}

	// Recompute (advisory).
	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseAddonChange); err != nil {
		s.logger().Warn("record drawdown: recompute failed",
			map[string]interface{}{"tenant_id": req.TenantID, "error": err.Error()})
	}
	return drawdown, nil
}

// buildShortfall applies ENT-D11/D12: auto-accept within tolerance or raise for review.
func (s *AddonService) buildShortfall(ctx context.Context, drawdownID, tenantID uuid.UUID, limitKey domain.LimitKey, requested, drawn, shortfallTokens int64) (*domain.QuotaAddonShortfall, error) {
	now := time.Now().UTC()
	sf := &domain.QuotaAddonShortfall{
		ID:          uuid.New(),
		TenantID:    tenantID,
		DrawdownID:  drawdownID,
		LimitKey:    limitKey,
		Requested:   requested,
		Available:   drawn,
		Shortfall:   shortfallTokens,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	// ENT-D11: auto-accept if shortfall is small relative to request.
	// Count recent shortfalls for this tenant (ENT-D12).
	recentCount, _ := s.shortfalls.CountRecentForTenant(ctx, tenantID, 30 /* rolling 30-day window */)

	shortfallPct := float64(shortfallTokens) / float64(requested) * 100
	const autoAcceptMaxPct = 5.0  // 5% shortfall threshold
	const autoAcceptMaxAbs = 1000 // 1000 tokens absolute

	if shortfallPct <= autoAcceptMaxPct && shortfallTokens <= autoAcceptMaxAbs && recentCount == 0 {
		// ENT-D11: auto-accept.
		disp := domain.DispositionApprove
		sf.Status = domain.ShortfallStatusAutoAccepted
		sf.Disposition = &disp
		s.logger().Info("shortfall auto-accepted (ENT-D11)",
			map[string]interface{}{
				"tenant_id":        tenantID,
				"shortfall_tokens": shortfallTokens,
				"pct":              shortfallPct,
			})
	} else {
		// ENT-D12: escalate for operator review.
		sf.Status = domain.ShortfallStatusPendingApproval
		s.logger().Warn("shortfall escalated for review (ENT-D12)",
			map[string]interface{}{
				"tenant_id":        tenantID,
				"shortfall_tokens": shortfallTokens,
				"pct":              shortfallPct,
				"recent_count":     recentCount,
			})
	}

	return sf, nil
}

// ListDrawdowns returns drawdown records for a tenant+key.
func (s *AddonService) ListDrawdowns(ctx context.Context, tenantID uuid.UUID, limitKey domain.LimitKey) ([]domain.QuotaAddonDrawdown, error) {
	return s.drawdowns.ListByTenantAndKey(ctx, tenantID, limitKey)
}

// ResolveShortfall applies an operator disposition to a pending shortfall.
func (s *AddonService) ResolveShortfall(ctx context.Context, req port.ResolveShortfallRequest) (*domain.QuotaAddonShortfall, error) {
	sf, err := s.shortfalls.GetByID(ctx, req.ShortfallID)
	if err != nil {
		return nil, domain.NewError(domain.ErrShortfallNotFound, "shortfall not found")
	}
	if sf.Status == domain.ShortfallStatusApproved || sf.Status == domain.ShortfallStatusRejected {
		return nil, domain.NewError(domain.ErrShortfallAlreadyResolved, "shortfall already resolved")
	}
	if req.Disposition == domain.DispositionApprove &&
		(req.DispositionRef == nil || *req.DispositionRef == "") {
		return nil, domain.NewError(domain.ErrDispositionRefRequired, "disposition_ref required for approval")
	}

	now := time.Now().UTC()
	switch req.Disposition {
	case domain.DispositionApprove:
		sf.Status = domain.ShortfallStatusApproved
	case domain.DispositionReject:
		sf.Status = domain.ShortfallStatusRejected
	default:
		return nil, domain.NewError(domain.ErrInvalidAction, "invalid disposition")
	}
	sf.Disposition = &req.Disposition
	sf.DispositionBy = &req.DispositionBy
	sf.DispositionRef = req.DispositionRef
	sf.DispositionNote = req.Note
	sf.ResolvedAt = &now
	sf.UpdatedAt = now

	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		return s.shortfalls.Update(txCtx, sf)
	})
	if txErr != nil {
		return nil, txErr
	}
	return sf, nil
}

// ListShortfalls returns shortfalls for a tenant, optionally filtered by status.
func (s *AddonService) ListShortfalls(ctx context.Context, tenantID uuid.UUID, statusFilter []domain.ShortfallStatus) ([]domain.QuotaAddonShortfall, error) {
	return s.shortfalls.ListByTenant(ctx, tenantID, statusFilter)
}

// GetShortfall returns a shortfall by ID.
func (s *AddonService) GetShortfall(ctx context.Context, id uuid.UUID) (*domain.QuotaAddonShortfall, error) {
	return s.shortfalls.GetByID(ctx, id)
}

// ExpireAddons marks addon packs as expired and triggers recompute.
func (s *AddonService) ExpireAddons(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	expired, err := s.addons.ListExpired(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("list expired addons: %w", err)
	}
	count := 0
	for i := range expired {
		a := &expired[i]
		a.Status = domain.AddonStatusExpired
		a.RemainingTokens = 0
		a.RecordVersion++
		a.UpdatedAt = now
		txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
			return s.addons.Update(txCtx, a)
		})
		if txErr != nil {
			s.logger().Warn("expire addon failed",
				map[string]interface{}{"addon_id": a.ID, "tenant_id": a.TenantID, "error": txErr.Error()})
			continue
		}
		if _, err := s.effectSvc.Recompute(ctx, a.TenantID, domain.CauseAddonChange); err != nil {
			s.logger().Warn("expire addon recompute failed",
				map[string]interface{}{"tenant_id": a.TenantID, "error": err.Error()})
		}
		count++
	}
	return count, nil
}

func sortAddonsByExpiry(addons []domain.QuotaAddon) {
	sort.Slice(addons, func(i, j int) bool {
		ai, aj := addons[i].ExpiresAt, addons[j].ExpiresAt
		if ai == nil && aj == nil {
			return false
		}
		if ai == nil {
			return false // nil expiry = never expires, so sort last
		}
		if aj == nil {
			return true
		}
		return ai.Before(*aj)
	})
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
