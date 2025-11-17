package internal_test

import (
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/shurcooL/graphql"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/spacelift-io/awsautoscalr/internal"
	"github.com/spacelift-io/awsautoscalr/internal/ifaces"
)

const (
	asgName      = "test-asg"
	workerPoolID = "test-pool"
)

func setupController() (*internal.AWSController, *ifaces.MockAutoscaling, *ifaces.MockEC2, *ifaces.MockSpacelift) {
	mockAutoscaling := &ifaces.MockAutoscaling{}
	mockEC2 := &ifaces.MockEC2{}
	mockSpacelift := &ifaces.MockSpacelift{}

	tp := trace.NewTracerProvider(
		trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(tracetest.NewNoopExporter())),
	)
	otel.SetTracerProvider(tp)

	controller := &internal.AWSController{
		Controller: internal.Controller{
			Spacelift:             mockSpacelift,
			SpaceliftWorkerPoolID: workerPoolID,
			Tracer:                tp.Tracer("unittest"),
		},
		Autoscaling:             mockAutoscaling,
		EC2:                     mockEC2,
		AWSAutoscalingGroupName: asgName,
	}

	return controller, mockAutoscaling, mockEC2, mockSpacelift
}

// DescribeInstances tests

func TestDescribeInstances_APICallFails_SendsCorrectInput(t *testing.T) {
	instanceIDs := []string{"i-1"}

	sut, _, mockEC2, _ := setupController()

	var capturedInput *ec2.DescribeInstancesInput
	mockEC2.On(
		"DescribeInstances",
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			capturedInput = in.(*ec2.DescribeInstancesInput)
			return true
		}),
		mock.Anything,
	).Return(nil, errors.New("bacon"))

	_, _ = sut.DescribeInstances(t.Context(), instanceIDs)

	require.NotNil(t, capturedInput)
	require.Equal(t, instanceIDs, capturedInput.InstanceIds)
}

func TestDescribeInstances_APICallFails_ReturnsError(t *testing.T) {
	instanceIDs := []string{"i-1"}

	sut, _, mockEC2, _ := setupController()

	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("bacon"))

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Empty(t, instances)
	require.EqualError(t, err, "could not describe instances: bacon")
}

func TestDescribeInstances_InstanceHasNoID_ReturnsError(t *testing.T) {
	instanceIDs := []string{"i-1"}

	sut, _, mockEC2, _ := setupController()

	output := &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{Instances: []ec2types.Instance{{
				InstanceId: nil,
				LaunchTime: nullable(time.Now()),
			}}},
		},
	}

	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(output, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Empty(t, instances)
	require.EqualError(t, err, "could not find instance ID")
}

func TestDescribeInstances_InstanceHasNoLaunchTime_ReturnsError(t *testing.T) {
	instanceIDs := []string{"i-1"}

	sut, _, mockEC2, _ := setupController()

	output := &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{Instances: []ec2types.Instance{{
				InstanceId: &instanceIDs[0],
				LaunchTime: nil,
			}}},
		},
	}

	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(output, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Empty(t, instances)
	require.EqualError(t, err, "could not find launch time for instance i-1")
}

func TestDescribeInstances_ValidInstance_ReturnsInstance(t *testing.T) {
	instanceIDs := []string{"i-1"}

	sut, _, mockEC2, _ := setupController()

	output := &ec2.DescribeInstancesOutput{
		Reservations: []ec2types.Reservation{
			{Instances: []ec2types.Instance{{
				InstanceId: &instanceIDs[0],
				LaunchTime: nullable(time.Now()),
			}}},
		},
	}

	mockEC2.On("DescribeInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(output, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.NoError(t, err)
	require.Len(t, instances, 1)
}

// GetAutoscalingGroup tests

func TestGetAutoscalingGroup_APICallFails_SendsCorrectInput(t *testing.T) {
	sut, mockAutoscaling, _, _ := setupController()

	var capturedInput *autoscaling.DescribeAutoScalingGroupsInput
	mockAutoscaling.On(
		"DescribeAutoScalingGroups",
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			capturedInput = in.(*autoscaling.DescribeAutoScalingGroupsInput)
			return true
		}),
		mock.Anything,
	).Return(nil, errors.New("bacon"))

	_, _ = sut.GetAutoscalingGroup(t.Context())

	require.NotNil(t, capturedInput)
	require.ElementsMatch(t, []string{asgName}, capturedInput.AutoScalingGroupNames)
}

