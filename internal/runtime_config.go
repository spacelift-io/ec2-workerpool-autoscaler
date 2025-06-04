package internal

type RuntimeConfig struct {
	SpaceliftAPIKeyID      string `env:"SPACELIFT_API_KEY_ID,notEmpty"`
	SpaceliftAPISecretName string `env:"SPACELIFT_API_KEY_SECRET_NAME,notEmpty"`
	SpaceliftAPIEndpoint   string `env:"SPACELIFT_API_KEY_ENDPOINT,notEmpty"`
	SpaceliftWorkerPoolID  string `env:"SPACELIFT_WORKER_POOL_ID,notEmpty"`

	AutoscalingScaleDownDelay int    `env:"AUTOSCALING_SCALE_DOWN_DELAY" envDefault:"0"`
	AutoscalingGroupARN       string `env:"AUTOSCALING_GROUP_ARN,notEmpty"`
	AutoscalingRegion         string `env:"AUTOSCALING_REGION,notEmpty"`
	AutoscalingMaxKill        int    `env:"AUTOSCALING_MAX_KILL" envDefault:"1"`
	AutoscalingMaxCreate      int    `env:"AUTOSCALING_MAX_CREATE" envDefault:"1"`
}
