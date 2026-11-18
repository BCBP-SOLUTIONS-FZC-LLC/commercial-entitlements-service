package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ShortfallRepository persists quota addon shortfall records.
type ShortfallRepository struct {
	pool *pgcommon.Pool
}

// NewShortfallRepository returns a new ShortfallRepository.
func NewShortfallRepository(pool *pgcommon.Pool) *ShortfallRepository {
	return &ShortfallRepository{pool: pool}
}

var _ port.ShortfallRepository = (*ShortfallRepository)(nil)

// Insert creates a new shortfall record.
func (r *ShortfallRepository) Insert(ctx context.Context, s *domain.QuotaAddonShortfall) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO quota_addon_shortfalls
				(id, tenant_id, drawdown_id, limit_key, requested, available,
				 shortfall, status, disposition, disposition_by, disposition_ref,
				 disposition_note, created_at, updated_at, resolved_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)`,
			s.ID, s.TenantID, s.DrawdownID, string(s.LimitKey),
			s.Requested, s.Available, s.Shortfall, string(s.Status),
			s.Disposition, s.DispositionBy, s.DispositionRef,
			s.DispositionNote, s.CreatedAt, s.UpdatedAt, s.ResolvedAt,
		)
		return err
	})
}

// Update applies mutations to an existing shortfall.
func (r *ShortfallRepository) Update(ctx context.Context, s *domain.QuotaAddonShortfall) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			UPDATE quota_addon_shortfalls
			SET status          = $3, disposition = $4,
			    disposition_by  = $5, disposition_ref = $6,
			    disposition_note= $7, updated_at = $8, resolved_at = $9
			WHERE id=$1 AND tenant_id=$2`,
			s.ID, s.TenantID,
			string(s.Status), s.Disposition, s.DispositionBy,
			s.DispositionRef, s.DispositionNote, s.UpdatedAt, s.ResolvedAt,
		)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrOptimisticLockConflict
		}
		return nil
	})
}

// GetByDrawdownID retrieves the shortfall raised for a specific drawdown.
func (r *ShortfallRepository) GetByDrawdownID(ctx context.Context, drawdownID uuid.UUID) (*domain.QuotaAddonShortfall, error) {
	var s domain.QuotaAddonShortfall
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return scanShortfallRow(tx.QueryRow(ctx, shortfallSelect+` WHERE drawdown_id=$1`, drawdownID), &s)
	})
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// GetByID retrieves a shortfall by primary key.
func (r *ShortfallRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.QuotaAddonShortfall, error) {
	var s domain.QuotaAddonShortfall
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return scanShortfallRow(tx.QueryRow(ctx, shortfallSelect+` WHERE id=$1`, id), &s)
	})
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// ListByTenant lists shortfalls for a tenant, optionally filtered by status.
func (r *ShortfallRepository) ListByTenant(ctx context.Context, tenantID uuid.UUID, statusFilter []domain.ShortfallStatus) ([]domain.QuotaAddonShortfall, error) {
	var sb strings.Builder
	args := []interface{}{tenantID}
	sb.WriteString(shortfallSelect + ` WHERE tenant_id=$1`)
	if len(statusFilter) > 0 {
		placeholders := make([]string, len(statusFilter))
		for i, sf := range statusFilter {
			args = append(args, string(sf))
			placeholders[i] = fmt.Sprintf("$%d", len(args))
		}
		sb.WriteString(` AND status IN (`)
		sb.WriteString(strings.Join(placeholders, ","))
		sb.WriteString(`)`)
	}
	sb.WriteString(` ORDER BY created_at DESC`)

	var out []domain.QuotaAddonShortfall
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, sb.String(), args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s domain.QuotaAddonShortfall
			if err := scanShortfallRow(rows, &s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	})
	return out, err
}

// CountRecentForTenant returns shortfalls that are not auto-accepted in a
// rolling window (used by ENT-D12 escalation policy).
func (r *ShortfallRepository) CountRecentForTenant(ctx context.Context, tenantID uuid.UUID, windowDays int) (int, error) {
	var count int
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT COUNT(*)
			FROM quota_addon_shortfalls
			WHERE tenant_id=$1
			  AND status NOT IN ('auto_accepted')
			  AND created_at >= now() - ($2::int * INTERVAL '1 day')`,
			tenantID, windowDays).Scan(&count)
	})
	return count, err
}

const shortfallSelect = `
	SELECT id, tenant_id, drawdown_id, limit_key, requested, available,
	       shortfall, status, disposition, disposition_by, disposition_ref,
	       disposition_note, created_at, updated_at, resolved_at
	FROM quota_addon_shortfalls`

type shortfallScanner interface {
	Scan(dest ...any) error
}

func scanShortfallRow(row shortfallScanner, s *domain.QuotaAddonShortfall) error {
	var lk, status string
	var disp *string
	err := row.Scan(
		&s.ID, &s.TenantID, &s.DrawdownID, &lk,
		&s.Requested, &s.Available, &s.Shortfall, &status,
		&disp, &s.DispositionBy, &s.DispositionRef,
		&s.DispositionNote, &s.CreatedAt, &s.UpdatedAt, &s.ResolvedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrShortfallNotFound
		}
		return err
	}
	s.LimitKey = domain.LimitKey(lk)
	s.Status = domain.ShortfallStatus(status)
	if disp != nil {
		d := domain.ShortfallDisposition(*disp)
		s.Disposition = &d
	}
	return nil
}
