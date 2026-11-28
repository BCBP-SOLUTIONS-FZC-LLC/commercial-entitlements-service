package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ShortfallConfig holds the ENT-D11/D12 thresholds approved in the LLD
// (§12 configuration table). All five values are configurable without a code
// change per the ENT-D12 resolution. Defaults match the approved LLD values.
//
//	SHORTFALL_TOLERANCE_TOKENS = 10000
//	SHORTFALL_TOLERANCE_PCT    = 1
//	SHORTFALL_HIGH_VALUE_TOKENS = 250000
//	SHORTFALL_REPEAT_COUNT     = 2
//	SHORTFALL_REPEAT_WINDOW_MONTHS = 3
type ShortfallConfig struct {
	// ToleranceTokens: auto-accept shortfalls at or below this many tokens.
	// ENV: SHORTFALL_TOLERANCE_TOKENS. Default: 10000.
	ToleranceTokens int64
	// TolerancePct: auto-accept shortfalls at or below this % of reported
	// consumption (both ToleranceTokens AND TolerancePct must hold).
	// ENV: SHORTFALL_TOLERANCE_PCT. Default: 1.0.
	TolerancePct float64
	// HighValueTokens: escalate any shortfall at or above this size regardless
	// of tolerance. ENV: SHORTFALL_HIGH_VALUE_TOKENS. Default: 250000.
	HighValueTokens int64
	// RepeatCount: escalate when a tenant has at least this many shortfalls
	// within RepeatWindowMonths. ENV: SHORTFALL_REPEAT_COUNT. Default: 2.
	RepeatCount int
	// RepeatWindowMonths: sliding window for repeat detection (approximate —
	// converted to days as 30 × months).
	// ENV: SHORTFALL_REPEAT_WINDOW_MONTHS. Default: 3.
	RepeatWindowMonths int
}

// DefaultShortfallConfig returns the LLD §12 / ENT-D12 approved defaults.
func DefaultShortfallConfig() ShortfallConfig {
	return ShortfallConfig{
		ToleranceTokens:    10_000,
		TolerancePct:       1.0,
		HighValueTokens:    250_000,
		RepeatCount:        2,
		RepeatWindowMonths: 3,
	}
}

// AddonService manages quota addon packs and drawdowns with full shortfall lifecycle.
type AddonService struct {
	addons     port.AddonRepository
	drawdowns  port.DrawdownRepository
	shortfalls port.ShortfallRepository
	refunds    port.RefundRepository
	effectSvc  *EffectiveSetService
	txRunner   port.TxRunner
	log        port.Logger

	sfCfg ShortfallConfig

	// Observability hooks injected by the wiring layer so the core service
	// stays free of adapter imports.
	onDrawdown  func(key domain.LimitKey, hadShortfall bool)
	onShortfall func(key domain.LimitKey, status domain.ShortfallStatus)
}

// Ensure AddonService implements port.AddonService at compile time.
var _ port.AddonService = (*AddonService)(nil)

// NewAddonService wires up the AddonService. Shortfall thresholds default to
// the LLD §12 / ENT-D12 approved values; call WithShortfallConfig to override.
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
		sfCfg:      DefaultShortfallConfig(),
	}
}

// WithRefundRepository wires the quota_addon_refunds ledger that makes
// ApplyBillingRefund idempotent on billing_ref (F-3). Without it
// ApplyBillingRefund fails closed.
func (s *AddonService) WithRefundRepository(r port.RefundRepository) *AddonService {
	s.refunds = r
	return s
}

// WithShortfallConfig injects configurable ENT-D11/D12 thresholds. Called by
// cmd/server/main.go after reading the five SHORTFALL_* env vars (LLD §12).
func (s *AddonService) WithShortfallConfig(cfg ShortfallConfig) *AddonService {
	s.sfCfg = cfg
	return s
}

// WithLogger attaches a logger.
func (s *AddonService) WithLogger(l port.Logger) *AddonService {
	s.log = l
	return s
}

