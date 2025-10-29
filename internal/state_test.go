package internal_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/stretchr/testify/require"

	"github.com/spacelift-io/awsautoscalr/internal"
)

func TestState_StrayInstances(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "instance-id"
	const failedToTerminateInstanceID = "instance-id2"
	cfg := internal.RuntimeConfig{}
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable(asgName),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
		Instances: []types.Instance{
			{
				InstanceId: nullable(instanceID),
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

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	strayInstances := state.StrayInstances()
	require.ElementsMatch(t, []string{failedToTerminateInstanceID}, strayInstances)
}

func TestNewState_ASGNameNotSet_ReturnsError(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nil,
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg)

	require.EqualError(t, err, "ASG name is not set")
}

func TestNewState_ASGMinSizeNotSet_ReturnsError(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nil,
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg)

	require.EqualError(t, err, "ASG minimum size is not set")
}

func TestNewState_ASGMaxSizeNotSet_ReturnsError(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nil,
		DesiredCapacity:      nullable(int32(3)),
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg)

	require.EqualError(t, err, "ASG maximum size is not set")
}

func TestNewState_ASGDesiredCapacityNotSet_ReturnsError(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nil,
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg)

	require.EqualError(t, err, "ASG desired capacity is not set")
}

func TestNewState_WorkerMissingMetadata_ReturnsError(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{{
			Metadata: mustJSON(map[string]any{}),
		}},
	}
	cfg := internal.RuntimeConfig{}

	_, err := internal.NewState(workerPool, asg, cfg)

	require.Error(t, err)
	require.ErrorContains(t, err, "metadata asg_id not present")
	require.ErrorContains(t, err, "metadata instance_id not present")
}

func TestNewState_WorkerEmptyASGID_ReturnsError(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
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

	_, err := internal.NewState(workerPool, asg, cfg)

	require.EqualError(t, err, "worker worker-123 has empty ASG ID in metadata")
}

func TestNewState_WorkerIncorrectASG_ReturnsError(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
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

	_, err := internal.NewState(workerPool, asg, cfg)

	require.EqualError(t, err, "worker worker-456 has incorrect ASG: other-asg (expected: asg-name)")
}

func TestStrayInstances_NoWorkers_InstanceInService_ReturnsStrayInstance(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "i-1234567890"

	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable(asgName),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
		Instances: []types.Instance{{
			InstanceId:     nullable(instanceID),
			LifecycleState: types.LifecycleStateInService,
		}},
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	instanceIDs := state.StrayInstances()

	require.ElementsMatch(t, []string{instanceID}, instanceIDs)
}

func TestStrayInstances_NoWorkers_InstanceNotInService_ReturnsEmpty(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "i-1234567890"

	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable(asgName),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
		Instances: []types.Instance{{
			InstanceId:     nullable(instanceID),
			LifecycleState: types.LifecycleStateTerminating,
		}},
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{}

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	instanceIDs := state.StrayInstances()

	require.Empty(t, instanceIDs)
}

func TestStrayInstances_WorkerMatchesInstance_ReturnsEmpty(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "i-1234567890"

	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable(asgName),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
		Instances: []types.Instance{{
			InstanceId:     nullable(instanceID),
			LifecycleState: types.LifecycleStateInService,
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

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	instanceIDs := state.StrayInstances()

	require.Empty(t, instanceIDs)
}

func TestStrayInstances_WorkerDoesNotMatchInstance_ReturnsStrayInstance(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "i-1234567890"

	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable(asgName),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
		Instances: []types.Instance{{
			InstanceId:     nullable(instanceID),
			LifecycleState: types.LifecycleStateInService,
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

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	instanceIDs := state.StrayInstances()

	require.ElementsMatch(t, []string{instanceID}, instanceIDs)
}

func TestScalableWorkers_ReturnsIdleWorkers(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
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

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	scalableWorkers := state.ScalableWorkers()

	require.Len(t, scalableWorkers, 1)
	require.Equal(t, "idle", scalableWorkers[0].ID)
}

func TestDecide_NoWorkersNoPendingRunsNoInstances_NoScaling(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(2)),
		DesiredCapacity:      nullable(int32(2)),
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"autoscaling group exactly at the right size"}, decision.Comments)
}

func TestDecide_NoWorkersNoPendingRunsWithInstances_NoScalingNotInBalance(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(2)),
		DesiredCapacity:      nullable(int32(2)),
		Instances:            []types.Instance{{}},
	}
	workerPool := &internal.WorkerPool{}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"number of workers does not match the number of instances in the ASG"}, decision.Comments)
}

