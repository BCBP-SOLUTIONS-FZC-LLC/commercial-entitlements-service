package temporal

import (
	"context"
	"fmt"

	"go.temporal.io/sdk/client"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// Starter implements port.TemporalStarter using the Temporal Go SDK client.
type Starter struct {
	client client.Client
}

var _ port.TemporalStarter = (*Starter)(nil)

// NewStarter creates a Starter wrapping the given Temporal client.
func NewStarter(c client.Client) *Starter {
	return &Starter{client: c}
}

// StartCatalogFanout triggers the CatalogFanoutWorkflow for the given plan code.
func (s *Starter) StartCatalogFanout(ctx context.Context, planCode string) error {
	opts := client.StartWorkflowOptions{
		// Workflow IDs are deterministic so duplicate starts are idempotent.
		ID:        fmt.Sprintf("catalog-fanout-%s", planCode),
		TaskQueue: TaskQueue,
	}
	_, err := s.client.ExecuteWorkflow(ctx, opts, CatalogFanoutWorkflow, CatalogFanoutInput{
		PlanCode: planCode,
	})
	return err
}

// StartExpiryFanout triggers the ExpiryFanoutWorkflow.
func (s *Starter) StartExpiryFanout(ctx context.Context) error {
	opts := client.StartWorkflowOptions{
		ID:        "expiry-fanout",
		TaskQueue: TaskQueue,
	}
	_, err := s.client.ExecuteWorkflow(ctx, opts, ExpiryFanoutWorkflow, struct{}{})
	return err
}
