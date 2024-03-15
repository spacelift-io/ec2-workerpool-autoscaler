variable "autoscaling_group_arn" {
  type        = string
  description = "ARN of the Spacelift worker pool's autoscaling group"
}

variable "autoscaler_version" {
  type        = string
  description = "Version of the autoscaler to deploy"
  default     = "v0.3.0"
}

variable "autoscaling_frequency" {
  type        = number
  description = "How often to run the autoscaler, in minutes"
  default     = 5
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

variable "spacelift_api_key_endpoint" {
  type        = string
  description = "Full URL of the Spacelift API endpoint to use, eg. https://demo.app.spacelift.io"
  default     = null
}

variable "worker_pool_id" {
  type        = string
  description = "ID of the Spacelift worker pool to autoscale"
}

variable "autoscaler_architecture" {
  type        = string
  description = "Instruction set architecture of the autoscaler to use"
  default     = "amd64"
}

variable "autoscaling_timeout" {
  type        = number
  description = "Timeout (in seconds) for a single autoscaling run. The more instances you have, the higher this should be."
  default     = 30
}

variable "autoscaling_max_create" {
  description = "The maximum number of instances the utility is allowed to create in a single run"
  type        = number
  default     = 1
}

variable "autoscaling_max_terminate" {
  description = "The maximum number of instances the utility is allowed to terminate in a single run"
  type        = number
  default     = 1
}

variable "schedule_expression" {
  type        = string
  description = "Autoscaler scheduling expression"
  default     = "rate(1 minute)"
}

variable "base_name" {
  type        = string
  description = "Base name for resources. If unset, it defaults to `sp5ft-$${var.worker_pool_id}`."
  nullable    = true
  default     = null
}

variable "region" {
  type        = string
  description = "AWS Region where the provider will operate"
}

variable "autoscaler_s3_package" {
  type = object({
    bucket = string
    key    = string
    # object_version = optional(string)
  })
  description = "Configuration to retrieve autoscaler lambda package from s3 bucket"
}