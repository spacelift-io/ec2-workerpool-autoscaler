package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// parseGCPIGMID tests - verifies parsing of GCP IGM IDs

func TestParseGCPIGMID_Zonal_ParsesCorrectly(t *testing.T) {
	resourceID := "projects/my-project/zones/us-central1-a/instanceGroupManagers/my-mig"

	result, err := parseGCPIGMID(resourceID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "my-project", result.Project)
	require.Equal(t, "us-central1-a", result.Location)
	require.Equal(t, "my-mig", result.Name)
	require.False(t, result.IsRegional)
}

func TestParseGCPIGMID_Regional_ParsesCorrectly(t *testing.T) {
	resourceID := "projects/my-project/regions/us-central1/instanceGroupManagers/my-mig"

	result, err := parseGCPIGMID(resourceID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "my-project", result.Project)
	require.Equal(t, "us-central1", result.Location)
	require.Equal(t, "my-mig", result.Name)
	require.True(t, result.IsRegional)
}

func TestParseGCPIGMID_EmptyString_ReturnsError(t *testing.T) {
	resourceID := ""

	result, err := parseGCPIGMID(resourceID)

	require.Nil(t, result)
	require.EqualError(t, err, "IGM ID cannot be empty")
}

func TestParseGCPIGMID_WrongNumberOfParts_ReturnsError(t *testing.T) {
	resourceID := "projects/my-project/zones/us-central1-a"

	result, err := parseGCPIGMID(resourceID)

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IGM ID format")
}

func TestParseGCPIGMID_WrongPrefix_ReturnsError(t *testing.T) {
	resourceID := "notprojects/my-project/zones/us-central1-a/instanceGroupManagers/my-mig"

	result, err := parseGCPIGMID(resourceID)

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IGM ID format")
}

func TestParseGCPIGMID_WrongLocationType_ReturnsError(t *testing.T) {
	resourceID := "projects/my-project/foos/us-central1-a/instanceGroupManagers/my-mig"

	result, err := parseGCPIGMID(resourceID)

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IGM ID format")
}

func TestParseGCPIGMID_WrongResourceType_ReturnsError(t *testing.T) {
	resourceID := "projects/my-project/zones/us-central1-a/notInstanceGroupManagers/my-mig"

	result, err := parseGCPIGMID(resourceID)

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IGM ID format")
}
