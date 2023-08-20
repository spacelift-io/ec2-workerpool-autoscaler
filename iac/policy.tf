data "aws_iam_policy_document" "lambda_policy" {
  # Allow the Lambda to write CloudWatch Logs.
  statement {
    effect = "Allow"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
    ]

    resources = ["${aws_cloudwatch_log_group.log_group.arn}/*"]
  }

  # Allow the Lambda to put X-Ray traces.
  statement {
    effect = "Allow"
    actions = [
      "xray:PutTraceSegments",
      "xray:PutTelemetryRecords",
    ]

    resources = ["*"]
  }

  # Allow the Lambda to DescribeAutoScalingGroups, DetachInstances and SetDesiredCapacity
  # on the AutoScalingGroup.
  statement {
    effect = "Allow"
    actions = [
      "autoscaling:DetachInstances",
      "autoscaling:SetDesiredCapacity",
    ]

    resources = [var.autoscaling_group_arn]
  }

  statement {
    effect    = "Allow"
    actions   = ["autoscaling:DescribeAutoScalingGroups"]
    resources = ["*"]
  }

  # Allow the Lambda to DescribeInstances and TerminateInstances on the EC2 instances.
  statement {
    effect = "Allow"
    actions = [
      "ec2:DescribeInstances",
      "ec2:TerminateInstances",
    ]

    resources = ["*"]
  }

  # Allow the Lambda to read the secret from SSM Parameter Store.
  statement {
    effect    = "Allow"
    actions   = ["ssm:GetParameter"]
    resources = [aws_ssm_parameter.spacelift_api_key_secret.arn]
  }
}

resource "aws_iam_role" "lambda" {
  name               = "ec2-autoscaler-${var.worker_pool_id}"
  assume_role_policy = data.aws_iam_policy_document.assume_lambda_role.json

  inline_policy {
    name   = "ec2-autoscaler-${var.worker_pool_id}"
    policy = data.aws_iam_policy_document.lambda_policy.json
  }
}
