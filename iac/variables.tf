variable "autoscaling_group_arn" {
  type        = string
  description = "ARN of the Spacelift worker pool's autoscaling group"
}

variable "autoscaler_version" {
  type        = string
  description = "Version of the autoscaler to deploy"
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
