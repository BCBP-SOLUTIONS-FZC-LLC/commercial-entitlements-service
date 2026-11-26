package eventbus

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/BCBP-SOLUTIONS-FZC-LLC/platform-events/v2/pkg/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNoopCodec_Encode_PassesThrough(t *testing.T) {
	c := NoopCodec{}
	payload := []byte(`{"key":"value"}`)
	encoded, schemaVersionID, err := c.Encode(context.Background(), "test_event", payload)
	require.NoError(t, err)
	assert.Equal(t, payload, encoded)
	assert.Empty(t, schemaVersionID)
}

func TestNoopCodec_Encode_EmptyPayload(t *testing.T) {
	c := NoopCodec{}
	encoded, _, err := c.Encode(context.Background(), "test_event", []byte{})
	require.NoError(t, err)
	assert.Empty(t, encoded)
}

func TestIsNoSchemaErr_Nil(t *testing.T) {
	assert.False(t, isNoSchemaErr(nil))
}

func TestIsNoSchemaErr_ErrNoSchema_True(t *testing.T) {
	assert.True(t, isNoSchemaErr(ErrNoSchema))
}

func TestNoopPublisher_Publish_NoError(t *testing.T) {
	p := NoopPublisher{}
	require.NoError(t, p.Publish(context.Background(), events.Envelope[json.RawMessage]{}))
}

func TestNoopPublisher_PublishBatch_NoError(t *testing.T) {
	p := NoopPublisher{}
	require.NoError(t, p.PublishBatch(context.Background(), nil))
}

func TestIsNoSchemaErr_OtherError_False(t *testing.T) {
	assert.False(t, isNoSchemaErr(assert.AnError))
}
