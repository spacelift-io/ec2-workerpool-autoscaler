locals {
  base_name      = var.base_name == null ? "sp5ft-${var.worker_pool_id}" : var.base_name
  function_name  = "${local.base_name}-ec2-autoscaler"
  use_s3_package = var.autoscaler_s3_package != null
}

resource "aws_ssm_parameter" "spacelift_api_key_secret" {
  name  = "/${local.function_name}/spacelift-api-secret-${var.worker_pool_id}"
  type  = "SecureString"
  value = var.spacelift_api_key_secret
}

resource "null_resource" "download" {
  triggers = {
    # Always re-download the archive file
    now = timestamp()
  }
  provisioner "local-exec" {
    command = "${path.module}/download.sh ${var.autoscaler_version} ${var.autoscaler_architecture}"
  }
}

data "archive_file" "binary" {
  type        = "zip"
  source_file = "lambda/bootstrap"
  output_path = "ec2-workerpool-autoscaler_${var.autoscaler_version}.zip"
  depends_on  = [null_resource.download]
}

resource "aws_lambda_function" "autoscaler" {
  filename         = !local.use_s3_package ? data.archive_file.binary.output_path : null
  source_code_hash = !local.use_s3_package ? data.archive_file.binary.output_base64sha256 : null

  s3_bucket         = local.use_s3_package ? var.autoscaler_s3_package.bucket : null
  s3_key            = local.use_s3_package ? var.autoscaler_s3_package.key : null
  s3_object_version = local.use_s3_package ? var.autoscaler_s3_package.object_version : null

  function_name = local.function_name
  role          = aws_iam_role.autoscaler.arn
  handler       = "bootstrap"
  runtime       = "provided.al2"
  architectures = [var.autoscaler_architecture == "amd64" ? "x86_64" : var.autoscaler_architecture]
  timeout       = var.autoscaling_timeout

  vpc_config {
    subnet_ids                  = null
    security_group_ids          = null
    ipv6_allowed_for_dual_stack = null
  }

  environment {
    variables = {
      AUTOSCALING_GROUP_ARN         = var.autoscaling_group_arn
      AUTOSCALING_REGION            = data.aws_region.current.name
      SPACELIFT_API_KEY_ID          = var.spacelift_api_key_id
      SPACELIFT_API_KEY_SECRET_NAME = aws_ssm_parameter.spacelift_api_key_secret.name
      SPACELIFT_API_KEY_ENDPOINT    = var.spacelift_api_key_endpoint
      SPACELIFT_WORKER_POOL_ID      = var.worker_pool_id
      AUTOSCALING_MAX_CREATE        = var.autoscaling_max_create
      AUTOSCALING_MAX_KILL          = var.autoscaling_max_terminate
    }
  }

  tracing_config {
    mode = "Active"
  }
}

resource "aws_cloudwatch_event_rule" "scheduling" {
  name                = local.function_name
  description         = "Spacelift autoscaler scheduling for worker pool ${var.worker_pool_id}"
  schedule_expression = var.schedule_expression
}

resource "aws_cloudwatch_event_target" "scheduling" {
  rule = aws_cloudwatch_event_rule.scheduling.name
  arn  = aws_lambda_function.autoscaler.arn
}

resource "aws_lambda_permission" "allow_cloudwatch_to_call_lambda" {
  statement_id  = "AllowExecutionFromCloudWatch"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.autoscaler.function_name
  principal     = "events.amazonaws.com"
  source_arn    = aws_cloudwatch_event_rule.scheduling.arn
}

resource "aws_cloudwatch_log_group" "log_group" {
  name              = "/aws/lambda/${local.function_name}"
  retention_in_days = 7
}