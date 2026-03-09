package internal_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/spacelift-io/awsautoscalr/internal"
)

func TestWorker_MetadataValue_NoMetadata_ReturnsError(t *testing.T) {
	sut := &internal.Worker{
		Metadata: "{}",
	}

	value, err := sut.MetadataValue("some_key")

	require.Error(t, err)
	require.ErrorContains(t, err, "metadata some_key not present")
	require.Empty(t, value)
}

func TestWorker_MetadataValue_ValidMetadata_ReturnsValue(t *testing.T) {
	sut := &internal.Worker{
		Metadata: `{"some_key": "some_value"}`,
	}

	value, err := sut.MetadataValue("some_key")

	require.NoError(t, err)
	require.Equal(t, "some_value", value)
}

func TestWorker_MetadataValue_InvalidJSON_ReturnsError(t *testing.T) {
	sut := &internal.Worker{
		Metadata: "not valid json",
	}

	value, err := sut.MetadataValue("some_key")

	require.Error(t, err)
	require.ErrorContains(t, err, "invalid instance metadata")
	require.Empty(t, value)
}
