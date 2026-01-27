# Spacelift worker pool autoscaler

This utility is designed to be executed periodically for a single combination of a Spacelift worker pool and a cloud provider's autoscaling group which provides the worker pool with workers.

## Supported Cloud Providers

This autoscaler supports both AWS EC2 Auto Scaling Groups and Azure Virtual Machine Scale Sets (VMSS). Choose the section below that matches your infrastructure:

- [AWS EC2 Auto Scaling Groups](#aws-setup)
- [Azure Virtual Machine Scale Sets](#azure-setup)

## Important note on concurrency

This utility is designed to be executed periodically, so running multiple instances in parallel or even running one instance in short intervals is not recommended and may lead to unexpected results. A Lambda function or Azure Function with a 5-minute interval and max concurrency of 1 is a good starting point.

## Autoscaling logic

The utility is designed to be executed periodically. Each execution performs the following steps:

1. Retrieve the secret containing the Spacelift API key from the cloud provider's secret store (AWS Secrets Manager / SSM Parameter Store or Azure Key Vault);

1. Establish a session with the Spacelift API using the API key;

1. Retrieve the current number of instances in the auto-scaling group;

1. Get the data about the worker pool from the Spacelift API:

    - the number of schedulable runs;

    - the number of currently running workers (excluding drained workers);

1. Get the data about the autoscaling group;

1. Check for the presence of "stray" machines. Stray machines are instances that are not registered with the Spacelift API as workers, but are registered with the auto-scaling group. There are two main reasons for this: either the machine has just been provisioned and is not yet registered with the Spacelift API, or the machine is malfunctioning in one way or another. We approximate the cause by looking at the machine creation timestamp - anything older than 10 minutes and not registered with the Spacelift API is considered a stray machine.

1. Terminate a **single** stray machine if some are found. If the termination occurred, the utility exits at this point. This is to prevent the malfunctioning utility from terminating multiple machines in a single execution. Stray machines are in practice not a common occurrence and it's safer to let the utility run again in a few minutes than to let the utility go berserk and possibly cause an outage. Note that the reason why we terminate machines here is that the autoscaler only works well with a stable state where there is a 100% correspondence between physical (cloud provider) and logical (Spacelift) nodes.

1. Ensure that all Spacelift workers are "live", that is they correspond to an instance in the auto-scaling group. A Spacelift logical worker can take a while to be considered dead if it does not terminate cleanly (eg. an OOM), so again we want to ensure that the state is stable before proceeding. If the number of workers is different than the number of instances in the auto-scaling group, the utility exits at this point with no scaling decision. No action needs to be taken at this point because Spacelift is eventually going to clean up the dead workers and the autoscaler will be able to make a decision on one of the subsequent runs.

1. Look at the following numbers to reach a scaling decision:

    - the number of schedulable runs;

    - the number of idle Spacelift workers;

    - the number of active instances in the auto-scaling group;

    - the minimum and maximum size of the auto-scaling group;

    - the configured minimum and maximum number of workers that can be created or destroyed during one run (see the `AUTOSCALING_MAX_CREATE` and `AUTOSCALING_MAX_KILL` environment variables, respectively);

    If there are more idle workers than schedulable runs, the utility starts a scale-down utility, taking into account the max number of killable instances, and the minimum size of the autoscaling group. Spacelift schedules jobs on the newest available workers, so we generally want to kill oldest ones first, because they're least likely to have a new job scheduled on them.

    A single safe scale-down operation for a worker involves the following steps:

    1. Drain the worker by calling the `workerDrainSet` mutation with `drain` parameter set to `true`;

    1. Based on the response from the Spacelift API, see if the worker reports as busy. If it does, it means that between the time of the original worker pool query and the time of the drain request, a new job has been scheduled on the worker. Since this is the oldest available worker, we can assume with a high degree of certainty that newer workers are also busy, so we undrain the worker and exit the scale-down operation. If the worker does not report as busy, we proceed to the next step;

    1. Detach the instance from the auto-scaling group with decrementing the desired capacity (AWS) or delete the instance which automatically decrements capacity (Azure);

    1. Terminate the instance;

    If there are more schedulable runs than idle workers, we attempt to provision the capacity, constrained by the max number of creatable instances and the maximum size of the auto-scaling group.

## AWS Setup

The AWS autoscaler works with EC2 Auto Scaling Groups. In the majority of cases, such an autoscaling group is provisioned using [this Terraform module](https://github.com/spacelift-io/terraform-aws-spacelift-workerpool-on-ec2).

### Execution Modes

- **local** (the `cmd/local` binary) - in this mode, the utility runs as a standalone process in an arbitrary environment;

- **lambda** (the `cmd/lambda` binary) - in this mode, the utility is designed to be periodically executed as an AWS Lambda function;

While the Lambda release artifacts are versioned and available as GitHub releases, the users of the local binary are encouraged to build it themselves for the system and architecture they're running it on.

### AWS Environment Variables

The utility requires the following environment variables to be set:

- `AUTOSCALING_GROUP_ARN` - the ARN of the EC2 auto-scaling group to scale;
- `AUTOSCALING_REGION` - the AWS region the auto-scaling group is in;
- `SPACELIFT_API_KEY_ID` - the ID of the Spacelift [API key](https://docs.spacelift.io/integrations/api#spacelift-api-key-token) to use for authentication;
- `SPACELIFT_API_KEY_SECRET_NAME` - the name of the AWS Secrets Manager secret or SSM Parameter Store parameter containing the Spacelift API key secret;
- `SPACELIFT_API_KEY_ENDPOINT` - the URL of the Spacelift API endpoint to use (eg. `https://demo.app.spacelift.io`);
- `SPACELIFT_WORKER_POOL_ID` - the ID of the Spacelift worker pool to scale;

Three additional environment variables are optional, but very useful if you're running at a non-trivial scale:

- `AUTOSCALING_MAX_KILL` (defaults to 1) - the maximum number of instances the utility is allowed to terminate in a single run;
- `AUTOSCALING_MAX_CREATE` (defaults to 1) - the maximum number of instances the utility is allowed to create in a single run;
- `AUTOSCALING_SCALE_DOWN_DELAY` (defaults to 0) - the number of minutes a worker must be registered to Spacelift before its eligible to be scaled in.

### AWS IAM Permissions

The utility requires the following AWS permissions to be granted to the IAM role or user it's running as:

- `autoscaling:DescribeAutoScalingGroups` on the target autoscaling group to retrieve the current number of instances in the auto-scaling group;
- `autoscaling:DetachInstances` on the target autoscaling group to detach instances from the auto-scaling group;
- `autoscaling:SetDesiredCapacity` on the target autoscaling group to set the desired capacity of the auto-scaling group;
- `ec2:DescribeInstances` in the region the autoscaling group is in to retrieve the instance IDs of the instances to terminate;
- `ec2:TerminateInstances` in the region the autoscaling group is in to terminate the instances;
- `ssm:GetParameter` on the SSM Parameter Store parameter storing the Spacelift API key secret (or use Secrets Manager permissions if using Secrets Manager);

The Spacelift API key needs to have:

- `Space:Read` for the Space the worker exists in
- `Worker Pool:Drain Worker`

### AWS Observability

The utility logs its actions to the standard output. The logs are formatted as JSON objects. It also emits traces to X-Ray if the X-Ray daemon is reachable at port 2000 on the local host. Note that the Lambda execution environment provides the X-Ray daemon out of the box, but the local execution environment does not. The IAM permissions required to emit traces to X-Ray are:

- `xray:PutTraceSegments` to send the trace segments to the X-Ray daemon;
- `xray:PutTelemetryRecords` to send the telemetry records to the X-Ray daemon;

## Azure Setup

The Azure autoscaler works with Azure Virtual Machine Scale Sets (VMSS). The autoscaler will automatically detect and prevent conflicts with Azure's native autoscaling feature - if you have Azure autoscaling enabled on your VMSS, the utility will fail with an error message asking you to disable it.

### Execution Mode

- **azurefunc** (the `cmd/azurefunc` binary) - designed to be executed as an Azure Function with a timer trigger;

The Azure Function release artifacts are versioned and available as GitHub releases.

### Azure Environment Variables

The utility requires the following environment variables to be set:

- `AZURE_VMSS_RESOURCE_ID` - the Azure Resource ID of the VMSS to scale (format: `/subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Compute/virtualMachineScaleSets/{vmssName}`);
- `AUTOSCALING_MAX_SIZE` (required) - the maximum number of VM instances the autoscaler can scale up to in the VMSS. Must be greater than 0;
- `SPACELIFT_API_KEY_ID` - the ID of the Spacelift [API key](https://docs.spacelift.io/integrations/api#spacelift-api-key-token) to use for authentication;
- `SPACELIFT_API_KEY_ENDPOINT` - the URL of the Spacelift API endpoint to use (eg. `https://demo.app.spacelift.io`);
- `SPACELIFT_WORKER_POOL_ID` - the ID of the Spacelift worker pool to scale;
- `AZURE_KEY_VAULT_NAME` - the name of the Azure Key Vault containing the Spacelift API key secret (just the name, not the full URL);
- `AZURE_SECRET_NAME` - the name of the secret in Azure Key Vault containing the Spacelift API key secret;

Additional optional environment variables:

- `AUTOSCALING_MAX_KILL` (defaults to 1) - the maximum number of VM instances the utility is allowed to terminate in a single run;
- `AUTOSCALING_MAX_CREATE` (defaults to 1) - the maximum number of VM instances the utility is allowed to create in a single run;
- `AUTOSCALING_SCALE_DOWN_DELAY` (defaults to 0) - the number of minutes a worker must be registered to Spacelift before its eligible to be scaled in;
- `AUTOSCALING_MIN_SIZE` (optional, defaults to 0) - the minimum number of VM instances the autoscaler should maintain in the VMSS;

### Azure Authentication

The autoscaler uses Azure's DefaultAzureCredential, which supports multiple authentication methods in the following order:

1. **Managed Identity** (recommended for Azure Functions) - automatically available when deployed to Azure
2. **Azure CLI** - uses credentials from `az login` (useful for local development)
3. **Environment variables** - `AZURE_CLIENT_ID`, `AZURE_TENANT_ID`, `AZURE_CLIENT_SECRET`

For production deployments in Azure Functions, it's recommended to use a Managed Identity.

### Azure RBAC Permissions

The Managed Identity or Service Principal used by the autoscaler needs the following Azure RBAC permissions:

#### On the Virtual Machine Scale Set

- `Microsoft.Compute/virtualMachineScaleSets/read` - to retrieve VMSS details and current capacity
- `Microsoft.Compute/virtualMachineScaleSets/write` - to update the VMSS capacity
- `Microsoft.Compute/virtualMachineScaleSets/virtualMachines/read` - to list and describe VM instances
- `Microsoft.Compute/virtualMachineScaleSets/virtualMachines/delete` - to terminate VM instances

These permissions are included in the built-in Azure role: **Virtual Machine Contributor** (scoped to the VMSS resource or resource group)

#### On the Azure Key Vault

- `Microsoft.KeyVault/vaults/secrets/getSecret/action` - to read the Spacelift API key secret

This permission is included in the built-in Azure role: **Key Vault Secrets User**

#### For Autoscale Conflict Detection

- `Microsoft.Insights/autoscalesettings/read` - to check if Azure autoscaling is enabled on the VMSS

This permission is included in the built-in Azure role: **Monitoring Reader** (scoped to the resource group)

### Recommended Azure Role Assignments

For a Managed Identity running the autoscaler, assign:

1. **Virtual Machine Contributor** role on the VMSS (or its resource group)
2. **Key Vault Secrets User** role on the Key Vault (or use Key Vault access policies)
3. **Monitoring Reader** role on the resource group containing the VMSS

### Azure Key Vault Setup

1. Create an Azure Key Vault or use an existing one
2. Store your Spacelift API key secret as a secret in the Key Vault
3. Grant the Managed Identity or Service Principal access to read secrets (using either RBAC or access policies)
4. Set the `AZURE_KEY_VAULT_NAME` and `AZURE_SECRET_NAME` environment variables

### Spacelift API Key Permissions

The Spacelift API key needs to have:

- `Space:Read` for the Space the worker exists in
- `Worker Pool:Drain Worker`

### Azure Observability

The utility logs its actions to the standard output. The logs are formatted as JSON objects and are automatically collected by Azure Functions logging. You can view logs in:

- Azure Portal (Function App → Functions → Your Function → Monitor)
- Application Insights (if configured)
- Log Analytics workspace (if configured)

The autoscaler also supports OpenTelemetry tracing, which can be configured to send traces to Azure Monitor or other OpenTelemetry-compatible backends.

### Azure-Specific Notes

- **No Native Autoscaling**: The autoscaler will check for and prevent conflicts with Azure's native autoscaling. If Azure autoscaling is enabled on the VMSS, the utility will exit with an error. You must disable Azure autoscaling to use this utility.
- **Capacity Management**: Unlike AWS ASG which has built-in min/max/desired capacity, Azure VMSS uses SKU capacity. The autoscaler treats the current SKU capacity as the desired capacity and uses `AUTOSCALING_MIN_SIZE` and `AUTOSCALING_MAX_SIZE` environment variables to control scaling bounds.
- **Instance Deletion**: When scaling down, Azure automatically adjusts the VMSS capacity when an instance is deleted, so there's no separate "detach" operation like in AWS.
