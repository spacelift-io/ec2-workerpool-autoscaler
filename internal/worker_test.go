package internal_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/spacelift-io/awsautoscalr/internal"
)

func TestWorker_InstanceIdentity_NoMetadata_ReturnsError(t *testing.T) {
	sut := &internal.Worker{
		Metadata: "{}",
	}

	groupID, instanceID, err := sut.InstanceIdentity()

	require.Error(t, err)
	require.ErrorContains(t, err, "metadata asg_id not present")
	require.ErrorContains(t, err, "metadata instance_id not present")
	require.Empty(t, groupID)
	require.Empty(t, instanceID)
}

func TestWorker_InstanceIdentity_ValidMetadata_ReturnsGroupAndInstanceIDs(t *testing.T) {
	sut := &internal.Worker{
		Metadata: `{"asg_id": "group", "instance_id": "instance"}`,
	}

	groupID, instanceID, err := sut.InstanceIdentity()

	require.NoError(t, err)
	require.Equal(t, internal.GroupID("group"), groupID)
	require.Equal(t, internal.InstanceID("instance"), instanceID)
}
