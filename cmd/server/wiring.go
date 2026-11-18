package main

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/glue"

	eventbusadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/eventbus"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/core/port"
	eventcfg "github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/config"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
)

// buildTopicPublisher returns an SNS publisher for topicARN carrying codec
// (applied transiently at publish time via events.WithCodec — never touches
// outbox_events), or a noop publisher when the ARN is empty (dev/test without
// SNS configured).
//
// Region and EndpointURL come from eventcfg.LoadSNS (AWS_REGION /
// AWS_ENDPOINT_URL). TopicARN is the per-topic override because this service
// publishes to one SNS topic and LoadSNS only reads SNS_TOPIC_ARN.
func buildTopicPublisher(topicARN string, codec events.Codec, log port.Logger) (events.Publisher, error) {
	if topicARN == "" {
		return eventbusadapter.NoopPublisher{}, nil
	}
	snsEnv := eventcfg.LoadSNS()
	snsEnv.TopicARN = topicARN
	return events.NewSNSPublisher(eventcfg.SNSConfigFromEnv(snsEnv, log), events.WithCodec(codec))
}

// buildTopicCodec returns a GlueCodec whose schema version UUIDs are
// resolved once, by definition, from registryName — fixed for the life of
// the process — or a NoopCodec (plain JSON) when registryName is empty
// (dev/test without a Glue registry configured).
//
// events.NoopCodec (platform-events' own identity Codec) is used for the
// noop case, NOT the local eventbusadapter.NoopCodec — those are different
// Encode-only interfaces used at outbox-enqueue time for schema validation,
// not the Encode+Decode events.Codec that WithCodec expects.
func buildTopicCodec(ctx context.Context, glueClient *glue.Client, registryName string, schemaNames []string) (events.Codec, error) {
	if registryName == "" {
		return events.NoopCodec{}, nil
	}
	return eventbusadapter.NewGlueCodec(ctx, glueClient, registryName, schemaNames)
}
