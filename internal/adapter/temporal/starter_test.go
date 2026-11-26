package temporal_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	temporalclient "go.temporal.io/sdk/client"
	temporalmocks "go.temporal.io/sdk/mocks"

	. "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/temporal"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

var _ port.TemporalStarter = (*Starter)(nil)

// TestConstants_NonEmpty ensures the exported package constants are populated
// and distinct from each other.
func TestConstants_NonEmpty(t *testing.T) {
	assert.NotEmpty(t, TaskQueue)
	assert.NotEmpty(t, CatalogFanoutWorkflowType)
	assert.NotEmpty(t, ExpiryFanoutWorkflowType)
	assert.NotEqual(t, CatalogFanoutWorkflowType, ExpiryFanoutWorkflowType)
}

// TestStartCatalogFanout_UsesExpectedWorkflowID verifies that StartCatalogFanout
// uses a deterministic, plan-qualified workflow ID and the correct task queue.
func TestStartCatalogFanout_UsesExpectedWorkflowID(t *testing.T) {
	c := &temporalmocks.Client{}
	run := &temporalmocks.WorkflowRun{}
	run.On("GetID").Return("catalog-fanout-pro")
	run.On("GetRunID").Return("run-1")

	c.On("ExecuteWorkflow", mock.Anything,
		mock.MatchedBy(func(opts temporalclient.StartWorkflowOptions) bool {
			return opts.ID == "catalog-fanout-pro" && opts.TaskQueue == TaskQueue
		}),
		mock.Anything, mock.Anything).
		Return(run, nil).Once()

	s := NewStarter(c)
	require.NoError(t, s.StartCatalogFanout(context.Background(), "pro"))
	c.AssertExpectations(t)
}

// TestStartCatalogFanout_DifferentPlans_DistinctIDs ensures plan code is
// embedded in the workflow ID.
func TestStartCatalogFanout_DifferentPlans_DistinctIDs(t *testing.T) {
	c := &temporalmocks.Client{}
	run := &temporalmocks.WorkflowRun{}
	run.On("GetID").Return("wf-id")
	run.On("GetRunID").Return("run-id")

	var capturedIDs []string
	c.On("ExecuteWorkflow", mock.Anything,
		mock.MatchedBy(func(opts temporalclient.StartWorkflowOptions) bool {
			capturedIDs = append(capturedIDs, opts.ID)
			return true
		}),
		mock.Anything, mock.Anything).
		Return(run, nil)

	s := NewStarter(c)
	require.NoError(t, s.StartCatalogFanout(context.Background(), "starter"))
	require.NoError(t, s.StartCatalogFanout(context.Background(), "enterprise"))

	require.Len(t, capturedIDs, 2)
	assert.NotEqual(t, capturedIDs[0], capturedIDs[1])
}

// TestStartExpiryFanout_UsesExpectedWorkflowID ensures StartExpiryFanout uses
// a stable (non-plan-qualified) workflow ID.
func TestStartExpiryFanout_UsesExpectedWorkflowID(t *testing.T) {
	c := &temporalmocks.Client{}
	run := &temporalmocks.WorkflowRun{}
	run.On("GetID").Return("expiry-fanout")
	run.On("GetRunID").Return("run-1")

	c.On("ExecuteWorkflow", mock.Anything,
		mock.MatchedBy(func(opts temporalclient.StartWorkflowOptions) bool {
			return opts.ID == "expiry-fanout" && opts.TaskQueue == TaskQueue
		}),
		mock.Anything, mock.Anything).
		Return(run, nil).Once()

	s := NewStarter(c)
	require.NoError(t, s.StartExpiryFanout(context.Background()))
	c.AssertExpectations(t)
}

// TestStartCatalogFanout_PropagatesClientError confirms client errors surface.
func TestStartCatalogFanout_PropagatesClientError(t *testing.T) {
	sentinel := errors.New("temporal unavailable")
	c := &temporalmocks.Client{}
	c.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, sentinel).Once()

	s := NewStarter(c)
	err := s.StartCatalogFanout(context.Background(), "starter")
	assert.ErrorIs(t, err, sentinel)
}

// TestStartExpiryFanout_PropagatesClientError is the same for expiry fanout.
func TestStartExpiryFanout_PropagatesClientError(t *testing.T) {
	sentinel := errors.New("temporal unavailable")
	c := &temporalmocks.Client{}
	c.On("ExecuteWorkflow", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, sentinel).Once()

	s := NewStarter(c)
	err := s.StartExpiryFanout(context.Background())
	assert.ErrorIs(t, err, sentinel)
}
