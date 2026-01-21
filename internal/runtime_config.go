package internal

type RuntimeConfig struct {
	SpaceliftAPIKeyID      string `env:"SPACELIFT_API_KEY_ID,notEmpty"`
	SpaceliftAPISecretName string `env:"SPACELIFT_API_KEY_SECRET_NAME,notEmpty"`
	SpaceliftAPIEndpoint   string `env:"SPACELIFT_API_KEY_ENDPOINT,notEmpty"`
	SpaceliftWorkerPoolID  string `env:"SPACELIFT_WORKER_POOL_ID,notEmpty"`

	AutoscalingScaleDownDelay      int    `env:"AUTOSCALING_SCALE_DOWN_DELAY" envDefault:"0"`
	AutoscalingGroupARN            string `env:"AUTOSCALING_GROUP_ARN,notEmpty"`
	AutoscalingRegion              string `env:"AUTOSCALING_REGION,notEmpty"`
	AutoscalingMaxKill             int    `env:"AUTOSCALING_MAX_KILL" envDefault:"1"`
	AutoscalingMaxCreate           int    `env:"AUTOSCALING_MAX_CREATE" envDefault:"1"`
	AutoscalingCapacitySanityCheck int    `env:"AUTOSCALING_CAPACITY_SANITY_CHECK" envDefault:"10"`

	// Azure-specific configuration for Key Vault
	AzureKeyVaultName string `env:"AZURE_KEY_VAULT_NAME"`
	AzureSecretName   string `env:"AZURE_SECRET_NAME"`

	// Azure-specific autoscaling limits
	AzureAutoscalingMinSize int `env:"AZURE_AUTOSCALING_MIN_SIZE" envDefault:"-1"`
	AzureAutoscalingMaxSize int `env:"AZURE_AUTOSCALING_MAX_SIZE"`
}

func (r RuntimeConfig) GroupKeyAndID() (string, string) {
	return "asg_arn", r.AutoscalingGroupARN
}
