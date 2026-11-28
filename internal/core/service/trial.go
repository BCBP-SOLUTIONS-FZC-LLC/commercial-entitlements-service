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

// TrialService manages trial profile assignment and trial-specific limit overrides.
type TrialService struct {
	trials    port.TrialRepository
	effectSvc *EffectiveSetService
	txRunner  port.TxRunner
	log       port.Logger
}

// NewTrialService wires up the TrialService.
func NewTrialService(
	trials port.TrialRepository,
	effectSvc *EffectiveSetService,
	txRunner port.TxRunner,
) *TrialService {
	return &TrialService{
		trials:    trials,
		effectSvc: effectSvc,
		txRunner:  txRunner,
	}
}

// WithLogger attaches a logger.
func (s *TrialService) WithLogger(l port.Logger) *TrialService {
	s.log = l
	return s
}

func (s *TrialService) logger() port.Logger {
	if s.log != nil {
		return s.log
	}
	return port.NopLogger{}
}

// AssignTrialProfile sets or replaces a tenant's trial profile (ENT-O3).
func (s *TrialService) AssignTrialProfile(ctx context.Context, req port.AssignTrialProfileRequest) (*domain.TenantTrialAssignment, error) {
	if !domain.ValidTrialProfiles[req.Profile] {
		return nil, domain.NewError(domain.ErrInvalidProfile, fmt.Sprintf("unknown trial profile %q", req.Profile))
	}
	if req.EndsAt.IsZero() || req.EndsAt.Before(time.Now().UTC()) {
		return nil, domain.NewError(domain.ErrTrialEndsAtInPast, "trial ends_at must be in the future")
	}
	if req.Reason == "" {
		return nil, domain.NewError(domain.ErrReasonRequired, "reason is required when assigning a trial profile")
	}

	now := time.Now().UTC()
	existing, err := s.trials.GetAssignment(ctx, req.TenantID)
	if err != nil && !errors.Is(err, domain.ErrTrialAssignmentNotFound) {
		// Fail rather than audit an overwrite as a first assignment.
		return nil, fmt.Errorf("get trial assignment: %w", err)
	}
	var version int64 = 1
	if existing != nil {
		version = existing.RecordVersion + 1
	}

	assignment := &domain.TenantTrialAssignment{
		TenantID:      req.TenantID,
		PlanCode:      req.PlanCode,
		Profile:       req.Profile,
		StartsAt:      req.StartsAt,
		EndsAt:        req.EndsAt,
		SetBy:         req.SetBy,
		RecordVersion: version,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	var final *domain.TenantTrialAssignment
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.trials.UpsertAssignment(txCtx, assignment); err != nil {
			return fmt.Errorf("upsert trial assignment: %w", err)
		}
		meta := map[string]any{
			"plan_code": req.PlanCode,
			"profile":   string(req.Profile),
			"starts_at": req.StartsAt,
			"ends_at":   req.EndsAt,
			"reason":    req.Reason,
		}
		if existing != nil {
			meta["previous_profile"] = string(existing.Profile)
		}
		recordAudit(txCtx, s.log, port.AuditEntry{
			TenantID:       &req.TenantID,
			EntryType:      "config.trial_profile.set",
			Actor:          port.AuditActor{Type: "platform_operator", ID: req.SetBy.String()},
			Target:         &port.AuditTarget{Type: "tenant_trial_assignment", ID: req.TenantID.String()},
			OccurredAt:     now,
			Metadata:       meta,
			IdempotencyKey: trialAssignmentAuditKey(req.TenantID, version, now),
		})
		final = assignment
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}

	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseTrialChange); err != nil {
		s.logger().Warn("assign trial profile: recompute failed",
			map[string]interface{}{"tenant_id": req.TenantID, "error": err.Error()})
	}
	return final, nil
}

