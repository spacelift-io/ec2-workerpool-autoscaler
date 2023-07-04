package internal

type RuntimeConfig struct {
	SpaceliftAPIKeyID     string `env:"SPACELIFT_API_KEY_ID,notEmpty"`
	SpaceliftAPISecret    string `env:"SPACELIFT_API_SECRET,notEmpty"`
	SpaceliftAPIEndpoint  string `env:"SPACELIFT_API_ENDPOINT,notEmpty"`
	SpaceliftWorkerPoolID string `env:"SPACELIFT_WORKER_POOL_ID,notEmpty"`

	AutoscalingGroupName string `env:"AUTOSCALING_GROUP_NAME,notEmpty"`
	AutoscalingRegion    string `env:"AUTOSCALING_REGION,notEmpty"`
	AutoscalingMaxKill   int    `env:"AUTOSCALING_MAX_KILL" envDefault:"1"`
	AutoscalingMaxCreate int    `env:"AUTOSCALING_MAX_CREATE" envDefault:"1"`
}
