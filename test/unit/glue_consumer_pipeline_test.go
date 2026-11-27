package unit_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"testing"
	"time"

	eventcfg "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/config"
	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/inbound/consumer"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/inbound/sqspipeline"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/eventbus"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/service"
)

const pipelineQueueURL = "https://sqs.eu-west-1.amazonaws.com/000000000000/billing-entitlements-q"

// oneShotSQS delivers its messages on the first ReceiveMessage call and then
// returns empty batches. It records deletes and visibility changes.
type oneShotSQS struct {
	mu       sync.Mutex
	pending  []sqstypes.Message
	deleted  []string
	released []string
}

func (s *oneShotSQS) ReceiveMessage(ctx context.Context, _ *awssqs.ReceiveMessageInput, _ ...func(*awssqs.Options)) (*awssqs.ReceiveMessageOutput, error) {
	s.mu.Lock()
	msgs := s.pending
	s.pending = nil
	s.mu.Unlock()
	if len(msgs) == 0 {
		select {
		case <-ctx.Done():
		case <-time.After(20 * time.Millisecond):
		}
	}
	return &awssqs.ReceiveMessageOutput{Messages: msgs}, nil
}

func (s *oneShotSQS) DeleteMessage(_ context.Context, in *awssqs.DeleteMessageInput, _ ...func(*awssqs.Options)) (*awssqs.DeleteMessageOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, aws.ToString(in.ReceiptHandle))
	return &awssqs.DeleteMessageOutput{}, nil
}

func (s *oneShotSQS) ChangeMessageVisibility(_ context.Context, in *awssqs.ChangeMessageVisibilityInput, _ ...func(*awssqs.Options)) (*awssqs.ChangeMessageVisibilityOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released = append(s.released, aws.ToString(in.ReceiptHandle))
	return &awssqs.ChangeMessageVisibilityOutput{}, nil
}

func (s *oneShotSQS) wasDeleted(handle string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, h := range s.deleted {
		if h == handle {
			return true
		}
	}
	return false
}

type recordingDLQ struct {
	mu      sync.Mutex
	reasons []string
}

func (d *recordingDLQ) SendToDLQ(_ context.Context, _ string, _ []byte, _ map[string]string, reason string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.reasons = append(d.reasons, reason)
	return nil
}

func (d *recordingDLQ) ResolveDLQ(_ context.Context, _ string) (string, error) {
	return pipelineQueueURL + "-dlq", nil
}

func (d *recordingDLQ) sent() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string(nil), d.reasons...)
}

// glueFramedMessage builds the SQS message Billing publishes outside dev: a
// platform-events envelope whose payload is the base64 of the 18-byte Glue
// header plus the JSON payload, with dataschema and the schema_id attribute.
func glueFramedMessage(t *testing.T, receipt, eventType string, payload map[string]any) sqstypes.Message {
	t.Helper()
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	schemaVersion := uuid.New()
	framed := make([]byte, 0, 18+len(body))
	framed = append(framed, 0x03, 0x00)
	framed = append(framed, schemaVersion[:]...)
	framed = append(framed, body...)
	env, err := json.Marshal(map[string]any{
		"id":         uuid.NewString(),
		"type":       eventType,
		"source":     "commercial-billing",
		"time":       time.Now().UTC(),
		"dataschema": schemaVersion.String(),
		"data":       base64.StdEncoding.EncodeToString(framed),
	})
	require.NoError(t, err)
	return sqstypes.Message{
		MessageId:     aws.String(uuid.NewString()),
		ReceiptHandle: aws.String(receipt),
		Body:          aws.String(string(env)),
		Attributes:    map[string]string{"ApproximateReceiveCount": "1"},
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			"schema_id": {DataType: aws.String("String"), StringValue: aws.String(schemaVersion.String())},
			"EventType": {DataType: aws.String("String"), StringValue: aws.String(eventType)},
		},
	}
}

