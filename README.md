# Spacelift EC2 worker pool autoscaler

This small utility is designed to be executed periodically for a single combination of a Spacelift worker pool and an EC2 auto-scaling group which provides the worker pool with workers. In the majority of cases, such an autoscaling group is provisioned using [this Terraform module](https://github.com/spacelift-io/terraform-aws-spacelift-workerpool-on-ec2).

It can be executed in two modes:

- *local* (the `cmd/local` binary) - in this mode, the utility runs as a standalone process in an arbitrary environment;

- *lambda* (the `cmd/lambda` binary) - in this mode, the utility is designed to be periodically executed as an AWS Lambda function;

While the Lambda release artifacts are versioned and available as GitHub releases, the users of the local binary are encouraged to build it themselves for the system and architecture they're running it on.

## Setup

The utility requires the following environment variables to be set:

- `AUTOSCALING_GROUP_ARN` - the ARN of the EC2 auto-scaling group to scale;
- `AUTOSCALING_REGION` - the AWS region the auto-scaling group is in;
- `SPACELIFT_API_KEY_ID` - the ID of the Spacelift [API key](https://docs.spacelift.io/integrations/api#spacelift-api-key-token) to use for authentication;
- `SPACELIFT_API_KEY_SECRET_NAME` - the name of the AWS Secrets Manager secret containing the Spacelift API key secret;
- `SPACELIFT_API_KEY_ENDPOINT` - the URL of the Spacelift API endpoint to use (eg. to `https://demo.app.spacelift.io`);
- `SPACELIFT_WORKER_POOL_ID` - the ID of the Spacelift worker pool to scale;

Two additional environment variables are optional, but very useful if you're running at a non-trivial scale:

- `AUTOSCALING_MAX_KILL` (defaults to 1) - the maximum number of instances the utility is allowed to terminate in a single run;
- `AUTOSCALING_MAX_CREATE` (defaults to 1) - the maximum number of instances the utility is allowed to create in a single run;
- `AUTOSCALING_SCALE_DOWN_DELAY` (defaults to 0) - the number of minutes a worker must be registered to Spacelift before its eligible to be scaled in.

## Important note on concurrency

This utility is designed to be executed periodically, so running multiple instances in parallel or even running one instance in short intervals is not recommended and may lead to unexpected results. A Lambda function with a 5-minute interval and max concurrency of 1 is a good starting point.

## Required IAM permissions

The utility requires the following AWS permissions to be granted to the IAM role or user it's running as:

- `autoscaling:DescribeAutoScalingGroups` on the target autoscaling group to retrieve the current number of instances in the auto-scaling group;
- `autoscaling:DetachInstances` on the target autoscaling group to detach instances from the auto-scaling group;
- `autoscaling:SetDesiredCapacity` on the target autoscaling group to set the desired capacity of the auto-scaling group;
- `ec2:DescribeInstances` in the region the autoscaling group is in to retrieve the instance IDs of the instances to terminate;
- `ec2:TerminateInstances` in the region the autoscaling group is in to terminate the instances;
- `ssm:GetParameter` on the SSM Parameter Store parameter storing the Spacelift API key secret;

The Spacelift API key needs to have administrator privileges for the [space](https://docs.spacelift.io/concepts/spaces/) where the worker pool is defined.

## Observability

The utility logs its actions to the standard output. The logs are formatted as JSON objects. It also emits traces to X-Ray if the X-Ray daemon is reachable at port 2000 on the local host. Note that the Lambda execution environment provides the X-Ray daemon out of the box, but the local execution environment does not. The IAM permissions required to emit traces to X-Ray are:

- `xray:PutTraceSegments` to send the trace segments to the X-Ray daemon;
- `xray:PutTelemetryRecords` to send the telemetry records to the X-Ray daemon;

## Autoscaling logic

The utility is designed to be executed periodically. Each execution performs the following steps:

1. Retrieve the secret containing the Spacelift API key from the SSM Parameter store;

1. Establish a session with the Spacelift API using the API key;

1. Retrieve the current number of instances in the auto-scaling group;

1. Get the data about the worker pool from the Spacelift API:

    - the number of schedulable runs;

    - the number of currently running workers;

1. Get the data about the autoscaling group;

1. Check for the presence of "stray" machines. Stray machines are instances that are not registered with the Spacelift API as workers, but are registered with the auto-scaling group. There are two main reasons for this: either the machine has just been provisioned and is not yet registered with the Spacelift API, or the machine is malfunctioning in one way or another. We approximate the cause by looking at the machine creation timestamp - anything older than 10 minutes and not registered with the Spacelift API is considered a stray machine.

1. Terminate a **single** stray machine if some are found. If the termination occurred, the utility exits at this point. This is to prevent the malfunctioning utility from terminating multiple machines in a single execution. Stray machines are in practice not a common occurrence and it's safer to let the utility run again in a few minutes than to let the utility go berserk and possibly cause an outage. Note that the reason why we terminate machines here is that the autoscaler only works well with a stable state where there is a 100% correspondence between physical (AWS) and logical (Spacelift) nodes.

1. Ensure that all Spacelift workers are "live", that is they correspond to an instance in the auto-scaling group. A Spacelift logical worker can take a while to be considered dead if it does not terminate cleanly (eg. an OOM), so again we want to ensure that the state is stable before proceeding. If the number of workers is different than the number of instances in the auto-scaling group, the utility exits at this point with no scaling decision. No action needs to be taken at this point because Spacelift is eventually going to clean up the dead workers and the autoscaler will be able to make a decision on one of the subsequent runs.

1. Look at the following numbers to reach a scaling decision:

    - the number of schedulable runs;

    - the number of idle Spacelift workers;

    - the number of active EC2 instances in the auto-scaling group;

    - the minimum and maximum size of the auto-scaling group;

    - the configured minimum and maximum number of workers that can be created or destroyed during one run (see the `AUTOSCALING_MAX_CREATE` and `AUTOSCALING_MAX_KILL` environment variables, respectively);

    If there are more idle workers than schedulable runs, the utility starts a scale-down utility, taking into account the max number of killable instances, and the minimum size of the autoscaling group. Spacelift schedules jobs on the newest available workers, so we generally want to kill oldest ones first, because they're least likely to have a new job scheduled on them.

    A single safe scale-down operation for a worker involves the following steps:

    1. Drain the worker by calling the `workerDrainSet` mutation with `drain` parameter set to `true`;

    1. Based on the response from the Spacelift API, see if the worker reports as busy. If it does, it means that between the time of the original worker pool query and the time of the drain request, a new job has been scheduled on the worker. Since this is the oldest available worker, we can assume with a high degree of certainty that newer workers are also busy, so we undrain the worker and exit the scale-down operation. If the worker does not report as busy, we proceed to the next step;

    1. Detach the instance from the auto-scaling group with decrementing the desired capacity;

    1. Terminate the instance;

    If there are more schedulable runs than idle workers, we attempt to provision the capacity, constrained by the max number of creatable instances and the maximum size of the auto-scaling group.
