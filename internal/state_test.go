package internal_test

import (
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/spacelift-io/awsautoscalr/internal"
)

func testLogger() *slog.Logger {
	return slog.Default()
}

// awsInstanceIdentifier implements InstanceIdentifier using AWS-style metadata keys.
// Used for testing NewState with AWS-style worker metadata.
type awsInstanceIdentifier struct{}

func (awsInstanceIdentifier) InstanceIdentity(worker *internal.Worker) (internal.GroupID, internal.InstanceID, error) {
	groupID, groupErr := worker.MetadataValue("asg_id")
	instanceID, instanceErr := worker.MetadataValue("instance_id")
	return internal.GroupID(groupID), internal.InstanceID(instanceID), errors.Join(groupErr, instanceErr)
}

var testIdentifier = awsInstanceIdentifier{}

func TestState_StrayInstances(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "instance-id"
	const failedToTerminateInstanceID = "instance-id2"
	cfg := internal.RuntimeConfig{}
	asg := &internal.AutoScalingGroup{
		Name:            asgName,
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
		Instances: []internal.Instance{
			{
				ID: instanceID,
			},
		},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{
				Metadata: mustJSON(map[string]any{
					"asg_id":      asgName,
					"instance_id": instanceID,
				}),
			},
			{
				Drained: true,
				Metadata: mustJSON(map[string]any{
					"asg_id":      asgName,
					"instance_id": failedToTerminateInstanceID,
				}),
			},
		},
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	strayInstances := state.StrayInstances()
	require.ElementsMatch(t, []string{failedToTerminateInstanceID}, strayInstances)
}

func TestNewState_ASGNameNotSet_ReturnsError(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "",
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)

	require.EqualError(t, err, "ASG name is not set")
}

func TestNewState_ASGMinSizeNotSet_ReturnsError(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         -1,
		MaxSize:         5,
		DesiredCapacity: 3,
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)

	require.EqualError(t, err, "ASG minimum size is not set")
}

func TestNewState_ASGMaxSizeNotSet_ReturnsError(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         -1,
		DesiredCapacity: 3,
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)

	require.EqualError(t, err, "ASG maximum size is not set")
}

func TestNewState_ASGDesiredCapacityNotSet_ReturnsError(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: -1,
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)

	require.EqualError(t, err, "ASG desired capacity is not set")
}

func TestNewState_WorkerMissingMetadata_SkipsWorker(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{{
			ID:       "worker-123",
			Metadata: mustJSON(map[string]any{}),
		}},
	}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)

	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, 0, state.ValidWorkerCount(), "worker with missing metadata should be skipped")
}

func TestNewState_WorkerEmptyASGID_SkipsWorker(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{{
			ID: "worker-123",
			Metadata: mustJSON(map[string]any{
				"asg_id":      "",
				"instance_id": "i-1234567890",
			}),
		}},
	}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)

	require.NoError(t, err)
	require.NotNil(t, state)
	require.Equal(t, 0, state.ValidWorkerCount(), "worker with empty ASG ID should be skipped")
}

func TestNewState_WorkerIncorrectASG_ReturnsError(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{{
			ID: "worker-456",
			Metadata: mustJSON(map[string]any{
				"asg_id":      "other-asg",
				"instance_id": "i-1234567890",
			}),
		}},
	}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)

	require.EqualError(t, err, "worker worker-456 has incorrect ASG: other-asg (expected: asg-name)")
}