// runBillingPipeline wires the production consumer pipeline for
// billing-entitlements-q — sqspipeline.NewConsumer (with its Glue decode
// codec), sqspipeline.Handler with the real consumed-schema validator, and
// the real BillingEventConsumer/AddonService — and runs it until cond holds.
func runBillingPipeline(t *testing.T, msgs []sqstypes.Message, cond func(*oneShotSQS, *recordingDLQ) bool) (*oneShotSQS, *recordingDLQ, *fakeAddonRepo) {
	t.Helper()
	codec, err := eventbus.NewValidatingCodec(eventbus.NoopCodec{})
	require.NoError(t, err)
	validator := validatorAdapter(func(eventType string, payload []byte) error {
		if err := codec.ValidateConsumed(consumer.BillingConsumerName, eventType, payload); err != eventbus.ErrNoSchema {
			return err
		}
		return nil
	})

	addons := newAddonRepo()
	tx := newTxRunner()
	effectSvc := newEffectSvc(newSnapshotRepo(), newOverrideRepo(), addons, newTrialRepo(), &fakeCatalogSvc{}, &fakeIAMClient{}, nil, tx).
		WithLogger(port.NopLogger{})
	addonSvc := service.NewAddonService(addons, newDrawdownRepo(), newShortfallRepo(), effectSvc, tx).WithLogger(port.NopLogger{})
	billing := consumer.NewBillingEventConsumer(nil, effectSvc, nil, addonSvc, port.NopLogger{})

	sqsClient := &oneShotSQS{pending: msgs}
	dlq := &recordingDLQ{}
	cons, err := sqspipeline.NewConsumer(
		eventcfg.SQSConfigEnv{QueueURL: pipelineQueueURL, Region: "eu-west-1", Concurrency: 1, WaitSeconds: 0},
		sqsClient,
		sqspipeline.Handler(consumer.BillingConsumerName, billing.Handle, validator, dlq, port.NopLogger{}),
		dlq,
		port.NopLogger{},
	)
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = cons.Start(ctx) }()
	t.Cleanup(func() { cancel(); _ = cons.Stop(); <-done })

	require.Eventually(t, func() bool { return cond(sqsClient, dlq) }, 5*time.Second, 10*time.Millisecond)
	return sqsClient, dlq, addons
}

type validatorAdapter func(eventType string, payload []byte) error

func (f validatorAdapter) Validate(eventType string, payload []byte) error {
	return f(eventType, payload)
}

// B-1 (BL-OQ-28): a Glue-framed TenantQuotaAddOnChanged — header plus
// schema_id — is decoded, validated against the consumed schema, and applied:
// the add-on pack is granted and the message deleted, not dead-lettered.
func TestPipeline_GlueFramedAddonPurchase_IsApplied(t *testing.T) {
	tenant := uuid.New()
	msg := glueFramedMessage(t, "rh-purchase", "TenantQuotaAddOnChanged", map[string]any{
		"tenant_id": tenant, "resource": "llm_tokens", "delta": 500_000, "reason": "purchase",
		"billing_ref": "pur_" + uuid.NewString(), "purchased_at": time.Now().UTC(),
		"pack_code": "tokens_500k", "pack_quantity": 1,
	})
	sqsClient, dlq, addons := runBillingPipeline(t, []sqstypes.Message{msg}, func(s *oneShotSQS, _ *recordingDLQ) bool {
		return s.wasDeleted("rh-purchase")
	})

	assert.Empty(t, dlq.sent(), "a Glue-framed purchase must not be dead-lettered")
	assert.Empty(t, sqsClient.released, "a Glue-framed purchase must not be left for retry")
	list, err := addons.ListByTenant(context.Background(), tenant, nil)
	require.NoError(t, err)
	require.Len(t, list, 1, "the paid add-on must be granted")
	assert.Equal(t, int64(500_000), list[0].TotalTokens)
	assert.Equal(t, domain.LimitKeyLLMTokenQuotaMonthly, list[0].LimitKey)
}

// B-4 (BL-OQ-28): a TenantQuotaAddOnChanged that violates the consumed schema
// (delta 0, missing billing_ref) is dead-lettered on its first delivery and
// never reaches the add-on service.
func TestPipeline_SchemaInvalidAddonEvent_DeadLetteredOnFirstDelivery(t *testing.T) {
	tenant := uuid.New()
	msg := glueFramedMessage(t, "rh-invalid", "TenantQuotaAddOnChanged", map[string]any{
		"tenant_id": tenant, "resource": "llm_tokens", "delta": 0, "reason": "purchase",
	})
	_, dlq, addons := runBillingPipeline(t, []sqstypes.Message{msg}, func(_ *oneShotSQS, d *recordingDLQ) bool {
		return len(d.sent()) > 0
	})

	assert.Equal(t, []string{"schema_violation"}, dlq.sent())
	list, err := addons.ListByTenant(context.Background(), tenant, nil)
	require.NoError(t, err)
	assert.Empty(t, list)
}
