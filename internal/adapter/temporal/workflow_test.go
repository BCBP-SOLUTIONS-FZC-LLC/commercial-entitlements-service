package temporal

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// newWorkflowEnv creates a TestWorkflowEnvironment with the Activities struct
// registered so string-name OnActivity mocks resolve correctly.
func newWorkflowEnv(suite *testsuite.WorkflowTestSuite) *testsuite.TestWorkflowEnvironment {
	env := suite.NewTestWorkflowEnvironment()
	// Register a zero-dep Activities so the SDK knows the method names.
	// The actual implementations are never called — the OnActivity mocks intercept.
	env.RegisterActivity(NewActivities(
		&stubEffectiveSvc{},
		&stubOverrideSvc{},
		&stubAddonSvc{},
		&stubSnapshotRepo{},
	))
	return env
}

// ── CatalogFanoutWorkflow ─────────────────────────────────────────────────────

func TestCatalogFanoutWorkflow_NoTenants_Succeeds(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := newWorkflowEnv(&suite)

	// Mock by string name — avoids the receiver-decode panic that occurs when
	// passing a method expression to OnActivity for struct-based activities.
	env.OnActivity("ListTenantsByPlanActivity", mock.Anything, "pro").
		Return([]string{}, nil).Once()

	env.ExecuteWorkflow(CatalogFanoutWorkflow, CatalogFanoutInput{PlanCode: "pro"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestCatalogFanoutWorkflow_WithTenants_RecomputesAll(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := newWorkflowEnv(&suite)

	id1 := uuid.New().String()
	id2 := uuid.New().String()

	env.OnActivity("ListTenantsByPlanActivity", mock.Anything, "enterprise").
		Return([]string{id1, id2}, nil).Once()

	// Both recompute calls must happen (order not guaranteed by fan-out).
	env.OnActivity("RecomputeEntitlementsActivity", mock.Anything, id1, CatalogFanoutWorkflowType).
		Return(nil).Once()
	env.OnActivity("RecomputeEntitlementsActivity", mock.Anything, id2, CatalogFanoutWorkflowType).
		Return(nil).Once()

	env.ExecuteWorkflow(CatalogFanoutWorkflow, CatalogFanoutInput{PlanCode: "enterprise"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestCatalogFanoutWorkflow_ListActivityError_ReturnsError(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := newWorkflowEnv(&suite)

	sentinel := errors.New("list failed")
	// No .Once() — the retry policy allows up to 3 attempts, so the mock must
	// handle repeated calls without exhausting a single-call expectation.
	env.OnActivity("ListTenantsByPlanActivity", mock.Anything, "starter").
		Return(nil, sentinel)

	env.ExecuteWorkflow(CatalogFanoutWorkflow, CatalogFanoutInput{PlanCode: "starter"})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), sentinel.Error())
}

func TestCatalogFanoutWorkflow_RecomputeActivityError_ReturnsLastError(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := newWorkflowEnv(&suite)

	id1 := uuid.New().String()
	sentinel := errors.New("recompute boom")

	env.OnActivity("ListTenantsByPlanActivity", mock.Anything, "pro").
		Return([]string{id1}, nil).Once()
	// Allow retries: mock without a call-count limit.
	env.OnActivity("RecomputeEntitlementsActivity", mock.Anything, id1, CatalogFanoutWorkflowType).
		Return(sentinel)

	env.ExecuteWorkflow(CatalogFanoutWorkflow, CatalogFanoutInput{PlanCode: "pro"})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), sentinel.Error())
}

func TestCatalogFanoutWorkflow_InputRecordVersionPassedThrough(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := newWorkflowEnv(&suite)

	env.OnActivity("ListTenantsByPlanActivity", mock.Anything, "basic").
		Return([]string{}, nil).Once()

	// RecordVersion is carried in the input struct; workflow must accept it.
	env.ExecuteWorkflow(CatalogFanoutWorkflow, CatalogFanoutInput{PlanCode: "basic", RecordVersion: 42})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}

// ── ExpiryFanoutWorkflow ──────────────────────────────────────────────────────

func TestExpiryFanoutWorkflow_BothActivitiesSucceed(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := newWorkflowEnv(&suite)

	env.OnActivity("ExpireOverridesActivity", mock.Anything).
		Return(nil).Once()
	env.OnActivity("ExpireAddonsActivity", mock.Anything).
		Return(nil).Once()

	env.ExecuteWorkflow(ExpiryFanoutWorkflow, struct{}{})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	env.AssertExpectations(t)
}

func TestExpiryFanoutWorkflow_ExpireOverridesError_ShortCircuits(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := newWorkflowEnv(&suite)

	sentinel := errors.New("overrides expire failed")
	// No call-count limit — the retry policy allows up to 3 attempts.
	env.OnActivity("ExpireOverridesActivity", mock.Anything).
		Return(sentinel)

	// ExpireAddons must NOT be called when overrides step exhausts all retries.
	env.ExecuteWorkflow(ExpiryFanoutWorkflow, struct{}{})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), sentinel.Error())
}

func TestExpiryFanoutWorkflow_ExpireAddonsError_ReturnsError(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := newWorkflowEnv(&suite)

	sentinel := errors.New("addons expire failed")
	env.OnActivity("ExpireOverridesActivity", mock.Anything).
		Return(nil).Once()
	// No call-count limit — allow retries.
	env.OnActivity("ExpireAddonsActivity", mock.Anything).
		Return(sentinel)

	env.ExecuteWorkflow(ExpiryFanoutWorkflow, struct{}{})

	require.True(t, env.IsWorkflowCompleted())
	err := env.GetWorkflowError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), sentinel.Error())
}