// GetTrialAssignment returns the current trial assignment for a tenant.
func (s *TrialService) GetTrialAssignment(ctx context.Context, tenantID uuid.UUID) (*domain.TenantTrialAssignment, error) {
	ta, err := s.trials.GetAssignment(ctx, tenantID)
	if err != nil {
		if errors.Is(err, domain.ErrTrialAssignmentNotFound) {
			return nil, domain.NewError(domain.ErrTrialAssignmentNotFound, "no trial assignment for tenant")
		}
		return nil, fmt.Errorf("get trial assignment: %w", err)
	}
	return ta, nil
}

// SetTrialOverride creates or replaces a trial-scoped limit override (ENT-O4).
func (s *TrialService) SetTrialOverride(ctx context.Context, req port.SetTrialOverrideRequest) (*domain.TrialEntitlementOverride, error) {
	if err := validateLimitKey(req.LimitKey); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	ov := &domain.TrialEntitlementOverride{
		TenantID:  req.TenantID,
		LimitKey:  req.LimitKey,
		Value:     req.Value,
		SetBy:     req.SetBy,
		CreatedAt: now,
		UpdatedAt: now,
	}

	var final *domain.TrialEntitlementOverride
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.trials.UpsertOverride(txCtx, ov); err != nil {
			return fmt.Errorf("upsert trial override: %w", err)
		}
		final = ov
		return nil
	})
	if txErr != nil {
		return nil, txErr
	}

	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseTrialChange); err != nil {
		s.logger().Warn("set trial override: recompute failed",
			map[string]interface{}{"tenant_id": req.TenantID, "error": err.Error()})
	}
	return final, nil
}

// RemoveTrialOverride deletes a trial-scoped limit override.
func (s *TrialService) RemoveTrialOverride(ctx context.Context, req port.RemoveTrialOverrideRequest) error {
	if err := validateLimitKey(req.LimitKey); err != nil {
		return err
	}

	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		return s.trials.DeleteOverride(txCtx, req.TenantID, req.LimitKey)
	})
	if txErr != nil {
		return txErr
	}

	if _, err := s.effectSvc.Recompute(ctx, req.TenantID, domain.CauseTrialChange); err != nil {
		s.logger().Warn("remove trial override: recompute failed",
			map[string]interface{}{"tenant_id": req.TenantID, "error": err.Error()})
	}
	return nil
}

// ListTrialOverrides returns all trial overrides for a tenant.
func (s *TrialService) ListTrialOverrides(ctx context.Context, tenantID uuid.UUID) ([]domain.TrialEntitlementOverride, error) {
	return s.trials.ListOverrides(ctx, tenantID)
}

// PurgeTrialForTenant removes all trial data (called on TenantConverted / TenantOffboarded).
func (s *TrialService) PurgeTrialForTenant(ctx context.Context, tenantID uuid.UUID) error {
	txErr := s.txRunner.RunInTx(ctx, func(txCtx context.Context) error {
		if err := s.trials.DeleteAllOverrides(txCtx, tenantID); err != nil {
			return fmt.Errorf("delete trial overrides: %w", err)
		}
		if err := s.trials.DeleteAssignment(txCtx, tenantID); err != nil {
			return fmt.Errorf("delete trial assignment: %w", err)
		}
		return nil
	})
	if txErr != nil {
		return txErr
	}

	if _, err := s.effectSvc.Recompute(ctx, tenantID, domain.CauseTenantLifecycle); err != nil {
		s.logger().Warn("purge trial: recompute failed",
			map[string]interface{}{"tenant_id": tenantID, "error": err.Error()})
	}
	return nil
}

// trialAssignmentAuditKey returns a v5 UUID idempotency key for one ENT-O3
// request: uuidv5("tenant_trial_assignments:<tenant>:<version>:<request time>").
// Both inputs are fixed before RunInTx, so a retried transaction reuses the key;
// each new request gets a new one.
func trialAssignmentAuditKey(tenantID uuid.UUID, recordVersion int64, updatedAt time.Time) string {
	seed := fmt.Sprintf("tenant_trial_assignments:%s:%d:%s", tenantID, recordVersion, updatedAt.Format(time.RFC3339Nano))
	return uuid.NewSHA1(uuid.NameSpaceDNS, []byte(seed)).String()
}
