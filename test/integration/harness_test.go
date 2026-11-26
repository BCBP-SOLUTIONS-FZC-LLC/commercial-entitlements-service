//go:build integration

// Package integration_test hosts Phase 6 integration tests that exercise
// the async event pipeline end-to-end — outbox → SNS → SQS — against real
// Postgres + real SNS/SQS via floci (open-source AWS emulator).
//
// Design notes:
//
//   - One floci container is shared across the package via TestMain.
//   - Each test gets its own Postgres container (~2s), unique SNS topics and
//     SQS queues (suffix-randomised), so parallel tests never share traffic.
//   - drainQueue polls with a short wait-time so test timeouts surface fast.
package integration_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awscreds "github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	snstypes "github.com/aws/aws-sdk-go-v2/service/sns/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	pgadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/postgres"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/test/dbseed"
	pgdomain "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/domain"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-pgcommon/v2/pkg/pgcommon"
)

const (
	flociImage   = "floci/floci:2.1.0"
	flociRegion  = "ap-south-1"
	flociAccount = "000000000000"
)

var (
	sharedFloci       testcontainers.Container
	sharedEndpointURL string
	sharedFlociErr    error
	sharedFlociOnce   sync.Once
)

func TestMain(m *testing.M) {
	for _, key := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"} {
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, "test")
		}
	}
	code := m.Run()
	if sharedFloci != nil {
		_ = sharedFloci.Terminate(context.Background())
	}
	os.Exit(code)
}

// startFloci starts the shared floci container once per package.
// Retried up to maxFlociAttempts times with exponential backoff because
// Docker Desktop can lose the port-registration race on the first attempt
// (same pattern as iam-org-membership setupTestDB).
const maxFlociAttempts = 3

func startFloci(t *testing.T) string {
	t.Helper()
	sharedFlociOnce.Do(func() {
		ctx := context.Background()
		var ctr testcontainers.Container
		var err error
		for attempt := 1; attempt <= maxFlociAttempts; attempt++ {
			ctr, err = testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
				ContainerRequest: testcontainers.ContainerRequest{
					Image:        flociImage,
					ExposedPorts: []string{"4566/tcp"},
					// Use log-based wait: floci logs "started in X.XXXs" when it
					// is ready to handle requests. HTTP health-check on the mapped
					// port is unreliable on macOS Docker Desktop because the
					// localhost port-mapping proxy takes time after the container
					// starts — the log line fires as soon as the process is live.
					WaitingFor: wait.ForLog("started in").
						WithOccurrence(1).
						WithStartupTimeout(120 * time.Second),
					// Use floci-native vars (not LocalStack SERVICES=) so
					// all services — including Glue Schema Registry — start.
					// FLOCI_DEFAULT_ACCOUNT_ID is required for Glue ARN construction.
					Env: map[string]string{
						"FLOCI_DEFAULT_REGION":     flociRegion,
						"FLOCI_DEFAULT_ACCOUNT_ID": flociAccount,
					},
				},
				Started: true,
			})
			if err == nil {
				break
			}
			t.Logf("startFloci: attempt %d/%d failed: %v — retrying", attempt, maxFlociAttempts, err)
			if ctr != nil {
				_ = ctr.Terminate(ctx)
			}
			time.Sleep(time.Duration(attempt) * time.Second)
		}
		if err != nil {
			sharedFlociErr = fmt.Errorf("start floci: %w", err)
			return
		}
		sharedFloci = ctr
		host, hErr := ctr.Host(ctx)
		port, pErr := ctr.MappedPort(ctx, "4566")
		if hErr != nil || pErr != nil {
			sharedFlociErr = fmt.Errorf("floci address: host=%v port=%v", hErr, pErr)
			return
		}
		sharedEndpointURL = fmt.Sprintf("http://%s:%s", host, port.Port())
	})
	require.NoError(t, sharedFlociErr, "floci container must start")
	return sharedEndpointURL
}

// phase6Env holds resources for a single integration test.
type phase6Env struct {
	ctx         context.Context
	dsn         string
	seedPool    *dbseed.Pool
	appPool     *pgcommon.Pool
	endpointURL string
	snsClient   *sns.Client
	sqsClient   *sqs.Client
	tenantID    uuid.UUID
}

// newPhase6Env spins up a fresh Postgres container and connects to the
// shared floci instance.
func newPhase6Env(t *testing.T) *phase6Env {
	t.Helper()
	endpointURL := startFloci(t)
	ctx := context.Background()
	tenantID := uuid.New()
	dsn, seedPool, appPool := setupIntegrationDB(t, ctx, tenantID)

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(flociRegion),
		awsconfig.WithCredentialsProvider(awscreds.NewStaticCredentialsProvider("test", "test", "")),
		awsconfig.WithBaseEndpoint(endpointURL),
	)
	require.NoError(t, err)

	return &phase6Env{
		ctx: ctx, dsn: dsn, seedPool: seedPool, appPool: appPool,
		endpointURL: endpointURL,
		snsClient:   sns.NewFromConfig(awsCfg),
		sqsClient:   sqs.NewFromConfig(awsCfg),
		tenantID:    tenantID,
	}
}