func TestStrayInstances_NoWorkers_InstanceInService_ReturnsStrayInstance(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "i-1234567890"

	asg := &internal.AutoScalingGroup{
		Name:            asgName,
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
		Instances: []internal.Instance{{
			ID:             instanceID,
			LifecycleState: internal.LifecycleStateInService,
		}},
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	instanceIDs := state.StrayInstances()

	require.ElementsMatch(t, []string{instanceID}, instanceIDs)
}

func TestStrayInstances_NoWorkers_InstanceNotInService_ReturnsEmpty(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "i-1234567890"

	asg := &internal.AutoScalingGroup{
		Name:            asgName,
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
		Instances: []internal.Instance{{
			ID:             instanceID,
			LifecycleState: internal.LifecycleStateTerminating,
		}},
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	instanceIDs := state.StrayInstances()

	require.Empty(t, instanceIDs)
}

func TestStrayInstances_WorkerMatchesInstance_ReturnsEmpty(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "i-1234567890"

	asg := &internal.AutoScalingGroup{
		Name:            asgName,
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
		Instances: []internal.Instance{{
			ID:             instanceID,
			LifecycleState: internal.LifecycleStateInService,
		}},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{{
			Metadata: mustJSON(map[string]any{
				"asg_id":      asgName,
				"instance_id": instanceID,
			}),
		}},
	}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	instanceIDs := state.StrayInstances()

	require.Empty(t, instanceIDs)
}

func TestStrayInstances_WorkerDoesNotMatchInstance_ReturnsStrayInstance(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "i-1234567890"

	asg := &internal.AutoScalingGroup{
		Name:            asgName,
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
		Instances: []internal.Instance{{
			ID:             instanceID,
			LifecycleState: internal.LifecycleStateInService,
		}},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{{
			Metadata: mustJSON(map[string]any{
				"asg_id":      asgName,
				"instance_id": "i-0987654321",
			}),
		}},
	}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	instanceIDs := state.StrayInstances()

	require.ElementsMatch(t, []string{instanceID}, instanceIDs)
}

func TestScalableWorkers_ReturnsIdleWorkers(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 3,
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{
				Busy: true,
				ID:   "busy",
				Metadata: mustJSON(map[string]any{
					"asg_id":      "asg-name",
					"instance_id": "i-busy",
				}),
			},
			{
				Busy: false,
				ID:   "idle",
				Metadata: mustJSON(map[string]any{
					"asg_id":      "asg-name",
					"instance_id": "i-idle",
				}),
			},
		},
	}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	scalableWorkers := state.ScalableWorkers()

	require.Len(t, scalableWorkers, 1)
	require.Equal(t, "idle", scalableWorkers[0].ID)
}

func TestDecide_NoWorkersNoPendingRunsNoInstances_NoScaling(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         0,
		MaxSize:         2,
		DesiredCapacity: 0,
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"autoscaling group exactly at the right size"}, decision.Comments)
}

func TestDecide_NoWorkersNoPendingRunsWithInstances_NoScalingNotInBalance(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         2,
		DesiredCapacity: 2,
		Instances:       []internal.Instance{{}},
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"number of valid workers does not match the desired capacity of the ASG"}, decision.Comments)
}

func TestDecide_TerminatingInstanceDoesNotBlockScaleUp(t *testing.T) {
	// Scenario: autoscaler previously scaled down, the instance is mid-deletion
	// (still visible in the instance list) but desired capacity is already decremented.
	// A new run arrives and the autoscaler should scale up without being blocked.
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         0,
		MaxSize:         5,
		DesiredCapacity: 0, // Already decremented by the scale-down
		Instances: []internal.Instance{
			{ID: "i-dying", LifecycleState: internal.LifecycleStateTerminating},
		},
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 1,
	}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(1, 1)

	require.Equal(t, internal.ScalingDirectionUp, decision.ScalingDirection)
	require.Equal(t, 1, decision.ScalingSize)
}

func TestDecide_CreatingInstancePreventsDoubleScaleUp(t *testing.T) {
	// Scenario: autoscaler previously scaled up (desired=2), one instance is
	// running with a registered worker, the other is still creating. The guard
	// should block to prevent a second scale-up for the same demand.
	const asgName = "asg-name"
	asg := &internal.AutoScalingGroup{
		Name:            asgName,
		MinSize:         0,
		MaxSize:         5,
		DesiredCapacity: 2,
		Instances: []internal.Instance{
			{ID: "i-running", LifecycleState: internal.LifecycleStateInService},
			{ID: "i-creating", LifecycleState: "CREATING"},
		},
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 1,
		Workers: []internal.Worker{
			{
				ID: "worker-1",
				Metadata: mustJSON(map[string]any{
					"asg_id":      asgName,
					"instance_id": "i-running",
				}),
			},
		},
	}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.ElementsMatch(t, []string{"number of valid workers does not match the desired capacity of the ASG"}, decision.Comments)
}

func TestDecide_MixedStateTerminatingInstanceWithPendingRuns(t *testing.T) {
	// Scenario: 2 workers active, 1 instance was scaled down and is terminating.
	// Desired capacity already decremented to 2. New pending runs arrive.
	// The guard should pass and allow scale-up.
	const asgName = "asg-name"
	asg := &internal.AutoScalingGroup{
		Name:            asgName,
		MinSize:         0,
		MaxSize:         10,
		DesiredCapacity: 2, // Decremented from 3 when termination was initiated
		Instances: []internal.Instance{
			{ID: "i-1", LifecycleState: internal.LifecycleStateInService},
			{ID: "i-2", LifecycleState: internal.LifecycleStateInService},
			{ID: "i-dying", LifecycleState: internal.LifecycleStateTerminating},
		},
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 3,
		Workers: []internal.Worker{
			{
				ID:   "worker-1",
				Busy: true,
				Metadata: mustJSON(map[string]any{
					"asg_id":      asgName,
					"instance_id": "i-1",
				}),
			},
			{
				ID:   "worker-2",
				Busy: true,
				Metadata: mustJSON(map[string]any{
					"asg_id":      asgName,
					"instance_id": "i-2",
				}),
			},
		},
	}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(5, 2)

	require.Equal(t, internal.ScalingDirectionUp, decision.ScalingDirection)
	require.Equal(t, 3, decision.ScalingSize)
}

