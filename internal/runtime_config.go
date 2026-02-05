package internal

import "github.com/caarlos0/env/v9"

// Platform represents the cloud platform being used.
type Platform string

const (
	PlatformAWS   Platform = "aws"
	PlatformAzure Platform = "azure"
)

type RuntimeConfig struct {
	// Common fields - used by all platforms
	SpaceliftAPIKeyID      string `env:"SPACELIFT_API_KEY_ID,notEmpty"`
	SpaceliftAPISecretName string `env:"SPACELIFT_API_KEY_SECRET_NAME,notEmpty"`
	SpaceliftAPIEndpoint   string `env:"SPACELIFT_API_KEY_ENDPOINT,notEmpty"`
	SpaceliftWorkerPoolID  string `env:"SPACELIFT_WORKER_POOL_ID,notEmpty"`

	// Shared autoscaling fields (used by both platforms)
	AutoscalingScaleDownDelay      int `env:"AUTOSCALING_SCALE_DOWN_DELAY" envDefault:"0"`
	AutoscalingMaxKill             int `env:"AUTOSCALING_MAX_KILL" envDefault:"1"`
	AutoscalingMaxCreate           int `env:"AUTOSCALING_MAX_CREATE" envDefault:"1"`
	AutoscalingCapacitySanityCheck int `env:"AUTOSCALING_CAPACITY_SANITY_CHECK" envDefault:"10"`

	// AWS-specific fields - use awsEnv tag
	AutoscalingGroupARN string `awsEnv:"AUTOSCALING_GROUP_ARN,notEmpty"`
	AutoscalingRegion   string `awsEnv:"AUTOSCALING_REGION,notEmpty"`

	// Min/Max size fields (Azure, GCP, etc.) - use minMaxEnv tag
	// AWS ASG has built-in min/max; other platforms need these from env vars
	AutoscalingMinSize uint `minMaxEnv:"AUTOSCALING_MIN_SIZE" envDefault:"0"`
	AutoscalingMaxSize uint `minMaxEnv:"AUTOSCALING_MAX_SIZE,notEmpty"`

	// Azure-specific fields - use azEnv tag
	AzureVMSSResourceID string `azEnv:"AZURE_VMSS_RESOURCE_ID,notEmpty"`
	AzureKeyVaultName   string `azEnv:"AZURE_KEY_VAULT_NAME,notEmpty"`
}

// Parse parses environment variables into the config for the specified platform.
func (r *RuntimeConfig) Parse(platform Platform) error {
	var allErrors env.AggregateError

	// Common fields for all platforms
	tags := []string{"env"}

	// Add platform-specific tags
	switch platform {
	case PlatformAWS:
		tags = append(tags, "awsEnv")
	case PlatformAzure:
		tags = append(tags, "azEnv", "minMaxEnv")
	}

	for _, tag := range tags {
		if err := env.ParseWithOptions(r, env.Options{TagName: tag}); err != nil {
			if aggErr, ok := err.(env.AggregateError); ok {
				allErrors.Errors = append(allErrors.Errors, aggErr.Errors...)
			} else {
				allErrors.Errors = append(allErrors.Errors, err)
			}
		}
	}

	if len(allErrors.Errors) > 0 {
		return allErrors
	}
	return nil
}

// GroupKeyAndID returns the platform-appropriate log key and resource ID.
func (r RuntimeConfig) GroupKeyAndID() (string, string) {
	if r.AzureVMSSResourceID != "" {
		return "vmss_resource_id", r.AzureVMSSResourceID
	}
	return "asg_arn", r.AutoscalingGroupARN
}
