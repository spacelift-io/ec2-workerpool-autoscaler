data "external" "package" {
  program = [
    "${path.module}/download.sh",
    local.code_version,
    local.package_path,
  ]
}

resource "aws_lambda_function" "test_lambda" {
  filename         = local.package_path
  source_code_hash = data.external.package.result.source_code_hash
  function_name    = "ec2-autoscaler-${var.worker_pool_id}"
  role             = aws_iam_role.lambda.arn
  handler          = "ec2-workerpool-autoscaler_v${var.autoscaler_version}"

  runtime = "go1.x"

  environment {
    variables = {
      AUTOSCALING_GROUP_ARN         = var.autoscaling_group_arn
      AUTOSCALING_REGION            = data.aws_region.current.name
      SPACELIFT_API_KEY_ID          = var.spacelift_api_key_id
      SPACELIFT_API_KEY_SECRET_NAME = aws_ssm_parameter.spacelift_api_key_secret.name
      SPACELIFT_API_KEY_ENDPOINT    = var.spacelift_url
      SPACELIFT_WORKER_POOL_ID      = var.worker_pool_id
    }
  }

  tracing_config {
    mode = "Active"
  }
}
