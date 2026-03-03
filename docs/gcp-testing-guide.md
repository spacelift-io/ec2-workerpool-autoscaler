# GCP Autoscaler Testing Guide

This guide walks through testing the GCP Managed Instance Group autoscaler locally.

## Prerequisites

### Required Permissions

Your GCP identity (user or service account) needs:

1. **Secret Manager Secret Accessor** - on the secret containing the Spacelift API key
2. **Compute Viewer** - to read IGM and instance details
3. **Compute Instance Admin (v1)** - to delete instances (for scale-down testing)

```bash
# Grant permissions (adjust project/member as needed)
gcloud secrets add-iam-policy-binding SECRET_NAME \
  --project=PROJECT_ID \
  --member="user:YOUR_EMAIL" \
  --role="roles/secretmanager.secretAccessor"

gcloud projects add-iam-policy-binding PROJECT_ID \
  --member="user:YOUR_EMAIL" \
  --role="roles/compute.instanceAdmin.v1"
```

### Spacelift Worker Pool Requirements

For the autoscaler to match instances to workers, Spacelift workers must have metadata:
- `gcp_igm_id`: The IGM ID (resource path)
- `gcp_instance_id`: The instance ID (resource path)

This metadata is set by the worker launcher configuration in Spacelift.

---

## Configuration

Create a `.env.test` file (already gitignored):

```bash
# GCP IGM Configuration
# Format: projects/{project}/zones/{zone}/instanceGroupManagers/{name}
GCP_IGM_ID=projects/YOUR_PROJECT/zones/YOUR_ZONE/instanceGroupManagers/YOUR_IGM

# Spacelift API Configuration
SPACELIFT_API_KEY_ENDPOINT=https://yourorg.app.spacelift.io
SPACELIFT_API_KEY_ID=YOUR_API_KEY_ID
SPACELIFT_API_KEY_SECRET_NAME=projects/PROJECT_NUMBER/secrets/SECRET_NAME/versions/VERSION
SPACELIFT_WORKER_POOL_ID=YOUR_WORKER_POOL_ID

# Autoscaling Limits
AUTOSCALING_MAX_SIZE=5
AUTOSCALING_MIN_SIZE=0
AUTOSCALING_MAX_CREATE=1
AUTOSCALING_MAX_KILL=1
```

---

## Testing Steps

### Step 1: Verify Prerequisites

```bash
# Check gcloud ADC is configured
gcloud auth application-default print-access-token > /dev/null && echo "ADC: OK" || echo "ADC: FAILED"

# Build the binary
go build -o /tmp/autoscaler-local ./cmd/local/
```

### Step 2: Test GCP Connectivity

```bash
# Source the config
source .env.test

# Test Secret Manager access (no output = success)
gcloud secrets versions access ${SPACELIFT_API_KEY_SECRET_NAME##*/versions/} \
  --secret=${SPACELIFT_API_KEY_SECRET_NAME%/versions/*} \
  --project=${SPACELIFT_API_KEY_SECRET_NAME#projects/} 2>&1 | head -c1 > /dev/null && echo "Secret Manager: OK" || echo "Secret Manager: FAILED"

# Extract IGM details from GCP_IGM_ID
IGM_PROJECT=$(echo $GCP_IGM_ID | cut -d'/' -f2)
IGM_ZONE=$(echo $GCP_IGM_ID | cut -d'/' -f4)
IGM_NAME=$(echo $GCP_IGM_ID | cut -d'/' -f6)

# Test IGM access
gcloud compute instance-groups managed describe $IGM_NAME \
  --zone=$IGM_ZONE --project=$IGM_PROJECT \
  --format="table(name,targetSize,status.isStable)"

# List instances
gcloud compute instance-groups managed list-instances $IGM_NAME \
  --zone=$IGM_ZONE --project=$IGM_PROJECT \
  --format="table(instance,status,currentAction)"
```

### Step 3: Run Autoscaler Locally

```bash
# Source config and run the autoscaler
set -a && source .env.test && set +a
/tmp/autoscaler-local
```

The local command runs the autoscaler once and exits — no server, no curl needed.
Logs confirm the platform: `"Detected GCP platform"` (triggered by `GCP_IGM_ID` env var).

### Step 4: View Logs

The autoscaler outputs JSON logs to stdout. Key log messages:

| Message | Meaning |
|---------|---------|
| `Detected GCP platform` | GCP platform selected (via `GCP_IGM_ID` env var) |
| `not scaling the ASG` | Stable state, no action needed |
| `scaling the ASG` | Scaling action taken |
| `worker has invalid metadata` | Worker missing gcp_igm_id/gcp_instance_id |
| `instance has no corresponding worker` | Stray instance detected |
| `could not kill stray instance` | Permission error on delete |

---

## Test Scenarios

### Scenario A: Read-Only Verification

Verify the autoscaler can read state without making changes:

```bash
set -a && source .env.test && set +a
export AUTOSCALING_MAX_CREATE=0 AUTOSCALING_MAX_KILL=0
/tmp/autoscaler-local
```

Verify logs show correct instance and worker counts.

### Scenario B: Scale-Up Test

1. Queue runs in Spacelift to create pending work
2. Run the autoscaler with scale-up enabled:

```bash
set -a && source .env.test && set +a
export AUTOSCALING_MAX_CREATE=1 AUTOSCALING_MAX_KILL=0
/tmp/autoscaler-local
```

3. Verify IGM target size increases

```bash
# Check IGM size before and after
gcloud compute instance-groups managed describe $IGM_NAME \
  --zone=$IGM_ZONE --project=$IGM_PROJECT \
  --format="value(targetSize)"
```

### Scenario C: Scale-Down Test

1. Ensure no pending runs in Spacelift
2. Have idle workers (no busy runs)
3. Run the autoscaler with scale-down enabled:

```bash
set -a && source .env.test && set +a
export AUTOSCALING_MAX_CREATE=0 AUTOSCALING_MAX_KILL=1
/tmp/autoscaler-local
```

4. Verify a worker is drained and instance is terminated

---

## Troubleshooting

### "unauthorized" from Spacelift

- Verify `SPACELIFT_API_KEY_ID` matches the secret value
- Check the API key hasn't expired
- Verify the key has access to the worker pool

### "Permission denied" on Secret Manager

- Grant `roles/secretmanager.secretAccessor` to your identity
- Verify the secret path is correct (project number, not name)

### "403" on instance delete

- Grant `roles/compute.instanceAdmin.v1` to your identity

### "worker has invalid metadata"

- Workers need `gcp_igm_id` and `gcp_instance_id` in their metadata
- Configure the Spacelift worker launcher to set these values

---

## Verification Checklist

- [ ] ADC configured (`gcloud auth application-default login`)
- [ ] Secret Manager access works
- [ ] IGM is readable
- [ ] Spacelift API authenticates successfully
- [ ] Worker pool is queryable
- [ ] Instance count matches expected
- [ ] Workers have proper metadata
- [ ] Scale-up works (if testing)
- [ ] Scale-down works (if testing)
