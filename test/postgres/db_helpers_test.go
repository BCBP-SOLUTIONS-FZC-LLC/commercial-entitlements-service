//go:build integration

package postgres_test

import (
	"context"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/test/dbseed"
)

// dbseedNew is an alias used by tests to open a seed pool, allowing the
// import of dbseed without an extra import block in every test file.
func dbseedNew(ctx context.Context, dsn string) (*dbseed.Pool, error) {
	return dbseed.New(ctx, dsn)
}
