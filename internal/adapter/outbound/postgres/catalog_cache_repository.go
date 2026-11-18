package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// CatalogCacheRepository caches plan definitions fetched from the IAM catalog service.
type CatalogCacheRepository struct {
	pool *pgcommon.Pool
}

// NewCatalogCacheRepository returns a new CatalogCacheRepository.
func NewCatalogCacheRepository(pool *pgcommon.Pool) *CatalogCacheRepository {
	return &CatalogCacheRepository{pool: pool}
}

var _ port.CatalogCacheRepository = (*CatalogCacheRepository)(nil)

// GetPlan returns the cached plan definition or ErrPlanNotFound.
func (r *CatalogCacheRepository) GetPlan(ctx context.Context, planCode string) (*domain.Plan, error) {
	var defJSON []byte
	var plan domain.Plan

	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT plan_code, display_name, record_version, definition
			FROM plan_catalog_cache WHERE plan_code=$1`, planCode).
			Scan(&plan.Code, &plan.DisplayName, &plan.RecordVersion, &defJSON)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrPlanNotFound
		}
		return nil, wrapConnErr(err)
	}
	if err := json.Unmarshal(defJSON, &plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

// SetPlan upserts a plan definition into the cache table.
func (r *CatalogCacheRepository) SetPlan(ctx context.Context, plan *domain.Plan) error {
	defJSON, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO plan_catalog_cache (plan_code, display_name, record_version, definition, cached_at)
			VALUES ($1,$2,$3,$4,now())
			ON CONFLICT (plan_code) DO UPDATE SET
				display_name   = EXCLUDED.display_name,
				record_version = EXCLUDED.record_version,
				definition     = EXCLUDED.definition,
				cached_at      = EXCLUDED.cached_at`,
			plan.Code, plan.DisplayName, plan.RecordVersion, defJSON)
		return err
	})
}

// InvalidatePlan removes the plan from the cache (evict on catalog change).
func (r *CatalogCacheRepository) InvalidatePlan(ctx context.Context, planCode string) error {
	return withPool(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM plan_catalog_cache WHERE plan_code=$1`, planCode)
		return err
	})
}

// ListPlanCodes returns all plan codes currently in cache.
func (r *CatalogCacheRepository) ListPlanCodes(ctx context.Context) ([]string, error) {
	var out []string
	err := withPool(ctx, r.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT plan_code FROM plan_catalog_cache ORDER BY plan_code`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var code string
			if err := rows.Scan(&code); err != nil {
				return err
			}
			out = append(out, code)
		}
		return rows.Err()
	})
	return out, err
}
