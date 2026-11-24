package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// OverrideService manages operator-level manual entitlement overrides.
type OverrideService struct {
	overrides port.OverrideRepository
	effectSvc *EffectiveSetService
	txRunner  port.TxRunner
	log       port.Logger
}

// Ensure OverrideService implements port.OverrideService at compile time.
var _ port.OverrideService = (*OverrideService)(nil)

// NewOverrideService wires up the OverrideService.
func NewOverrideService(
	overrides port.OverrideRepository,
	effectSvc *EffectiveSetService,
	txRunner port.TxRunner,
) *OverrideService {
	return &OverrideService{
		overrides: overrides,
		effectSvc: effectSvc,
		txRunner:  txRunner,
	}
}

// WithLogger attaches a logger.
func (s *OverrideService) WithLogger(l port.Logger) *OverrideService {
	s.log = l
	return s
}

func (s *OverrideService) logger() port.Logger {
	if s.log != nil {
		return s.log
	}
	return port.NopLogger{}
}

// SetOverride creates or replaces an operator override for a tenant+key.
// Triggers a recomputation after the write.
func (s *OverrideService) SetOverride(ctx context.Context, req port.SetOverrideRequest) (*domain.Override, error) {
	if err := validateLimitKey(req.LimitKey); err != nil {
		return nil, err
	}
	if req.Reason == "" {
		return nil, domain.NewError(domain.ErrReasonRequired, "reason is required when setting an override")
	}
	if req.ExpiresAt != nil && req.ExpiresAt.Before(time.Now().UTC()) {
		return nil, domain.NewError(domain.ErrExpiresAtInPast, "expires_at must be in the future")
	}

	// Fetch existing to record history delta.
	existing, _ := s.overrides.Get(ctx, req.TenantID, req.LimitKey)

	now := time.Now().UTC()
	ov := &domain.Override{
		TenantID:  req.TenantID,
		LimitKey:  req.LimitKey,
		Value:     req.Value,
		Reason:    req.Reason,
		SetBy:     req.SetBy,
		ExpiresAt: req.ExpiresAt,
		UpdatedAt: now,
	}
	if existing == nil {
		ov.CreatedAt = now
		ov.RecordVersion = 1
	} else {
		ov.CreatedAt = existing.CreatedAt
		ov.RecordVersion = existing.RecordVersion + 1
	}

	var oldVal *int64
	if existing != nil {
		oldVal = existing.Value
	}
	hist := &domain.OverrideHistory{
		TenantID:  req.TenantID,
		LimitKey:  req.LimitKey,
		OldValue:  oldVal,
		NewValue:  req.Value,
		Action:    domain.ActionSet,
		Reason:    req.Reason,
		ActorID:   req.SetBy,
		CreatedAt: now,
	}

	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.overrides.Upsert(txCtx, ov); err != nil {
			return fmt.Errorf("upsert override: %w", err)
		}
		return s.overrides.InsertHistory(txCtx, hist)
	})
	if txErr != nil {
		return nil, txErr
	}

	// Trigger recompute (advisory; non-fatal if it fails).
	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseOverrideChange); err != nil {
		s.logger().Warn("override set: recompute failed",
			map[string]interface{}{"tenant_id": req.TenantID, "error": err.Error()})
	}

	return ov, nil
}

// RemoveOverride deletes an existing override and triggers a recomputation.
func (s *OverrideService) RemoveOverride(ctx context.Context, req port.RemoveOverrideRequest) error {
	if err := validateLimitKey(req.LimitKey); err != nil {
		return err
	}
	existing, err := s.overrides.Get(ctx, req.TenantID, req.LimitKey)
	if err != nil {
		if errors.Is(err, domain.ErrOverrideNotFound) {
			return domain.NewError(domain.ErrOverrideNotFound, "override not found")
		}
		return fmt.Errorf("get override: %w", err)
	}

	now := time.Now().UTC()
	hist := &domain.OverrideHistory{
		TenantID:  req.TenantID,
		LimitKey:  req.LimitKey,
		OldValue:  existing.Value,
		NewValue:  nil,
		Action:    domain.ActionRemoved,
		Reason:    req.Reason,
		ActorID:   req.RemovedBy,
		CreatedAt: now,
	}

	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.overrides.Delete(txCtx, req.TenantID, req.LimitKey); err != nil {
			return fmt.Errorf("delete override: %w", err)
		}
		return s.overrides.InsertHistory(txCtx, hist)
	})
	if txErr != nil {
		return txErr
	}

	// Recompute (advisory).
	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseOverrideChange); err != nil {
		s.logger().Warn("override remove: recompute failed",
			map[string]interface{}{"tenant_id": req.TenantID, "error": err.Error()})
	}
	return nil
}

// GetOverride returns a single override for a tenant+key.
func (s *OverrideService) GetOverride(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) (*domain.Override, error) {
	ov, err := s.overrides.Get(ctx, tenantID, key)
	if err != nil {
		if errors.Is(err, domain.ErrOverrideNotFound) {
			return nil, domain.NewError(domain.ErrOverrideNotFound, "override not found")
		}
		return nil, fmt.Errorf("get override: %w", err)
	}
	return ov, nil
}

// ListOverrides returns all overrides for a tenant.
func (s *OverrideService) ListOverrides(ctx context.Context, tenantID uuid.UUID) ([]domain.Override, error) {
	return s.overrides.ListByTenant(ctx, tenantID)
}

// ListOverrideHistory returns the full audit trail for a tenant+key.
func (s *OverrideService) ListOverrideHistory(ctx context.Context, tenantID uuid.UUID, key domain.LimitKey) ([]domain.OverrideHistory, error) {
	return s.overrides.ListHistory(ctx, tenantID, key)
}

// ExpireOverrides sweeps all overrides whose ExpiresAt has passed.
func (s *OverrideService) ExpireOverrides(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	expired, err := s.overrides.ListExpired(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("list expired overrides: %w", err)
	}

	count := 0
	for i := range expired {
		ov := &expired[i]
		hist := &domain.OverrideHistory{
			TenantID:  ov.TenantID,
			LimitKey:  ov.LimitKey,
			OldValue:  ov.Value,
			NewValue:  nil,
			Action:    domain.ActionExpired,
			Reason:    "override expired",
			ActorID:   ov.SetBy,
			CreatedAt: now,
		}
		txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
			if err := s.overrides.Delete(txCtx, ov.TenantID, ov.LimitKey); err != nil {
				return err
			}
			return s.overrides.InsertHistory(txCtx, hist)
		})
		if txErr != nil {
			s.logger().Warn("expire override failed",
				map[string]interface{}{"tenant_id": ov.TenantID, "key": ov.LimitKey, "error": txErr.Error()})
			continue
		}
		if _, err := s.effectSvc.Recompute(ctx, ov.TenantID, domain.CauseOverrideChange); err != nil {
			s.logger().Warn("expire override recompute failed",
				map[string]interface{}{"tenant_id": ov.TenantID, "error": err.Error()})
		}
		count++
	}
	return count, nil
}
