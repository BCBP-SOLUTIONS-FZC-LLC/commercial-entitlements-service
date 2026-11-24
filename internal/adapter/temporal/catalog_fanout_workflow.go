package temporal

import (
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	CatalogFanoutWorkflowType = "CatalogFanoutWorkflow"
	ExpiryFanoutWorkflowType  = "ExpiryFanoutWorkflow"
	TaskQueue                 = "commercial-entitlements"
)

// CatalogFanoutInput carries the plan code for which all tenant entitlements
// must be recomputed.
type CatalogFanoutInput struct {
	PlanCode      string
	RecordVersion int64
}

// CatalogFanoutWorkflow fetches all tenant IDs on the given plan and
// recomputes each in parallel, fan-out style.
func CatalogFanoutWorkflow(ctx workflow.Context, input CatalogFanoutInput) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	var tenantIDs []string
	if err := workflow.ExecuteActivity(ctx, (*Activities).ListTenantsByPlanActivity, input.PlanCode).Get(ctx, &tenantIDs); err != nil {
		return err
	}

	futures := make([]workflow.Future, 0, len(tenantIDs))
	for _, id := range tenantIDs {
		f := workflow.ExecuteActivity(ctx, (*Activities).RecomputeEntitlementsActivity, id, CatalogFanoutWorkflowType)
		futures = append(futures, f)
	}

	var lastErr error
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			workflow.GetLogger(ctx).Error("recompute activity failed", "error", err)
			lastErr = err
		}
	}
	return lastErr
}

// ExpiryFanoutWorkflow expires overrides and addons, then recomputes affected tenants.
func ExpiryFanoutWorkflow(ctx workflow.Context, _ struct{}) error {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 3},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	if err := workflow.ExecuteActivity(ctx, (*Activities).ExpireOverridesActivity).Get(ctx, nil); err != nil {
		return err
	}
	if err := workflow.ExecuteActivity(ctx, (*Activities).ExpireAddonsActivity).Get(ctx, nil); err != nil {
		return err
	}
	return nil
}