func TestDecide_PreemptedInstanceWithReplacementCreating(t *testing.T) {
	// Scenario: a VM was preempted (terminating), the cloud is auto-replacing it
	// (creating), and the old worker is still registered in Spacelift.
	// Desired capacity unchanged. Guard should pass since validWorkers == desired.
	const asgName = "asg-name"
	asg := &internal.AutoScalingGroup{
		Name:            asgName,
		MinSize:         1,
		MaxSize:         5,
		DesiredCapacity: 1, // Unchanged — cloud will replace the preempted instance
		Instances: []internal.Instance{
			{ID: "i-preempted", LifecycleState: internal.LifecycleStateTerminating},
			{ID: "i-replacement", LifecycleState: "CREATING"},
		},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID: "old-worker",
				Metadata: mustJSON(map[string]any{
					"asg_id":      asgName,
					"instance_id": "i-preempted",
				}),
			},
		},
	}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	// validWorkers=1, desired=1 → guard passes.
	// No pending runs, 1 scalable worker → no scaling needed.
	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.ElementsMatch(t, []string{"autoscaling group exactly at the right size"}, decision.Comments)
}

func TestDecide_NoWorkersPendingRunsAtMaxSize_NoScaling(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         2,
		DesiredCapacity: 2,
		Instances:       []internal.Instance{{}, {}},
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 5,
		Workers: []internal.Worker{
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-1"})},
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-2"})},
		},
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"autoscaling group is already at maximum size"}, decision.Comments)
}

func TestDecide_PendingRunsConstrainedByMaxCreate_ScalesUpBy1(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         2,
		DesiredCapacity: 0,
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 5,
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(1, 2)

	require.Equal(t, internal.ScalingDirectionUp, decision.ScalingDirection)
	require.Equal(t, 1, decision.ScalingSize)
	require.ElementsMatch(t, []string{
		"need 5 workers, but can only create 1",
		"adding 1 workers to match pending runs",
	}, decision.Comments)
}

func TestDecide_PendingRunsNotConstrainedByMaxCreateOrASGSize_ScalesUpBy5(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         10,
		DesiredCapacity: 0,
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 5,
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(10, 2)

	require.Equal(t, internal.ScalingDirectionUp, decision.ScalingDirection)
	require.Equal(t, 5, decision.ScalingSize)
	require.ElementsMatch(t, []string{"adding 5 workers to match pending runs"}, decision.Comments)
}

func TestDecide_PendingRunsConstrainedByMaxASGSize_ScalesUpBy2(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         2,
		DesiredCapacity: 0,
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 5,
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(10, 2)

	require.Equal(t, internal.ScalingDirectionUp, decision.ScalingDirection)
	require.Equal(t, 2, decision.ScalingSize)
	require.ElementsMatch(t, []string{"adding 2 workers to match pending runs, up to the ASG max size"}, decision.Comments)
}

func TestDecide_NoPendingRunsAtMinSize_NoScaling(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         2,
		MaxSize:         10,
		DesiredCapacity: 2,
		Instances:       []internal.Instance{{}, {}},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-1"})},
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-2"})},
		},
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"autoscaling group exactly at the right size"}, decision.Comments)
}

func TestDecide_NoPendingRunsConstrainedByMaxKill_ScalesDownBy1(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         0,
		MaxSize:         10,
		DesiredCapacity: 2,
		Instances:       []internal.Instance{{}, {}},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-1"})},
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-2"})},
		},
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 1)

	require.Equal(t, internal.ScalingDirectionDown, decision.ScalingDirection)
	require.Equal(t, 1, decision.ScalingSize)
	require.ElementsMatch(t, []string{
		"need to kill 2 workers, but can only kill 1",
		"removing 1 idle workers",
	}, decision.Comments)
}

func TestDecide_NoPendingRunsConstrainedByMinASGSize_ScalesDownBy1(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         1,
		MaxSize:         10,
		DesiredCapacity: 2,
		Instances:       []internal.Instance{{}, {}},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-1"})},
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-2"})},
		},
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 10)

	require.Equal(t, internal.ScalingDirectionDown, decision.ScalingDirection)
	require.Equal(t, 1, decision.ScalingSize)
	require.ElementsMatch(t, []string{
		"removing 1 idle workers",
	}, decision.Comments)
}

