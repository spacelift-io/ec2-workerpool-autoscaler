package internal

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuntimeConfig_Parse_GCP(t *testing.T) {
	// Save and clear environment
	originalEnv := os.Environ()
	os.Clearenv()
	defer func() {
		os.Clearenv()
		for _, e := range originalEnv {
			for i := 0; i < len(e); i++ {
				if e[i] == '=' {
					os.Setenv(e[:i], e[i+1:])
					break
				}
			}
		}
	}()

	testIGMID := "projects/my-project/zones/us-central1-a/instanceGroupManagers/my-mig"

	// Set required env vars for GCP platform
	os.Setenv("SPACELIFT_API_KEY_ID", "test-key-id")
	os.Setenv("SPACELIFT_API_KEY_SECRET_NAME", "projects/my-project/secrets/my-secret/versions/latest")
	os.Setenv("SPACELIFT_API_KEY_ENDPOINT", "https://demo.app.spacelift.io")
	os.Setenv("SPACELIFT_WORKER_POOL_ID", "test-worker-pool")
	os.Setenv("AUTOSCALING_MAX_SIZE", "10")
	os.Setenv("GCP_IGM_ID", testIGMID)

	cfg := &RuntimeConfig{}
	err := cfg.Parse(PlatformGCP)
	require.NoError(t, err, "Parse should succeed with valid GCP config")

	// Verify GCPIGMID is parsed from GCP_IGM_ID env var
	assert.Equal(t, testIGMID, cfg.GCPIGMID, "GCPIGMID should be parsed from GCP_IGM_ID")

	// Verify GroupKeyAndID returns correct values for GCP
	key, id := cfg.GroupKeyAndID()
	assert.Equal(t, "igm_id", key, "GroupKeyAndID should return 'igm_id' key for GCP")
	assert.Equal(t, testIGMID, id, "GroupKeyAndID should return the IGM ID")
}

func TestRuntimeConfig_Parse_GCP_MissingIGMID(t *testing.T) {
	// Save and clear environment
	originalEnv := os.Environ()
	os.Clearenv()
	defer func() {
		os.Clearenv()
		for _, e := range originalEnv {
			for i := 0; i < len(e); i++ {
				if e[i] == '=' {
					os.Setenv(e[:i], e[i+1:])
					break
				}
			}
		}
	}()

	// Set required env vars but NOT GCP_IGM_ID (intentionally omitted)
	os.Setenv("SPACELIFT_API_KEY_ID", "test-key-id")
	os.Setenv("SPACELIFT_API_KEY_SECRET_NAME", "projects/my-project/secrets/my-secret/versions/latest")
	os.Setenv("SPACELIFT_API_KEY_ENDPOINT", "https://demo.app.spacelift.io")
	os.Setenv("SPACELIFT_WORKER_POOL_ID", "test-worker-pool")
	os.Setenv("AUTOSCALING_MAX_SIZE", "10")
	cfg := &RuntimeConfig{}
	err := cfg.Parse(PlatformGCP)
	require.Error(t, err, "Parse should fail when GCP_IGM_ID is not set")
	assert.Contains(t, err.Error(), "GCP_IGM_ID", "Error should mention missing GCP_IGM_ID")
}

func TestRuntimeConfig_Parse_GCP_NonAwsEnvFields(t *testing.T) {
	// Save and clear environment
	originalEnv := os.Environ()
	os.Clearenv()
	defer func() {
		os.Clearenv()
		for _, e := range originalEnv {
			for i := 0; i < len(e); i++ {
				if e[i] == '=' {
					os.Setenv(e[:i], e[i+1:])
					break
				}
			}
		}
	}()

	testIGMID := "projects/my-project/regions/us-central1/instanceGroupManagers/my-regional-mig"

	// Set required env vars for GCP platform including nonAwsEnv fields
	os.Setenv("SPACELIFT_API_KEY_ID", "test-key-id")
	os.Setenv("SPACELIFT_API_KEY_SECRET_NAME", "projects/my-project/secrets/my-secret/versions/latest")
	os.Setenv("SPACELIFT_API_KEY_ENDPOINT", "https://demo.app.spacelift.io")
	os.Setenv("SPACELIFT_WORKER_POOL_ID", "test-worker-pool")
	os.Setenv("AUTOSCALING_MIN_SIZE", "2")
	os.Setenv("AUTOSCALING_MAX_SIZE", "10")
	os.Setenv("GCP_IGM_ID", testIGMID)

	cfg := &RuntimeConfig{}
	err := cfg.Parse(PlatformGCP)
	require.NoError(t, err, "Parse should succeed with valid GCP config")

	// Verify GCP-specific field
	assert.Equal(t, testIGMID, cfg.GCPIGMID, "GCPIGMID should be parsed")

	// Verify nonAwsEnv fields are also parsed for GCP
	assert.Equal(t, uint(2), cfg.AutoscalingMinSize, "AutoscalingMinSize should be parsed from AUTOSCALING_MIN_SIZE")
	assert.Equal(t, uint(10), cfg.AutoscalingMaxSize, "AutoscalingMaxSize should be parsed from AUTOSCALING_MAX_SIZE")
}

