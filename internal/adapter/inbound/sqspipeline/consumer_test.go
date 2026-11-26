package sqspipeline

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"

	eventcfg "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
)

// ── fakes ─────────────────────────────────────────────────────────────────

type nopLogger struct{}

func (nopLogger) Info(_ string, _ map[string]any)  {}
func (nopLogger) Warn(_ string, _ map[string]any)  {}
func (nopLogger) Error(_ string, _ map[string]any) {}
func (nopLogger) Debug(_ string, _ map[string]any) {}

type fakeValidator struct {
	err error
}

func (v *fakeValidator) Validate(_ string, _ []byte) error { return v.err }

type fakeDLQ struct {
	resolved   string
	err        error
	sent       bool
	resolveErr error
}

func (d *fakeDLQ) SendToDLQ(_ context.Context, _ string, _ []byte, _ map[string]string, _ string) error {
	d.sent = true
	return d.err
}

func (d *fakeDLQ) ResolveDLQ(_ context.Context, _ string) (string, error) {
	if d.resolveErr != nil {
		return "", d.resolveErr
	}
	return d.resolved, nil
}

func makeEnvPipeline(eventType string) events.Envelope[json.RawMessage] {
	return events.Envelope[json.RawMessage]{
		ID:      "evt-1",
		Type:    eventType,
		Payload: json.RawMessage(`{"tenant_id":"abc"}`),
	}
}

// ── ErrNoSchema ───────────────────────────────────────────────────────────

func TestErrNoSchema_IsError(t *testing.T) {
	assert.EqualError(t, ErrNoSchema, "no schema")
}

// ── Handler ───────────────────────────────────────────────────────────────

func TestHandler_NilValidator_CallsHandlerFn(t *testing.T) {
	var called bool
	h := Handler(
		"test-queue",
		func(_ context.Context, _ events.Envelope[json.RawMessage]) error { called = true; return nil },
		nil,
		nil,
		nopLogger{},
	)
	require.NoError(t, h(context.Background(), makeEnvPipeline("TenantPlanChanged")))
	assert.True(t, called)
}

func TestHandler_ValidatorPass_CallsHandlerFn(t *testing.T) {
	var called bool
	h := Handler(
		"test-queue",
		func(_ context.Context, _ events.Envelope[json.RawMessage]) error { called = true; return nil },
		&fakeValidator{},
		nil,
		nopLogger{},
	)
	require.NoError(t, h(context.Background(), makeEnvPipeline("TenantPlanChanged")))
	assert.True(t, called)
}

func TestHandler_ValidatorFails_ErrNoSchema_CallsHandlerFn(t *testing.T) {
	// ErrNoSchema is forward-compat — handler still invoked
	var called bool
	h := Handler(
		"test-queue",
		func(_ context.Context, _ events.Envelope[json.RawMessage]) error { called = true; return nil },
		&fakeValidator{err: ErrNoSchema},
		nil,
		nopLogger{},
	)
	require.NoError(t, h(context.Background(), makeEnvPipeline("UnknownEvent")))
	assert.True(t, called)
}

func TestHandler_ValidatorFails_OtherError_ForwardsToDLQ(t *testing.T) {
	dlq := &fakeDLQ{}
	h := Handler(
		"test-queue",
		func(_ context.Context, _ events.Envelope[json.RawMessage]) error { return nil },
		&fakeValidator{err: errors.New("schema mismatch")},
		dlq,
		nopLogger{},
	)
	// No source message in context → dlq.SendToDLQ won't be called (SourceMessageFromContext returns ok=false)
	require.NoError(t, h(context.Background(), makeEnvPipeline("TenantPlanChanged")))
}

func TestHandler_ValidatorFails_NilDLQ_NoError(t *testing.T) {
	h := Handler(
		"test-queue",
		func(_ context.Context, _ events.Envelope[json.RawMessage]) error { return nil },
		&fakeValidator{err: errors.New("schema mismatch")},
		nil,
		nopLogger{},
	)
	require.NoError(t, h(context.Background(), makeEnvPipeline("TenantPlanChanged")))
}

func TestHandler_HandlerFnError_Propagates(t *testing.T) {
	want := errors.New("handler failed")
	h := Handler(
		"test-queue",
		func(_ context.Context, _ events.Envelope[json.RawMessage]) error { return want },
		nil,
		nil,
		nopLogger{},
	)
	err := h(context.Background(), makeEnvPipeline("TenantPlanChanged"))
	assert.ErrorIs(t, err, want)
}

// ── EnvForQueue ───────────────────────────────────────────────────────────

