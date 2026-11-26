package main

import (
	"context"
	"testing"

	eventbusadapter "github.com/BCBP-SOLUTIONS-FZC-LLC/commercial-entitlements/internal/adapter/outbound/eventbus"
	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildTopicPublisher_EmptyARN_ReturnsNoop(t *testing.T) {
	pub, err := buildTopicPublisher("", events.NoopCodec{}, nil)
	require.NoError(t, err)
	_, ok := pub.(eventbusadapter.NoopPublisher)
	assert.True(t, ok, "empty ARN must wire the NoopPublisher")
}

func TestBuildTopicCodec_EmptyRegistry_ReturnsNoopCodec(t *testing.T) {
	codec, err := buildTopicCodec(context.Background(), nil, "", nil)
	require.NoError(t, err)
	_, ok := codec.(events.NoopCodec)
	assert.True(t, ok, "empty registry name must wire events.NoopCodec")
}
