//go:build integration

// Phase 6 · P6-WIRE — outbox → SNS → SQS wire tests.
// Verifies:
//   - P6-WIRE-001: a row in outbox_events is picked up by the outbox runner,
//     published to SNS, and delivered to a subscribed SQS queue.
//   - P6-WIRE-002: SNS filter policy on EventType delivers only matching
//     events to each filtered subscriber queue.
//   - P6-WIRE-003: a message published without EventType attribute is NOT
//     delivered to a filtered subscriber, but IS delivered to an unfiltered one.
package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/outbox"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

// Test Case ID:      P6-WIRE-001
// Module:            commercial-entitlements · Event pipeline
// Feature:           outbox → SNS → SQS round-trip
// Scenario:          Insert a row in outbox_events; start outbox runner;
//
//	assert the event appears in the subscribed SQS queue.
//
// Expected Result:   SQS message body is a parseable JSON envelope within 10s
func TestP6Wire001_OutboxToSQSRoundTrip(t *testing.T) {
	t.Parallel()
	env := newPhase6Env(t)

	topicARN := createTopic(t, env, "wire001")
	queueURL := createQueue(t, env, "wire001")
	subscribeQueue(t, env, topicARN, queueURL)

	pub, err := events.NewSNSPublisher(events.SNSConfig{
		TopicARN:    topicARN,
		Region:      flociRegion,
		EndpointURL: env.endpointURL,
	})
	require.NoError(t, err)

	envelope := events.NewEnvelope("EntitlementSnapshotComputed", "commercial-entitlements",
		json.RawMessage(`{"tenant_id":"`+env.tenantID.String()+`"}`),
		events.WithSubject(env.tenantID.String()),
	)
	require.NoError(t, pgcommon.RunInTx(env.ctx, env.appPool, pgcommon.TxOptions{},
		func(ctx context.Context, tx pgcommon.Tx) error {
			return outbox.Enqueue(ctx, tx, envelope)
		},
	))

	stop := startOutboxRunner(t, env, pub)
	defer stop()

	require.Eventually(t, func() bool {
		msgs := drainQueue(t, env, queueURL, 1, 1*time.Second)
		if len(msgs) == 0 {
			return false
		}
		var body map[string]any
		return json.Unmarshal([]byte(msgs[0]), &body) == nil
	}, 10*time.Second, 500*time.Millisecond,
		"P6-WIRE-001: event must arrive in SQS within 10s")
}

// Test Case ID:      P6-WIRE-002
// Module:            commercial-entitlements · Event pipeline
// Feature:           SNS filter policy by EventType attribute
// Scenario:          Two queues with different EventType filters.
//
//	Publish one event of each type directly to SNS.
//
// Expected Result:   Each queue receives exactly the matching event type.
func TestP6Wire002_FilterPolicyRoutesCorrectly(t *testing.T) {
	t.Parallel()
	env := newPhase6Env(t)

	topicARN := createTopic(t, env, "wire002")
	qSnapQueue := createQueue(t, env, "wire002-snap")
	qPlanQueue := createQueue(t, env, "wire002-plan")

	withEventTypeFilter(t, env, topicARN, qSnapQueue, []string{"EntitlementSnapshotComputed"})
	withEventTypeFilter(t, env, topicARN, qPlanQueue, []string{"TenantPlanChanged"})

	publishSNS(t, env, topicARN, "EntitlementSnapshotComputed", `{"type":"snap"}`)
	publishSNS(t, env, topicARN, "TenantPlanChanged", `{"type":"plan"}`)

	time.Sleep(500 * time.Millisecond)

	snapMsgs := drainQueue(t, env, qSnapQueue, 10, 3*time.Second)
	planMsgs := drainQueue(t, env, qPlanQueue, 10, 3*time.Second)

	assert.Len(t, snapMsgs, 1, "P6-WIRE-002: snap queue must receive one snap event")
	assert.Len(t, planMsgs, 1, "P6-WIRE-002: plan queue must receive one plan event")
	for _, m := range snapMsgs {
		assert.Contains(t, m, "snap")
	}
	for _, m := range planMsgs {
		assert.Contains(t, m, "plan")
	}
}

// Test Case ID:      P6-WIRE-003
// Module:            commercial-entitlements · Event pipeline
// Feature:           EventType MessageAttribute — absent means filter-policy drops it
// Scenario:          Message published without EventType attribute.
// Expected Result:   Filtered queue: 0 messages; unfiltered queue: 1 message.
func TestP6Wire003_MissingAttributeDroppedByFilter(t *testing.T) {
	t.Parallel()
	env := newPhase6Env(t)

	topicARN := createTopic(t, env, "wire003")
	filteredQueue := createQueue(t, env, "wire003-filtered")
	allQueue := createQueue(t, env, "wire003-all")

	withEventTypeFilter(t, env, topicARN, filteredQueue, []string{"EntitlementSnapshotComputed"})
	subscribeQueue(t, env, topicARN, allQueue)

	msg := `{"no_attribute":true}`
	_, err := env.snsClient.Publish(env.ctx, &sns.PublishInput{
		TopicArn: &topicARN,
		Message:  &msg,
	})
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	filteredMsgs := drainQueue(t, env, filteredQueue, 10, 2*time.Second)
	allMsgs := drainQueue(t, env, allQueue, 10, 2*time.Second)

	assert.Empty(t, filteredMsgs, "P6-WIRE-003: filtered queue must not receive attribute-less message")
	assert.Len(t, allMsgs, 1, "P6-WIRE-003: unfiltered queue must receive the message")
}

// ── helpers ────────────────────────────────────────────────────────────────

func startOutboxRunner(t *testing.T, env *phase6Env, pub events.Publisher) func() {
	t.Helper()
	runner, err := outbox.NewRunner(outbox.Config{
		Pool:               env.appPool,
		Publisher:          pub,
		PollInterval:       100 * time.Millisecond,
		BatchSize:          20,
		MaxAttempts:        5,
		PublishConcurrency: 2,
		PublishTimeout:     5 * time.Second,
		DrainTimeout:       5 * time.Second,
	})
	require.NoError(t, err)

	runCtx, cancel := context.WithCancel(env.ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = runner.Start(runCtx)
	}()
	return func() {
		cancel()
		<-done
		_ = runner.Stop()
	}
}
