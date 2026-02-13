package internal

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// parseGCPIGMSelfLink tests - verifies parsing of GCP IGM self-links

func TestParseGCPIGMSelfLink_Zonal_ParsesCorrectly(t *testing.T) {
	resourceID := "projects/my-project/zones/us-central1-a/instanceGroupManagers/my-mig"

	result, err := parseGCPIGMSelfLink(resourceID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "my-project", result.Project)
	require.Equal(t, "us-central1-a", result.Location)
	require.Equal(t, "my-mig", result.Name)
	require.False(t, result.IsRegional)
}

func TestParseGCPIGMSelfLink_Regional_ParsesCorrectly(t *testing.T) {
	resourceID := "projects/my-project/regions/us-central1/instanceGroupManagers/my-mig"

	result, err := parseGCPIGMSelfLink(resourceID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "my-project", result.Project)
	require.Equal(t, "us-central1", result.Location)
	require.Equal(t, "my-mig", result.Name)
	require.True(t, result.IsRegional)
}

func TestParseGCPIGMSelfLink_EmptyString_ReturnsError(t *testing.T) {
	resourceID := ""

	result, err := parseGCPIGMSelfLink(resourceID)

	require.Nil(t, result)
	require.EqualError(t, err, "IGM self-link cannot be empty")
}

func TestParseGCPIGMSelfLink_WrongNumberOfParts_ReturnsError(t *testing.T) {
	resourceID := "projects/my-project/zones/us-central1-a"

	result, err := parseGCPIGMSelfLink(resourceID)

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IGM self-link format")
}

func TestParseGCPIGMSelfLink_WrongPrefix_ReturnsError(t *testing.T) {
	resourceID := "notprojects/my-project/zones/us-central1-a/instanceGroupManagers/my-mig"

	result, err := parseGCPIGMSelfLink(resourceID)

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IGM self-link format")
}

func TestParseGCPIGMSelfLink_WrongLocationType_ReturnsError(t *testing.T) {
	resourceID := "projects/my-project/foos/us-central1-a/instanceGroupManagers/my-mig"

	result, err := parseGCPIGMSelfLink(resourceID)

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IGM self-link format")
}

func TestParseGCPIGMSelfLink_WrongResourceType_ReturnsError(t *testing.T) {
	resourceID := "projects/my-project/zones/us-central1-a/notInstanceGroupManagers/my-mig"

	result, err := parseGCPIGMSelfLink(resourceID)

	require.Nil(t, result)
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid IGM self-link format")
}