func TestDecide_NoPendingRunsNotConstrainedByMinASGSize_ScalesDownBy2(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         0,
		MaxSize:         10,
		DesiredCapacity: 2,
		Instances:       []internal.Instance{{}, {}},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-1"})},
			{Metadata: mustJSON(map[string]any{"asg_id": "asg-name", "instance_id": "i-2"})},
		},
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 10)

	require.Equal(t, internal.ScalingDirectionDown, decision.ScalingDirection)
	require.Equal(t, 2, decision.ScalingSize)
	require.ElementsMatch(t, []string{"removing 2 idle workers"}, decision.Comments)
}

func TestDecide_WaitingForIdleWorkers_NoScaling(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         0,
		MaxSize:         10,
		DesiredCapacity: 2,
		Instances:       []internal.Instance{{}, {}},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID:   "busy",
				Busy: true,
				Metadata: mustJSON(map[string]any{
					"asg_id":      "asg-name",
					"instance_id": "i-busy",
				}),
			},
			{
				ID:        "idle",
				Busy:      false,
				CreatedAt: int32(time.Now().Unix()),
				Metadata: mustJSON(map[string]any{
					"asg_id":      "asg-name",
					"instance_id": "i-idle",
				}),
			},
		},
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	decision := state.Decide(2, 10)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"autoscaling group exactly at the right size"}, decision.Comments)
}

func TestScalableWorkers_WithAvailableAt_UsesAvailableAtForIdleTime(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         0,
		MaxSize:         10,
		DesiredCapacity: 1,
	}

	// Worker created 10 minutes ago but became available (idle) just now
	createdAt := int32(time.Now().Add(-10 * time.Minute).Unix())
	availableAt := int32(time.Now().Unix()) // just became idle

	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID:          "worker-with-available-at",
				Busy:        false,
				CreatedAt:   createdAt,
				AvailableAt: &availableAt,
				Metadata: mustJSON(map[string]any{
					"asg_id":      "asg-name",
					"instance_id": "i-1",
				}),
			},
		},
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 5, // 5 minute delay
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	scalableWorkers := state.ScalableWorkers()

	// Worker should NOT be scalable because it just became available (< 5 min idle)
	// even though it was created 10 minutes ago
	require.Empty(t, scalableWorkers, "worker should not be scalable because availableAt is recent")
}

func TestScalableWorkers_WithAvailableAt_IdleLongEnough_IsScalable(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         0,
		MaxSize:         10,
		DesiredCapacity: 1,
	}

	// Worker became available 10 minutes ago
	createdAt := int32(time.Now().Add(-30 * time.Minute).Unix())
	availableAt := int32(time.Now().Add(-10 * time.Minute).Unix())

	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID:          "worker-idle-long-enough",
				Busy:        false,
				CreatedAt:   createdAt,
				AvailableAt: &availableAt,
				Metadata: mustJSON(map[string]any{
					"asg_id":      "asg-name",
					"instance_id": "i-1",
				}),
			},
		},
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 5, // 5 minute delay
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	scalableWorkers := state.ScalableWorkers()

	// Worker should be scalable because it's been idle for 10 minutes (> 5 min delay)
	require.Len(t, scalableWorkers, 1)
	require.Equal(t, "worker-idle-long-enough", scalableWorkers[0].ID)
}

func TestScalableWorkers_WithoutAvailableAt_FallsBackToCreatedAt(t *testing.T) {
	asg := &internal.AutoScalingGroup{
		Name:            "asg-name",
		MinSize:         0,
		MaxSize:         10,
		DesiredCapacity: 1,
	}

	// Worker created 10 minutes ago, no availableAt (legacy worker)
	createdAt := int32(time.Now().Add(-10 * time.Minute).Unix())

	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID:          "legacy-worker",
				Busy:        false,
				CreatedAt:   createdAt,
				AvailableAt: nil, // not set - legacy worker
				Metadata: mustJSON(map[string]any{
					"asg_id":      "asg-name",
					"instance_id": "i-1",
				}),
			},
		},
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 5, // 5 minute delay
	}

	state, err := internal.NewState(workerPool, asg, cfg, testLogger(), testIdentifier)
	require.NoError(t, err)

	scalableWorkers := state.ScalableWorkers()

	// Worker should be scalable because createdAt is 10 minutes ago (> 5 min delay)
	require.Len(t, scalableWorkers, 1)
	require.Equal(t, "legacy-worker", scalableWorkers[0].ID)
}

func nullable[T any](t T) *T {
	out := t
	return &out
}

func mustJSON(T any) string {
	out, err := json.Marshal(T)
	if err != nil {
		panic(err)
	}
	return string(out)
}