func setupIntegrationDB(t *testing.T, ctx context.Context, tenantID uuid.UUID) (string, *dbseed.Pool, *pgcommon.Pool) {
	t.Helper()
	ctr, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "postgres:16-alpine",
			ExposedPorts: []string{"5432/tcp"},
			Env: map[string]string{
				"POSTGRES_USER": "postgres", "POSTGRES_PASSWORD": "password",
				"POSTGRES_DB": "entitlements_int_test",
			},
			WaitingFor: wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60 * time.Second),
		},
		Started: true,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })

	host, _ := ctr.Host(ctx)
	port, _ := ctr.MappedPort(ctx, "5432")
	dsn := fmt.Sprintf("postgres://postgres:password@%s:%s/entitlements_int_test?sslmode=disable", host, port.Port())

	// Create both roles BEFORE migrations (mirrors scripts/init-db.sql):
	//   commercial_entitlements_app — target of GRANT statements in 000003/000004.
	//   entitlements_app            — placeholder required because migration 000002
	//     (on main, immutable per MIG-1) contains an unconditional bare GRANT to this
	//     old name; without it MigrateSchema aborts on a fresh database.
	superSeed, seedErr := dbseed.New(ctx, dsn)
	require.NoError(t, seedErr)
	require.NoError(t, superSeed.Exec(ctx,
		"CREATE ROLE commercial_entitlements_app NOLOGIN"))
	require.NoError(t, superSeed.Exec(ctx,
		"CREATE ROLE entitlements_app NOLOGIN"))
	superSeed.Close()

	require.NoError(t, pgadapter.MigrateSchema(ctx, dsn, nil))

	seedPool, err := dbseed.New(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(seedPool.Close)

	appPool, err := pgcommon.NewPool(ctx, pgcommon.Config{
		DSN: dsn, PGBouncerMode: false,
		GUCProvider: func(context.Context) (pgdomain.GUCSet, bool) {
			return pgdomain.GUCSet{TenantID: tenantID.String()}, true
		},
	})
	require.NoError(t, err)
	t.Cleanup(appPool.Close)

	return dsn, seedPool, appPool
}

// ── SNS / SQS helpers ──────────────────────────────────────────────────────

func createTopic(t *testing.T, env *phase6Env, suffix string) string {
	t.Helper()
	name := fmt.Sprintf("ent-%s-%s", suffix, uuid.New().String()[:8])
	out, err := env.snsClient.CreateTopic(env.ctx, &sns.CreateTopicInput{
		Name:       &name,
		Attributes: map[string]string{"FifoTopic": "false"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = env.snsClient.DeleteTopic(context.Background(), &sns.DeleteTopicInput{TopicArn: out.TopicArn})
	})
	return *out.TopicArn
}

func createQueue(t *testing.T, env *phase6Env, suffix string) string {
	t.Helper()
	name := fmt.Sprintf("ent-%s-%s", suffix, uuid.New().String()[:8])
	out, err := env.sqsClient.CreateQueue(env.ctx, &sqs.CreateQueueInput{QueueName: &name})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = env.sqsClient.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: out.QueueUrl})
	})
	return *out.QueueUrl
}

// queueARN converts a floci SQS URL to its SNS-subscription ARN.
// URL form: http://host:port/000000000000/name
func queueARN(queueURL string) string {
	segs := strings.Split(queueURL, "/")
	if len(segs) < 2 {
		return ""
	}
	account := segs[len(segs)-2]
	name := segs[len(segs)-1]
	return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", flociRegion, account, name)
}

func subscribeQueue(t *testing.T, env *phase6Env, topicARN, queueURL string) {
	t.Helper()
	arn := queueARN(queueURL)
	out, err := env.snsClient.Subscribe(env.ctx, &sns.SubscribeInput{
		TopicArn:   &topicARN,
		Protocol:   strPtr("sqs"),
		Endpoint:   &arn,
		Attributes: map[string]string{"RawMessageDelivery": "true"},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = env.snsClient.Unsubscribe(context.Background(), &sns.UnsubscribeInput{SubscriptionArn: out.SubscriptionArn})
	})
}

func withEventTypeFilter(t *testing.T, env *phase6Env, topicARN, queueURL string, eventTypes []string) {
	t.Helper()
	arn := queueARN(queueURL)
	quoted := make([]string, len(eventTypes))
	for i, et := range eventTypes {
		quoted[i] = `"` + et + `"`
	}
	filter := `{"EventType":[` + strings.Join(quoted, ",") + `]}`
	out, err := env.snsClient.Subscribe(env.ctx, &sns.SubscribeInput{
		TopicArn: &topicARN,
		Protocol: strPtr("sqs"),
		Endpoint: &arn,
		Attributes: map[string]string{
			"RawMessageDelivery": "true",
			"FilterPolicy":       filter,
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = env.snsClient.Unsubscribe(context.Background(), &sns.UnsubscribeInput{SubscriptionArn: out.SubscriptionArn})
	})
}

func publishSNS(t *testing.T, env *phase6Env, topicARN, eventType, payload string) {
	t.Helper()
	_, err := env.snsClient.Publish(env.ctx, &sns.PublishInput{
		TopicArn: &topicARN,
		Message:  &payload,
		MessageAttributes: map[string]snstypes.MessageAttributeValue{
			"EventType": {DataType: strPtr("String"), StringValue: &eventType},
		},
	})
	require.NoError(t, err)
}

func drainQueue(t *testing.T, env *phase6Env, queueURL string, maxMessages int32, timeout time.Duration) []string {
	t.Helper()
	var msgs []string
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && len(msgs) < int(maxMessages) {
		out, err := env.sqsClient.ReceiveMessage(env.ctx, &sqs.ReceiveMessageInput{
			QueueUrl: &queueURL, MaxNumberOfMessages: maxMessages, WaitTimeSeconds: 1,
		})
		require.NoError(t, err)
		for _, m := range out.Messages {
			msgs = append(msgs, *m.Body)
			_, _ = env.sqsClient.DeleteMessage(env.ctx, &sqs.DeleteMessageInput{
				QueueUrl: &queueURL, ReceiptHandle: m.ReceiptHandle,
			})
		}
	}
	return msgs
}

func strPtr(s string) *string { return &s }