// TestRuntimeConfig_Parse_GCP_MissingMaxSize verifies that Parse returns an error
// when AUTOSCALING_MAX_SIZE is not set for GCP.
func TestRuntimeConfig_Parse_GCP_MissingMaxSize(t *testing.T) {
	// Save and clear environment
	originalEnv := os.Environ()
	os.Clearenv()
	defer func() {
		os.Clearenv()
		for _, e := range originalEnv {
			for i := 0; i < len(e); i++ {
				if e[i] == '=' {
					os.Setenv(e[:i], e[i+1:])
					break
				}
			}
		}
	}()

	// Set all required env vars EXCEPT AUTOSCALING_MAX_SIZE
	os.Setenv("SPACELIFT_API_KEY_ID", "test-key-id")
	os.Setenv("SPACELIFT_API_KEY_SECRET_NAME", "projects/my-project/secrets/my-secret/versions/latest")
	os.Setenv("SPACELIFT_API_KEY_ENDPOINT", "https://demo.app.spacelift.io")
	os.Setenv("SPACELIFT_WORKER_POOL_ID", "test-worker-pool")
	os.Setenv("GCP_IGM_ID", "projects/my-project/zones/us-central1-a/instanceGroupManagers/my-mig")
	// AUTOSCALING_MAX_SIZE is intentionally NOT set

	cfg := &RuntimeConfig{}
	err := cfg.Parse(PlatformGCP)
	require.Error(t, err, "Parse should fail when AUTOSCALING_MAX_SIZE is not set")
	assert.Contains(t, err.Error(), "AUTOSCALING_MAX_SIZE", "Error should mention missing AUTOSCALING_MAX_SIZE")
}

// TestGroupKeyAndID_FallbackBehavior tests that GroupKeyAndID returns the GCP IGM
// IGM ID when present, and falls back to the ASG name otherwise.
func TestGroupKeyAndID_FallbackBehavior(t *testing.T) {
	t.Run("Returns GCP IGM ID when GCPIGMID is set", func(t *testing.T) {
		cfg := RuntimeConfig{
			GCPIGMID: "projects/my-project/zones/us-central1-a/instanceGroupManagers/my-mig",
		}
		key, id := cfg.GroupKeyAndID()
		assert.Equal(t, "igm_id", key)
		assert.Equal(t, "projects/my-project/zones/us-central1-a/instanceGroupManagers/my-mig", id)
	})

	t.Run("Falls back to Azure when GCPIGMID is empty but AzureVMSSResourceID is set", func(t *testing.T) {
		cfg := RuntimeConfig{
			GCPIGMID:            "", // empty
			AzureVMSSResourceID: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/vmss",
		}
		key, id := cfg.GroupKeyAndID()
		assert.Equal(t, "vmss_resource_id", key)
		assert.Equal(t, "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.Compute/virtualMachineScaleSets/vmss", id)
	})

	t.Run("Falls back to AWS when both GCP and Azure IDs are empty", func(t *testing.T) {
		cfg := RuntimeConfig{
			GCPIGMID:            "", // empty
			AzureVMSSResourceID: "", // empty
			AutoscalingGroupARN: "arn:aws:autoscaling:us-east-1:123456789:autoScalingGroup:group-id:autoScalingGroupName/my-asg",
		}
		key, id := cfg.GroupKeyAndID()
		assert.Equal(t, "asg_arn", key)
		assert.Equal(t, "arn:aws:autoscaling:us-east-1:123456789:autoScalingGroup:group-id:autoScalingGroupName/my-asg", id)
	})
}
