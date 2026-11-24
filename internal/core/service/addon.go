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

// AddonService manages quota addon packs and drawdowns with full shortfall lifecycle.
type AddonService struct {
	addons     port.AddonRepository
	drawdowns  port.DrawdownRepository
	shortfalls port.ShortfallRepository
	effectSvc  *EffectiveSetService
	txRunner   port.TxRunner
	log        port.Logger

	// Observability hooks injected by the wiring layer so the core service
	// stays free of adapter imports.
	onDrawdown  func(key domain.LimitKey, hadShortfall bool)
	onShortfall func(key domain.LimitKey, status domain.ShortfallStatus)
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
		recordAddonAudit(txCtx, s.log, port.AuditEntry{
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

// RemoveAddon revokes an active addon pack (must be fully unused).
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
		if a.RemainingTokens < a.TotalTokens {
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
		recordAddonAudit(txCtx, s.log, port.AuditEntry{
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

// buildShortfall applies ENT-D11/D12: auto-accept within tolerance or raise for review.
func (s *AddonService) buildShortfall(ctx context.Context, drawdownID, tenantID uuid.UUID, limitKey domain.LimitKey, requested, drawn, shortfallTokens int64) (*domain.QuotaAddonShortfall, error) {
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
			recordAddonAudit(txCtx, s.log, port.AuditEntry{
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
		return s.addons.Insert(txCtx, a)
	})
	if txErr != nil {
		if errors.Is(txErr, domain.ErrBillingRefAlreadyClaimed) {
			s.logger().Error("billing purchase: billing_ref claimed by another tenant — dropping event",
				map[string]interface{}{"billing_ref": req.BillingRef, "tenant_id": req.TenantID})
			return nil
		}
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
// Idempotency is guaranteed by the caller via processOnce on the billing event ID.
func (s *AddonService) ApplyBillingRefund(ctx context.Context, req port.ApplyBillingRefundRequest) error {
	if req.Delta >= 0 {
		return domain.NewError(domain.ErrInvalidValue, "delta must be negative for a refund")
	}

	// Read and write inside the same transaction to close the CONC-1
	// read-modify-write race between ApplyBillingRefund and concurrent RecordDrawdown.
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		a, err := s.addons.GetByBillingRef(txCtx, req.TenantID, req.PurchaseRef.String())
		if err != nil {
			return domain.NewError(domain.ErrAddonNotFound, "addon not found for purchase_ref")
		}

		// reduction = min(|delta|, remaining_tokens) — never reduces consumed tokens.
		reduction := -req.Delta
		if reduction > a.RemainingTokens {
			reduction = a.RemainingTokens
		}
		if reduction == 0 {
			s.logger().Info("billing refund: no unused tokens to remove",
				map[string]interface{}{"addon_id": a.ID, "tenant_id": req.TenantID, "billing_ref": req.BillingRef})
			return nil
		}

		now := time.Now().UTC()
		a.TotalTokens -= reduction
		a.RemainingTokens -= reduction
		a.RecordVersion++
		a.UpdatedAt = now

		return s.addons.Update(txCtx, a)
	})
	if txErr != nil {
		return txErr
	}

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

// recordAddonAudit writes an AL-5 audit entry inside the current transaction.
// If no AuditRecorder is in ctx the call is a no-op — audit failures never
// block the business write (AUDIT-CALLER-1).
func recordAddonAudit(ctx context.Context, log port.Logger, entry port.AuditEntry) {
	rec, ok := port.AuditRecorderFromContext(ctx)
	if !ok {
		if log != nil {
			log.Warn("addon audit: recorder not in context, skipping",
				map[string]interface{}{"entry_type": entry.EntryType})
		}
		return
	}
	if err := rec.Record(ctx, entry); err != nil {
		if log != nil {
			log.Warn("addon audit: record failed",
				map[string]interface{}{"entry_type": entry.EntryType, "error": err.Error()})
		}
	}
}