func TestGetAutoscalingGroup_APICallFails_ReturnsError(t *testing.T) {
	sut, mockAutoscaling, _, _ := setupController()

	mockAutoscaling.On("DescribeAutoScalingGroups", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("bacon"))

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.Nil(t, group)
	require.EqualError(t, err, "could not get autoscaling group details: bacon")
}

func TestGetAutoscalingGroup_NoGroupsReturned_ReturnsError(t *testing.T) {
	sut, mockAutoscaling, _, _ := setupController()

	output := &autoscaling.DescribeAutoScalingGroupsOutput{
		AutoScalingGroups: nil,
	}

	mockAutoscaling.On("DescribeAutoScalingGroups", mock.Anything, mock.Anything, mock.Anything).
		Return(output, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.Nil(t, group)
	require.EqualError(t, err, "could not find autoscaling group test-asg")
}

func TestGetAutoscalingGroup_MultipleGroupsReturned_ReturnsError(t *testing.T) {
	sut, mockAutoscaling, _, _ := setupController()

	output := &autoscaling.DescribeAutoScalingGroupsOutput{
		AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{{}, {}},
	}

	mockAutoscaling.On("DescribeAutoScalingGroups", mock.Anything, mock.Anything, mock.Anything).
		Return(output, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.Nil(t, group)
	require.EqualError(t, err, "found more than one autoscaling group with name test-asg")
}

func TestGetAutoscalingGroup_SingleGroupReturned_ReturnsGroup(t *testing.T) {
	sut, mockAutoscaling, _, _ := setupController()

	output := &autoscaling.DescribeAutoScalingGroupsOutput{
		AutoScalingGroups: []autoscalingtypes.AutoScalingGroup{{}},
	}

	mockAutoscaling.On("DescribeAutoScalingGroups", mock.Anything, mock.Anything, mock.Anything).
		Return(output, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.NoError(t, err)
	require.NotNil(t, group)
}

// GetWorkerPool tests

func TestGetWorkerPool_APICallFails_SendsCorrectInput(t *testing.T) {
	sut, _, _, mockSpacelift := setupController()

	var capturedParams map[string]any
	mockSpacelift.On(
		"Query",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			capturedParams = in.(map[string]any)
			return true
		}),
		mock.Anything,
	).Return(errors.New("bacon"))

	_, _ = sut.GetWorkerPool(t.Context())

	require.NotNil(t, capturedParams)
	require.Equal(t, workerPoolID, capturedParams["workerPool"])
}

func TestGetWorkerPool_APICallFails_ReturnsError(t *testing.T) {
	sut, _, _, mockSpacelift := setupController()

	mockSpacelift.On("Query", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("bacon"))

	workerPool, err := sut.GetWorkerPool(t.Context())

	require.Nil(t, workerPool)
	require.EqualError(t, err, "could not get Spacelift worker pool details: bacon")
}

func TestGetWorkerPool_WorkerPoolNotFound_ReturnsError(t *testing.T) {
	sut, _, _, mockSpacelift := setupController()

	mockSpacelift.On("Query", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			details := args.Get(1).(*internal.WorkerPoolDetails)
			details.Pool = nil
		}).Return(nil)

	workerPool, err := sut.GetWorkerPool(t.Context())

	require.Nil(t, workerPool)
	require.EqualError(t, err, "worker pool not found or not accessible")
}

func TestGetWorkerPool_WorkerPoolFound_ReturnsSortedAndFilteredWorkers(t *testing.T) {
	sut, _, _, mockSpacelift := setupController()

	mockSpacelift.On("Query", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			details := args.Get(1).(*internal.WorkerPoolDetails)
			details.Pool = &internal.WorkerPool{
				Workers: []internal.Worker{
					{ID: "newer", CreatedAt: 5, Drained: false},
					{ID: "drained", CreatedAt: 2, Drained: true},
					{ID: "older", CreatedAt: 1, Drained: false},
				},
			}
		}).Return(nil)

	workerPool, err := sut.GetWorkerPool(t.Context())

	require.NoError(t, err)
	require.NotNil(t, workerPool)
	require.Len(t, workerPool.Workers, 2)
	require.Equal(t, "older", workerPool.Workers[0].ID)
	require.Equal(t, "newer", workerPool.Workers[1].ID)
}

// DrainWorker tests

func TestDrainWorker_DrainCallFails_SendsCorrectInput(t *testing.T) {
	const workerID = "test-worker"

	sut, _, _, mockSpacelift := setupController()

	var capturedParams map[string]any
	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			if params := in.(map[string]any); params["drain"].(graphql.Boolean) {
				capturedParams = params
				return true
			}
			return false
		}),
		mock.Anything,
	).Return(errors.New("bacon"))

	_, _ = sut.DrainWorker(t.Context(), workerID)

	require.NotNil(t, capturedParams)
	require.Equal(t, workerPoolID, capturedParams["workerPoolId"])
	require.Equal(t, workerID, capturedParams["workerId"])
	require.True(t, bool(capturedParams["drain"].(graphql.Boolean)))
}