// WithAddonMetricsHooks registers observability callbacks invoked after
// RecordDrawdown succeeds. onDrawdown(key, hadShortfall) is always called.
// onShortfall(key, status) is called only when a shortfall was raised.
func (s *AddonService) WithAddonMetricsHooks(
	onDrawdown func(domain.LimitKey, bool),
	onShortfall func(domain.LimitKey, domain.ShortfallStatus),
) *AddonService {
	s.onDrawdown = onDrawdown
	s.onShortfall = onShortfall
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
		recordAudit(txCtx, s.log, port.AuditEntry{
			TenantID:   &req.TenantID,
			EntryType:  "billing.token_addon.granted",
			Actor:      port.AuditActor{Type: "platform_operator", ID: req.GrantedBy.String()},
			Target:     &port.AuditTarget{Type: "quota_addon", ID: a.ID.String()},
			OccurredAt: now,
			Metadata: map[string]any{
				"limit_key":    string(req.LimitKey),
				"total_tokens": req.TotalTokens,
				"reason":       req.Reason,
			},
			IdempotencyKey: addonAuditKey(a.ID, a.RecordVersion, a.UpdatedAt),
		})
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

// RemoveAddon revokes an active addon pack from which nothing was drawn.
// Refunded tokens are not use (ENT-D16): an undrawn pack that was partly
// refunded is still removable. Requires the refund ledger to tell refunds
// from draws; without it the pack is treated as drawn wherever
// remaining < total (fail closed).
func (s *AddonService) RemoveAddon(ctx context.Context, req port.RemoveAddonRequest) error {
	if req.Reason == "" {
		return domain.NewError(domain.ErrReasonRequired, "reason is required")
	}

	// Read and write inside the same transaction to close the CONC-1
	// read-modify-write race between RemoveAddon and concurrent RecordDrawdown.
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		a, err := s.addons.GetByID(txCtx, req.AddonID)
		if err != nil {
			return domain.NewError(domain.ErrAddonNotFound, "addon not found")
		}
		if a.TenantID != req.TenantID {
			return domain.NewError(domain.ErrForbidden, "addon does not belong to tenant")
		}
		if a.Status != domain.AddonStatusActive {
			return domain.NewError(domain.ErrInvalidAction,
				fmt.Sprintf("only an active addon pack can be removed (status %s)", a.Status))
		}
		if err := s.fillRefunded(txCtx, req.TenantID, []*domain.QuotaAddon{a}); err != nil {
			return err
		}
		if !a.Removable() {
			return domain.NewError(domain.ErrPackPartiallyUsed, "cannot remove a partially-used addon pack")
		}

		now := time.Now().UTC()
		a.Status = domain.AddonStatusRevoked
		a.RemainingTokens = 0
		a.RecordVersion++
		a.UpdatedAt = now

		if err := s.addons.Update(txCtx, a); err != nil {
			return err
		}
		recordAudit(txCtx, s.log, port.AuditEntry{
			TenantID:   &req.TenantID,
			EntryType:  "billing.token_addon.revoked",
			Actor:      port.AuditActor{Type: "platform_operator", ID: req.RemovedBy.String()},
			Target:     &port.AuditTarget{Type: "quota_addon", ID: a.ID.String()},
			OccurredAt: now,
			Metadata: map[string]any{
				"limit_key": string(a.LimitKey),
				"reason":    req.Reason,
			},
			IdempotencyKey: addonAuditKey(a.ID, a.RecordVersion, a.UpdatedAt),
		})
		return nil
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
// Each pack carries RefundedTokens from the refund ledger, so callers can
// show consumed vs refunded quantities and removability (ENT-D16).
func (s *AddonService) ListAddons(ctx context.Context, tenantID uuid.UUID) ([]domain.QuotaAddon, error) {
	list, err := s.addons.ListByTenant(ctx, tenantID, nil)
	if err != nil {
		return nil, err
	}
	ptrs := make([]*domain.QuotaAddon, len(list))
	for i := range list {
		ptrs[i] = &list[i]
	}
	if err := s.fillRefunded(ctx, tenantID, ptrs); err != nil {
		return nil, err
	}
	return list, nil
}

// fillRefunded sets RefundedTokens on each pack from the refund ledger, the
// system of record for refunded amounts. With no ledger wired it leaves 0.
func (s *AddonService) fillRefunded(ctx context.Context, tenantID uuid.UUID, packs []*domain.QuotaAddon) error {
	if s.refunds == nil || len(packs) == 0 {
		return nil
	}
	ids := make([]uuid.UUID, len(packs))
	for i, a := range packs {
		ids[i] = a.ID
	}
	sums, err := s.refunds.SumByAddon(ctx, tenantID, ids)
	if err != nil {
		return fmt.Errorf("sum refunded tokens: %w", err)
	}
	for _, a := range packs {
		a.RefundedTokens = sums[a.ID]
	}
	return nil
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
		sfRecord = s.buildShortfall(ctx, drawdown.ID, req.TenantID, req.LimitKey, req.TokensConsumed, tokensDrawn, shortfall)
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

	// Observability hooks — fire after successful commit.
	if s.onDrawdown != nil {
		s.onDrawdown(req.LimitKey, shortfall > 0)
	}
	if s.onShortfall != nil && sfRecord != nil {
		s.onShortfall(req.LimitKey, sfRecord.Status)
	}

	// Recompute (advisory).
	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseAddonChange); err != nil {
		s.logger().Warn("record drawdown: recompute failed",
			map[string]interface{}{"tenant_id": req.TenantID, "error": err.Error()})
	}
	return drawdown, nil
}

// buildShortfall applies ENT-D11/D12 using the configurable thresholds from
// ShortfallConfig (env-var driven per LLD §12 and ENT-D12 resolution).
//
// Escalation (QAS-7): a shortfall is ALWAYS escalated — and therefore never
// auto-accepted — when it meets either condition:
//   - shortfallTokens >= cfg.HighValueTokens  (reason: "high_value")
//   - recentCount     >= cfg.RepeatCount      (reason: "repeated")
//
// Auto-accept (QAS-5): when NOT escalated AND both tolerance conditions hold:
//   - shortfallTokens <= cfg.ToleranceTokens
//   - shortfallPct    <= cfg.TolerancePct
func (s *AddonService) buildShortfall(ctx context.Context, drawdownID, tenantID uuid.UUID, limitKey domain.LimitKey, requested, drawn, shortfallTokens int64) *domain.QuotaAddonShortfall {
	now := time.Now().UTC()
	sf := &domain.QuotaAddonShortfall{
		ID:         uuid.New(),
		TenantID:   tenantID,
		DrawdownID: drawdownID,
		LimitKey:   limitKey,
		Requested:  requested,
		Available:  drawn,
		Shortfall:  shortfallTokens,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	cfg := s.sfCfg
	// Count recent shortfalls using the configured window (months → days approximation).
	windowDays := cfg.RepeatWindowMonths * 30
	recentCount, _ := s.shortfalls.CountRecentForTenant(ctx, tenantID, windowDays)

	shortfallPct := float64(shortfallTokens) / float64(requested) * 100

	// ENT-D12 / QAS-7: escalation check — overrides tolerance.
	escalated := shortfallTokens >= cfg.HighValueTokens || recentCount >= cfg.RepeatCount

	if !escalated && shortfallTokens <= cfg.ToleranceTokens && shortfallPct <= cfg.TolerancePct {
		// ENT-D11 / QAS-5: auto-accept as commercial loss.
		disp := domain.DispositionApprove
		sf.Status = domain.ShortfallStatusAutoAccepted
		sf.Disposition = &disp
		s.logger().Info("shortfall auto-accepted (ENT-D11)",
			map[string]interface{}{
				"tenant_id":        tenantID,
				"shortfall_tokens": shortfallTokens,
				"pct":              shortfallPct,
				"tolerance_tokens": cfg.ToleranceTokens,
				"tolerance_pct":    cfg.TolerancePct,
			})
	} else {
		// ENT-D12 / QAS-7: escalate for operator review.
		sf.Status = domain.ShortfallStatusPendingApproval
		s.logger().Warn("shortfall escalated for review (ENT-D12)",
			map[string]interface{}{
				"tenant_id":        tenantID,
				"shortfall_tokens": shortfallTokens,
				"pct":              shortfallPct,
				"recent_count":     recentCount,
				"escalated":        escalated,
				"high_value":       shortfallTokens >= cfg.HighValueTokens,
				"repeated":         recentCount >= cfg.RepeatCount,
			})
	}

	return sf
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
	if sf.Status == domain.ShortfallStatusApproved ||
		sf.Status == domain.ShortfallStatusRejected ||
		sf.Status == domain.ShortfallStatusAutoAccepted {
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

	// Trigger recompute on approval so the quota ceiling and effective set are
	// refreshed and TenantEntitlementsChanged is emitted to downstream services.
	if req.Disposition == domain.DispositionApprove {
		if _, err := s.effectSvc.Recompute(ctx, sf.TenantID, domain.CauseShortfallResolution); err != nil {
			s.logger().Warn("shortfall resolve: recompute failed",
				map[string]any{"shortfall_id": req.ShortfallID, "tenant_id": sf.TenantID, "error": err.Error()})
		}
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
		forfeited := a.RemainingTokens
		a.Status = domain.AddonStatusExpired
		a.RemainingTokens = 0
		a.RecordVersion++
		a.UpdatedAt = now
		txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
			if err := s.addons.Update(txCtx, a); err != nil {
				return err
			}
			recordAudit(txCtx, s.log, port.AuditEntry{
				TenantID:   &a.TenantID,
				EntryType:  "billing.token_addon.expired",
				Actor:      port.AuditActor{Type: "system", ID: "commercial-entitlements"},
				Target:     &port.AuditTarget{Type: "quota_addon", ID: a.ID.String()},
				OccurredAt: now,
				Metadata: map[string]any{
					"limit_key":        string(a.LimitKey),
					"forfeited_tokens": forfeited,
				},
				IdempotencyKey: addonAuditKey(a.ID, a.RecordVersion, a.UpdatedAt),
			})
			return nil
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

// ForfeitAddonsAtPurge forfeits every active pack of a purged tenant (E-9).
// The balance is forfeited without refund: at offboarding (CD-55) and for a
// trial that expired without converting (HLD v0.69 P-5a), which reaches
// Entitlements as the same TenantMembershipsPurged. Rows are kept (status
// forfeited, remaining 0) because the drawdown and refund ledgers reference
// them. Runs in the caller's transaction when one is in ctx (inbox claim).
func (s *AddonService) ForfeitAddonsAtPurge(ctx context.Context, tenantID uuid.UUID) (int, error) {
	now := time.Now().UTC()
	count := 0
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		count = 0
		packs, err := s.addons.ListByTenant(txCtx, tenantID, nil)
		if err != nil {
			return fmt.Errorf("list addons: %w", err)
		}
		// Lock rows in drawdown FIFO order (expires_at, created_at) so a purge
		// racing a multi-pack drawdown cannot deadlock.
		sortAddonsFIFO(packs)
		for i := range packs {
			a := packs[i]
			if a.Status != domain.AddonStatusActive {
				continue
			}
			forfeited := a.RemainingTokens
			a.Status = domain.AddonStatusForfeited
			a.RemainingTokens = 0
			a.RecordVersion++
			a.UpdatedAt = now
			if err := s.addons.Update(txCtx, &a); err != nil {
				return fmt.Errorf("forfeit addon %s: %w", a.ID, err)
			}
			recordAudit(txCtx, s.log, port.AuditEntry{
				TenantID:   &a.TenantID,
				EntryType:  "billing.token_addon.revoked",
				Actor:      port.AuditActor{Type: "system", ID: "commercial-entitlements"},
				Target:     &port.AuditTarget{Type: "quota_addon", ID: a.ID.String()},
				OccurredAt: now,
				Metadata: map[string]any{
					"limit_key":        string(a.LimitKey),
					"forfeited_tokens": forfeited,
					"reason":           "tenant_purged",
				},
				IdempotencyKey: addonAuditKey(a.ID, a.RecordVersion, a.UpdatedAt),
			})
			count++
		}
		return nil
	})
	if txErr != nil {
		return 0, txErr
	}
	return count, nil
}

// ApplyBillingPurchase creates a new quota addon pack from a confirmed Billing
// purchase event (E-1 / BL-OQ-4). Idempotent on BillingRef; expiry is
// PurchasedAt + 12 months.
func (s *AddonService) ApplyBillingPurchase(ctx context.Context, req port.ApplyBillingPurchaseRequest) error {
	if req.Delta <= 0 {
		return domain.NewError(domain.ErrInvalidValue, "delta must be positive for a purchase")
	}
	if req.BillingRef == "" {
		return domain.NewError(domain.ErrInvalidValue, "billing_ref is required")
	}

	limitKey, ok := domain.BillingResourceToLimitKey(req.Resource)
	if !ok {
		return domain.NewError(domain.ErrInvalidLimitKey, "unknown billing resource: "+req.Resource)
	}

	expiresAt := req.PurchasedAt.UTC().AddDate(1, 0, 0)
	now := time.Now().UTC()
	a := &domain.QuotaAddon{
		ID:              uuid.New(),
		TenantID:        req.TenantID,
		LimitKey:        limitKey,
		TotalTokens:     req.Delta,
		RemainingTokens: req.Delta,
		GrantedBy:       uuid.Nil, // system actor — Billing confirmed purchase
		Reason:          "billing_purchase",
		BillingRef:      &req.BillingRef,
		Status:          domain.AddonStatusActive,
		ExpiresAt:       &expiresAt,
		RecordVersion:   1,
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		existing, err := s.addons.GetByBillingRef(txCtx, req.TenantID, req.BillingRef)
		if err != nil && !errors.Is(err, domain.ErrAddonNotFound) {
			return err
		}
		if existing != nil {
			s.logger().Info("billing purchase: idempotent replay, addon already exists",
				map[string]interface{}{"billing_ref": req.BillingRef, "addon_id": existing.ID})
			return nil
		}
		err = s.addons.Insert(txCtx, a)
		if !errors.Is(err, domain.ErrBillingRefAlreadyClaimed) {
			return err
		}
		// Lost a race with a concurrent grant of the same billing_ref for
		// this tenant: that is a replay, not a conflict.
		again, gerr := s.addons.GetByBillingRef(txCtx, req.TenantID, req.BillingRef)
		switch {
		case gerr == nil && again != nil:
			return nil
		case gerr != nil && !errors.Is(gerr, domain.ErrAddonNotFound):
			// A real read error is not a conflict: retry, don't raise the
			// billing_ref conflict alert.
			return gerr
		}
		return err
	})
	if txErr != nil {
		// ErrBillingRefAlreadyClaimed is returned to the caller (B-8): the
		// billing_ref is held by another tenant's pack, a duplicate or
		// conflicting paid grant. The Billing consumer counts and alerts on
		// it and acks the event; nothing is granted.
		return txErr
	}

	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseAddonChange); err != nil {
		return fmt.Errorf("billing purchase: recompute failed, event will retry: %w", err)
	}
	return nil
}

// ApplyBillingRefund reduces unused tokens on an addon pack in response to a
// Billing refund event (C-5). It removes at most |delta| unused tokens, never
// below zero and never consuming already-drawn tokens (CONC-1 optimistic lock).
//
// Idempotent on billing_ref (Billing's refund_id) via the quota_addon_refunds
// ledger (F-3, BL-OQ-20 W-5); processOnce only dedups redeliveries within the
// processed_events TTL. The refund row is claimed in the same transaction as
// the pack update, before it:
//   - claimed: the reduction is applied and recorded (0 when nothing was
//     unused, so a later delivery still finds the row);
//   - already recorded for this tenant: idempotent replay — the pack is not
//     touched (the follow-up recompute is a no-op, ENT-INV-6);
//   - recorded for another tenant, or for a different purchase of this
//     tenant: ErrBillingRefAlreadyClaimed (Details["conflict"] says which) is
//     returned; nothing changes; the Billing consumer counts it, alerts and acks.
//
// A refund that arrives before its purchase returns ErrAddonNotFound and
// writes no row, so it is retried.
func (s *AddonService) ApplyBillingRefund(ctx context.Context, req port.ApplyBillingRefundRequest) error {
	if req.Delta >= 0 {
		return domain.NewError(domain.ErrInvalidValue, "delta must be negative for a refund")
	}
	if req.BillingRef == uuid.Nil {
		return domain.NewError(domain.ErrInvalidValue, "billing_ref (refund_id) is required")
	}
	if s.refunds == nil {
		// Fail closed: without the ledger a redelivery after the
		// processed_events prune would apply the refund twice.
		return fmt.Errorf("billing refund: refund ledger not wired (WithRefundRepository)")
	}

	// Read and write inside the same transaction to close the CONC-1
	// read-modify-write race between ApplyBillingRefund and concurrent RecordDrawdown.
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		a, err := s.addons.GetByBillingRef(txCtx, req.TenantID, req.PurchaseRef.String())
		if err != nil {
			if errors.Is(err, domain.ErrAddonNotFound) {
				return domain.NewError(domain.ErrAddonNotFound, "addon not found for purchase_ref")
			}
			return err
		}

		// reduction = min(|delta|, remaining_tokens) — never reduces consumed tokens.
		reduction := -req.Delta
		if reduction > a.RemainingTokens {
			reduction = a.RemainingTokens
		}

		now := time.Now().UTC()
		claimed, err := s.refunds.Claim(txCtx, &domain.QuotaAddonRefund{
			BillingRef:    req.BillingRef,
			TenantID:      req.TenantID,
			AddonID:       a.ID,
			PurchaseRef:   req.PurchaseRef,
			TokensRemoved: reduction,
			AppliedAt:     now,
		})
		if err != nil {
			return err
		}
		if !claimed {
			prior, err := s.refunds.GetForTenant(txCtx, req.TenantID, req.BillingRef)
			if err != nil {
				return err
			}
			if prior == nil {
				return domain.NewError(domain.ErrBillingRefAlreadyClaimed, "refund billing_ref already recorded for another tenant").
					WithDetails(map[string]any{"conflict": domain.RefundConflictOtherTenant})
			}
			if prior.PurchaseRef != req.PurchaseRef {
				// Same tenant, but Billing reused the refund_id for a different
				// purchase: not a replay. Apply nothing; surface it.
				return domain.NewError(domain.ErrBillingRefAlreadyClaimed, "refund billing_ref already recorded for a different purchase").
					WithDetails(map[string]any{"conflict": domain.RefundConflictOtherPurchase, "recorded_purchase_ref": prior.PurchaseRef.String()})
			}
			s.logger().Info("billing refund: idempotent replay, refund already applied",
				map[string]interface{}{"billing_ref": req.BillingRef, "tenant_id": req.TenantID, "addon_id": a.ID})
			return nil
		}

		if reduction == 0 {
			s.logger().Info("billing refund: no unused tokens to remove",
				map[string]interface{}{"addon_id": a.ID, "tenant_id": req.TenantID, "billing_ref": req.BillingRef})
			return nil
		}

		// ENT-D16: total_tokens is the original purchased quantity and never
		// changes; the refund lowers the effective balance and is recorded in
		// the ledger (claimed above, so the sum below includes it).
		a.RemainingTokens -= reduction
		if a.RemainingTokens == 0 && a.Status == domain.AddonStatusActive {
			refunded, err := s.refunds.SumByAddon(txCtx, req.TenantID, []uuid.UUID{a.ID})
			if err != nil {
				return err
			}
			// ENT-D16: status records the disposition of the purchased
			// quantity, not the operation that removed the last tokens.
			// refunded only when cumulative refunds equal total_tokens
			// (refunds are capped at the remaining balance, so they can never
			// exceed it; >= is equality); any mix of consumption and refunds
			// that empties the pack is consumed.
			if refunded[a.ID] >= a.TotalTokens {
				a.Status = domain.AddonStatusRefunded
			} else {
				a.Status = domain.AddonStatusConsumed
			}
		}
		a.RecordVersion++
		a.UpdatedAt = now

		return s.addons.Update(txCtx, a)
	})
	if txErr != nil {
		return txErr
	}

	// Also runs on a replay: if an earlier delivery committed the refund but
	// failed this recompute (possible outside an inbox transaction), the
	// replay is what brings the snapshot up to date. When the snapshot is
	// already current this writes and emits nothing (ENT-INV-6).
	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseAddonChange); err != nil {
		return fmt.Errorf("billing refund: recompute failed, event will retry: %w", err)
	}
	return nil
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