func TestEnvForQueue_SetsQueueURLAndConcurrency(t *testing.T) {
	base := eventcfg.SQSConfigEnv{QueueURL: "original", Concurrency: 1}
	result := EnvForQueue(base, "https://sqs.us-east-1.amazonaws.com/123/my-queue", 5)
	assert.Equal(t, "https://sqs.us-east-1.amazonaws.com/123/my-queue", result.QueueURL)
	assert.Equal(t, 5, result.Concurrency)
}

func TestEnvForQueue_ZeroConcurrency_KeepsBase(t *testing.T) {
	base := eventcfg.SQSConfigEnv{QueueURL: "original", Concurrency: 3}
	result := EnvForQueue(base, "new-url", 0)
	assert.Equal(t, "new-url", result.QueueURL)
	assert.Equal(t, 3, result.Concurrency) // unchanged when concurrency=0
}

// ── RequireDLQ ────────────────────────────────────────────────────────────

func TestRequireDLQ_NilDLQ_WarnsAndReturnsNil(t *testing.T) {
	err := RequireDLQ(context.Background(), nil, "https://sqs/queue", nopLogger{})
	require.NoError(t, err)
}

func TestRequireDLQ_DLQResolvesOK_ReturnsNil(t *testing.T) {
	dlq := &fakeDLQ{resolved: "https://sqs/dlq"}
	err := RequireDLQ(context.Background(), dlq, "https://sqs/queue", nopLogger{})
	require.NoError(t, err)
}

func TestRequireDLQ_DLQNotConfigured_ReturnsError(t *testing.T) {
	dlq := &fakeDLQ{resolveErr: events.ErrDLQNotConfigured}
	err := RequireDLQ(context.Background(), dlq, "https://sqs/queue", nopLogger{})
	require.Error(t, err)
}

func TestRequireDLQ_OtherResolveError_WarnsAndReturnsNil(t *testing.T) {
	dlq := &fakeDLQ{resolveErr: errors.New("network error")}
	err := RequireDLQ(context.Background(), dlq, "https://sqs/queue", nopLogger{})
	require.NoError(t, err)
}

// ── InstallTracingHooks ───────────────────────────────────────────────────

func TestInstallTracingHooks_NoPanic(t *testing.T) {
	assert.NotPanics(t, func() {
		InstallTracingHooks("commercial-entitlements")
	})
	// Restore after test
	t.Cleanup(func() {
		port.SetTraceExtractor(nil)
		port.SetSpanContextExtractor(nil)
		port.SetConsumerSpanFunc(nil)
	})
}

// ── NewConsumer ───────────────────────────────────────────────────────────

// stubSQSClient satisfies events.SQSClientLike with no-op implementations so
// NewConsumer can be called in a unit test without real AWS credentials.
type stubSQSClient struct{}

func (s *stubSQSClient) ReceiveMessage(_ context.Context, _ *awssqs.ReceiveMessageInput, _ ...func(*awssqs.Options)) (*awssqs.ReceiveMessageOutput, error) {
	return &awssqs.ReceiveMessageOutput{}, nil
}
func (s *stubSQSClient) DeleteMessage(_ context.Context, _ *awssqs.DeleteMessageInput, _ ...func(*awssqs.Options)) (*awssqs.DeleteMessageOutput, error) {
	return &awssqs.DeleteMessageOutput{}, nil
}
func (s *stubSQSClient) ChangeMessageVisibility(_ context.Context, _ *awssqs.ChangeMessageVisibilityInput, _ ...func(*awssqs.Options)) (*awssqs.ChangeMessageVisibilityOutput, error) {
	return &awssqs.ChangeMessageVisibilityOutput{}, nil
}

func TestNewConsumer_WithStubSQSClient_ReturnsConsumer(t *testing.T) {
	env := eventcfg.SQSConfigEnv{
		QueueURL:    "https://sqs.us-east-1.amazonaws.com/123456789012/test-queue",
		Region:      "us-east-1",
		Concurrency: 1,
	}
	handlerFn := func(_ context.Context, _ events.Envelope[json.RawMessage]) error { return nil }
	consumer, err := NewConsumer(env, &stubSQSClient{}, handlerFn, nil, nopLogger{})
	require.NoError(t, err)
	assert.NotNil(t, consumer)
}

func TestNewConsumer_WithDLQ_ReturnsConsumer(t *testing.T) {
	env := eventcfg.SQSConfigEnv{
		QueueURL:    "https://sqs.us-east-1.amazonaws.com/123456789012/test-queue",
		Region:      "us-east-1",
		Concurrency: 2,
	}
	handlerFn := func(_ context.Context, _ events.Envelope[json.RawMessage]) error { return nil }
	dlq := &fakeDLQ{resolved: "https://sqs.us-east-1.amazonaws.com/123456789012/test-queue-dlq"}
	consumer, err := NewConsumer(env, &stubSQSClient{}, handlerFn, dlq, nopLogger{})
	require.NoError(t, err)
	assert.NotNil(t, consumer)
}
