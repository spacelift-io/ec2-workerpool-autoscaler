variable "autoscaling_group_arn" {
  type        = string
  description = "ARN of the Spacelift worker pool's autoscaling group"
}

variable "autoscaler_version" {
  type        = string
  description = "Version of the autoscaler to deploy"
}

variable "autoscaling_frequency" {
  type        = number
  description = "How often to run the autoscaler, in minutes"
  default     = 5
}

variable "autoscaling_max_create" {
  type        = number
  description = "Max number of instances to create during a single execution of the autoscaler"
  default     = 1
}

variable "autoscaling_max_kill" {
  type        = number
  description = "Max number of instances to kill during a single execution of the autoscaler"
  default     = 1
}

variable "spacelift_api_key_id" {
  type        = string
  description = "ID of the Spacelift API key to use"
}

variable "spacelift_api_key_secret" {
  type        = string
  sensitive   = true
  description = "Secret corresponding to the Spacelift API key to use"
}

variable "spacelift_url" {
  type        = string
  description = "Full URL of the Spacelift API endpoint to use, eg. https://demo.app.spacelift.io"
}

variable "worker_pool_id" {
  type        = string
  description = "ID of the Spacelift worker pool to autoscale"
}