// sortAddonsFIFO orders packs as the drawdown FIFO query does
// (expires_at ASC NULLS LAST, created_at ASC), with id as a final tie-break.
func sortAddonsFIFO(addons []domain.QuotaAddon) {
	sort.Slice(addons, func(i, j int) bool {
		ai, aj := addons[i].ExpiresAt, addons[j].ExpiresAt
		switch {
		case ai != nil && aj != nil && !ai.Equal(*aj):
			return ai.Before(*aj)
		case ai == nil && aj != nil:
			return false
		case ai != nil && aj == nil:
			return true
		}
		if !addons[i].CreatedAt.Equal(addons[j].CreatedAt) {
			return addons[i].CreatedAt.Before(addons[j].CreatedAt)
		}
		return addons[i].ID.String() < addons[j].ID.String()
	})
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// addonAuditKey returns a stable v5 UUID idempotency key for a quota_addons row
// following the LLD pattern: uuidv5("quota_addons:<id>:<version>:<updated_at>").
func addonAuditKey(addonID uuid.UUID, recordVersion int64, updatedAt time.Time) string {
	seed := fmt.Sprintf("quota_addons:%s:%d:%s", addonID, recordVersion, updatedAt.Format(time.RFC3339Nano))
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(seed)).String()
}

// recordAudit writes an AL-5 audit entry inside the current transaction.
// If no AuditRecorder is in ctx the call is a no-op — audit failures never
// block the business write (AUDIT-CALLER-1).
func recordAudit(ctx context.Context, log port.Logger, entry port.AuditEntry) {
	rec, ok := port.AuditRecorderFromContext(ctx)
	if !ok {
		if log != nil {
			log.Warn("audit: recorder not in context, skipping",
				map[string]interface{}{"entry_type": entry.EntryType})
		}
		return
	}
	if err := rec.Record(ctx, entry); err != nil {
		if log != nil {
			log.Warn("audit: record failed",
				map[string]interface{}{"entry_type": entry.EntryType, "error": err.Error()})
		}
	}
}