func TestDecide_NoWorkersPendingRunsAtMaxSize_NoScaling(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(2)),
		DesiredCapacity:      nullable(int32(2)),
		Instances:            []types.Instance{{}, {}},
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

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"autoscaling group is already at maximum size"}, decision.Comments)
}

func TestDecide_PendingRunsConstrainedByMaxCreate_ScalesUpBy1(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(2)),
		DesiredCapacity:      nullable(int32(0)),
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 5,
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg)
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
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(10)),
		DesiredCapacity:      nullable(int32(0)),
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 5,
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	decision := state.Decide(10, 2)

	require.Equal(t, internal.ScalingDirectionUp, decision.ScalingDirection)
	require.Equal(t, 5, decision.ScalingSize)
	require.ElementsMatch(t, []string{"adding 5 workers to match pending runs"}, decision.Comments)
}

func TestDecide_PendingRunsConstrainedByMaxASGSize_ScalesUpBy2(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(2)),
		DesiredCapacity:      nullable(int32(0)),
	}
	workerPool := &internal.WorkerPool{
		PendingRuns: 5,
	}
	cfg := internal.RuntimeConfig{
		AutoscalingScaleDownDelay: 50,
	}

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	decision := state.Decide(10, 2)

	require.Equal(t, internal.ScalingDirectionUp, decision.ScalingDirection)
	require.Equal(t, 2, decision.ScalingSize)
	require.ElementsMatch(t, []string{"adding 2 workers to match pending runs, up to the ASG max size"}, decision.Comments)
}

func TestDecide_NoPendingRunsAtMinSize_NoScaling(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(2)),
		MaxSize:              nullable(int32(10)),
		DesiredCapacity:      nullable(int32(2)),
		Instances:            []types.Instance{{}, {}},
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

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	decision := state.Decide(2, 2)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"autoscaling group is already at minimum size"}, decision.Comments)
}

func TestDecide_NoPendingRunsConstrainedByMaxKill_ScalesDownBy1(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(0)),
		MaxSize:              nullable(int32(10)),
		DesiredCapacity:      nullable(int32(2)),
		Instances:            []types.Instance{{}, {}},
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

	state, err := internal.NewState(workerPool, asg, cfg)
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
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(10)),
		DesiredCapacity:      nullable(int32(2)),
		Instances:            []types.Instance{{}, {}},
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

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	decision := state.Decide(2, 10)

	require.Equal(t, internal.ScalingDirectionDown, decision.ScalingDirection)
	require.Equal(t, 1, decision.ScalingSize)
	require.ElementsMatch(t, []string{
		"need to kill 2 workers, but can't get below minimum size of 1",
		"removing 1 idle workers",
	}, decision.Comments)
}

func TestDecide_NoPendingRunsNotConstrainedByMinASGSize_ScalesDownBy2(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(0)),
		MaxSize:              nullable(int32(10)),
		DesiredCapacity:      nullable(int32(2)),
		Instances:            []types.Instance{{}, {}},
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

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	decision := state.Decide(2, 10)

	require.Equal(t, internal.ScalingDirectionDown, decision.ScalingDirection)
	require.Equal(t, 2, decision.ScalingSize)
	require.ElementsMatch(t, []string{"removing 2 idle workers"}, decision.Comments)
}

func TestDecide_WaitingForIdleWorkers_NoScaling(t *testing.T) {
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable("asg-name"),
		MinSize:              nullable(int32(0)),
		MaxSize:              nullable(int32(10)),
		DesiredCapacity:      nullable(int32(2)),
		Instances:            []types.Instance{{}, {}},
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

	state, err := internal.NewState(workerPool, asg, cfg)
	require.NoError(t, err)

	decision := state.Decide(2, 10)

	require.Equal(t, internal.ScalingDirectionNone, decision.ScalingDirection)
	require.Zero(t, decision.ScalingSize)
	require.ElementsMatch(t, []string{"autoscaling group exactly at the right size"}, decision.Comments)
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
