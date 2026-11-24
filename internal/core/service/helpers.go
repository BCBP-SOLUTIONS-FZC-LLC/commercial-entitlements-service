package service

import (
	"fmt"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
)

// validateLimitKey returns an error if key is not in the known set.
func validateLimitKey(key domain.LimitKey) error {
	if !domain.KnownLimitKeys[key] {
		return domain.NewError(domain.ErrInvalidLimitKey, fmt.Sprintf("unknown limit key %q", key))
	}
	return nil
}