func TestDrainWorker_DrainCallFails_ReturnsError(t *testing.T) {
	const workerID = "test-worker"

	sut, _, _, mockSpacelift := setupController()

	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			params := in.(map[string]any)
			return bool(params["drain"].(graphql.Boolean))
		}),
		mock.Anything,
	).Return(errors.New("bacon"))

	drained, err := sut.DrainWorker(t.Context(), workerID)

	require.False(t, drained)
	require.EqualError(t, err, "could not drain worker: could not set worker drain to true: bacon")
}

func TestDrainWorker_WorkerNotBusy_SucceedsAndReportsDrained(t *testing.T) {
	const workerID = "test-worker"

	sut, _, _, mockSpacelift := setupController()

	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			params := in.(map[string]any)
			return bool(params["drain"].(graphql.Boolean))
		}),
		mock.Anything,
	).Run(func(args mock.Arguments) {
		args.Get(1).(*internal.WorkerDrainSet).Worker = internal.Worker{Busy: false}
	}).Return(nil)

	drained, err := sut.DrainWorker(t.Context(), workerID)

	require.True(t, drained)
	require.NoError(t, err)
}

func TestDrainWorker_WorkerBusy_UndrainCallFails_SendsCorrectInput(t *testing.T) {
	const workerID = "test-worker"

	sut, _, _, mockSpacelift := setupController()

	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			params := in.(map[string]any)
			return bool(params["drain"].(graphql.Boolean))
		}),
		mock.Anything,
	).Run(func(args mock.Arguments) {
		args.Get(1).(*internal.WorkerDrainSet).Worker = internal.Worker{Busy: true}
	}).Return(nil)

	var capturedUndrainParams map[string]any
	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			if params := in.(map[string]any); !bool(params["drain"].(graphql.Boolean)) {
				capturedUndrainParams = params
				return true
			}
			return false
		}),
		mock.Anything,
	).Return(errors.New("bacon"))

	_, _ = sut.DrainWorker(t.Context(), workerID)

	require.NotNil(t, capturedUndrainParams)
	require.Equal(t, workerPoolID, capturedUndrainParams["workerPoolId"])
	require.Equal(t, workerID, capturedUndrainParams["workerId"])
	require.False(t, bool(capturedUndrainParams["drain"].(graphql.Boolean)))
}

func TestDrainWorker_WorkerBusy_UndrainCallFails_ReturnsError(t *testing.T) {
	const workerID = "test-worker"

	sut, _, _, mockSpacelift := setupController()

	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			params := in.(map[string]any)
			return bool(params["drain"].(graphql.Boolean))
		}),
		mock.Anything,
	).Run(func(args mock.Arguments) {
		args.Get(1).(*internal.WorkerDrainSet).Worker = internal.Worker{Busy: true}
	}).Return(nil)

	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			params := in.(map[string]any)
			return !bool(params["drain"].(graphql.Boolean))
		}),
		mock.Anything,
	).Return(errors.New("bacon"))

	drained, err := sut.DrainWorker(t.Context(), workerID)

	require.False(t, drained)
	require.EqualError(t, err, "could not undrain a busy worker: could not set worker drain to false: bacon")
}

func TestDrainWorker_WorkerBusy_UndrainCallSucceeds_ReportsNotDrained(t *testing.T) {
	const workerID = "test-worker"

	sut, _, _, mockSpacelift := setupController()

	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			params := in.(map[string]any)
			return bool(params["drain"].(graphql.Boolean))
		}),
		mock.Anything,
	).Run(func(args mock.Arguments) {
		args.Get(1).(*internal.WorkerDrainSet).Worker = internal.Worker{Busy: true}
	}).Return(nil)

	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			params := in.(map[string]any)
			return !bool(params["drain"].(graphql.Boolean))
		}),
		mock.Anything,
	).Return(nil)

	drained, err := sut.DrainWorker(t.Context(), workerID)

	require.False(t, drained)
	require.NoError(t, err)
}

// KillInstance tests

func TestKillInstance_DetachCallFails_SendsCorrectInput(t *testing.T) {
	const instanceID = "test-instance"

	sut, mockAutoscaling, _, _ := setupController()

	var capturedInput *autoscaling.DetachInstancesInput
	mockAutoscaling.On(
		"DetachInstances",
		mock.Anything,
		mock.MatchedBy(func(in *autoscaling.DetachInstancesInput) bool {
			capturedInput = in
			return true
		}),
		mock.Anything,
	).Return(nil, errors.New("bacon"))

	_ = sut.KillInstance(t.Context(), instanceID)

	require.NotNil(t, capturedInput)
	require.Contains(t, capturedInput.InstanceIds, instanceID)
	require.Equal(t, asgName, *capturedInput.AutoScalingGroupName)
	require.True(t, *capturedInput.ShouldDecrementDesiredCapacity)
}

func TestKillInstance_DetachCallFails_ReturnsError(t *testing.T) {
	const instanceID = "test-instance"

	sut, mockAutoscaling, _, _ := setupController()

	mockAutoscaling.On("DetachInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("bacon"))

	err := sut.KillInstance(t.Context(), instanceID)

	require.EqualError(t, err, "could not detach instance from autoscaling group: bacon")
}

func TestKillInstance_InstanceNotPartOfASG_ReturnsError(t *testing.T) {
	const instanceID = "test-instance"

	sut, mockAutoscaling, mockEC2, _ := setupController()

	mockAutoscaling.On("DetachInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("instance is not part of Auto Scaling group"))

	mockEC2.On("TerminateInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("bacon"))

	err := sut.KillInstance(t.Context(), instanceID)

	require.EqualError(t, err, "could not terminate detached instance: bacon")
}

func TestKillInstance_TerminateCallFails_SendsCorrectInput(t *testing.T) {
	const instanceID = "test-instance"

	sut, mockAutoscaling, mockEC2, _ := setupController()

	mockAutoscaling.On("DetachInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	var capturedInput *ec2.TerminateInstancesInput
	mockEC2.On(
		"TerminateInstances",
		mock.Anything,
		mock.MatchedBy(func(in *ec2.TerminateInstancesInput) bool {
			capturedInput = in
			return true
		}),
		mock.Anything,
	).Return(nil, errors.New("bacon"))

	_ = sut.KillInstance(t.Context(), instanceID)

	require.NotNil(t, capturedInput)
	require.Contains(t, capturedInput.InstanceIds, instanceID)
}

func TestKillInstance_TerminateCallFails_ReturnsError(t *testing.T) {
	const instanceID = "test-instance"

	sut, mockAutoscaling, mockEC2, _ := setupController()

	mockAutoscaling.On("DetachInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	mockEC2.On("TerminateInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("bacon"))

	err := sut.KillInstance(t.Context(), instanceID)

	require.EqualError(t, err, "could not terminate detached instance: bacon")
}

func TestKillInstance_Success_NoError(t *testing.T) {
	const instanceID = "test-instance"

	sut, mockAutoscaling, mockEC2, _ := setupController()

	mockAutoscaling.On("DetachInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	mockEC2.On("TerminateInstances", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	err := sut.KillInstance(t.Context(), instanceID)

	require.NoError(t, err)
}

// ScaleUpASG tests

func TestScaleUpASG_SetCapacityCallFails_SendsCorrectInput(t *testing.T) {
	const desiredCapacity = 42

	sut, mockAutoscaling, _, _ := setupController()

	var capturedInput *autoscaling.SetDesiredCapacityInput
	mockAutoscaling.On(
		"SetDesiredCapacity",
		mock.Anything,
		mock.MatchedBy(func(in *autoscaling.SetDesiredCapacityInput) bool {
			capturedInput = in
			return true
		}),
		mock.Anything,
	).Return(nil, errors.New("bacon"))

	_ = sut.ScaleUpASG(t.Context(), desiredCapacity)

	require.NotNil(t, capturedInput)
	require.Equal(t, asgName, *capturedInput.AutoScalingGroupName)
	require.EqualValues(t, desiredCapacity, *capturedInput.DesiredCapacity)
}

func TestScaleUpASG_SetCapacityCallFails_ReturnsError(t *testing.T) {
	const desiredCapacity = 42

	sut, mockAutoscaling, _, _ := setupController()

	mockAutoscaling.On("SetDesiredCapacity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("bacon"))

	err := sut.ScaleUpASG(t.Context(), desiredCapacity)

	require.EqualError(t, err, "could not set desired capacity: bacon")
}

func TestScaleUpASG_Success_NoError(t *testing.T) {
	const desiredCapacity = 42

	sut, mockAutoscaling, _, _ := setupController()

	mockAutoscaling.On("SetDesiredCapacity", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, nil)

	err := sut.ScaleUpASG(t.Context(), desiredCapacity)

	require.NoError(t, err)
}
