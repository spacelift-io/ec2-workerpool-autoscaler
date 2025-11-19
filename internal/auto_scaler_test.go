package internal_test

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/spacelift-io/awsautoscalr/internal"
)

func TestAutoScalerScalingNone(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := internal.RuntimeConfig{}

	ctrl := new(MockController)
	defer ctrl.AssertExpectations(t)

	scaler := internal.NewAutoScaler(ctrl, slog.New(h))

	ctrl.On("GetWorkerPool", mock.Anything).Return(&internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID:       "1",
				Metadata: `{"asg_id": "group", "instance_id": "instance"}`,
			},
		},
	}, nil)
	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&internal.AutoScalingGroup{
		Name:            "group",
		MinSize:         1,
		MaxSize:         3,
		DesiredCapacity: 2,
	}, nil)
	err := scaler.Scale(t.Context(), cfg)
	require.NoError(t, err)
}

func TestAutoScalerScalingUp(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := internal.RuntimeConfig{}

	ctrl := new(MockController)
	defer ctrl.AssertExpectations(t)

	scaler := internal.NewAutoScaler(ctrl, slog.New(h))

	ctrl.On("GetWorkerPool", mock.Anything).Return(&internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID:       "1",
				Metadata: `{"asg_id": "group", "instance_id": "instance"}`,
			},
		},
		PendingRuns: 2,
	}, nil)
	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&internal.AutoScalingGroup{
		Name:            "group",
		MinSize:         1,
		MaxSize:         3,
		DesiredCapacity: 2,
		Instances: []internal.Instance{
			{ID: "instance"},
		},
	}, nil)
	ctrl.On("ScaleUpASG", mock.Anything, 2).Return(nil)
	err := scaler.Scale(t.Context(), cfg)
	require.NoError(t, err)
}

func TestAutoScalerScalingDown(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := internal.RuntimeConfig{
		AutoscalingMaxKill: 1,
	}

	ctrl := new(MockController)
	defer ctrl.AssertExpectations(t)

	scaler := internal.NewAutoScaler(ctrl, slog.New(h))

	ctrl.On("GetWorkerPool", mock.Anything).Return(&internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID:       "1",
				Metadata: `{"asg_id": "group", "instance_id": "instance"}`,
			},
			{
				ID:       "2",
				Metadata: `{"asg_id": "group", "instance_id": "instance2"}`,
			},
		},
	}, nil)
	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&internal.AutoScalingGroup{
		Name:            "group",
		MinSize:         1,
		MaxSize:         3,
		DesiredCapacity: 2,
		Instances: []internal.Instance{
			{ID: "instance"},
			{ID: "instance2"},
		},
	}, nil)
	ctrl.On("DrainWorker", mock.Anything, "1").Return(true, nil)
	ctrl.On("KillInstance", mock.Anything, "instance").Return(nil)
	err := scaler.Scale(t.Context(), cfg)
	require.NoError(t, err)
}

func TestAutoScalerDetachedNotTerminatedInstances(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := internal.RuntimeConfig{}

	ctrl := new(MockController)
	defer ctrl.AssertExpectations(t)

	scaler := internal.NewAutoScaler(ctrl, slog.New(h))

	ctrl.On("GetWorkerPool", mock.Anything).Return(&internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID:       "1",
				Metadata: `{"asg_id": "group", "instance_id": "instance"}`,
			},
			{
				ID:       "2",
				Drained:  true,
				Metadata: `{"asg_id": "group", "instance_id": "detached"}`,
			},
		},
		PendingRuns: 2,
	}, nil)
	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&internal.AutoScalingGroup{
		Name:            "group",
		MinSize:         1,
		MaxSize:         3,
		DesiredCapacity: 2,
		Instances: []internal.Instance{
			{ID: "instance"},
		},
	}, nil)
	ctrl.On("KillInstance", mock.Anything, "detached").Return(nil)
	output := []internal.Instance{{
		ID:         "detached",
		LaunchTime: time.Now().Add(-time.Hour),
	}}
	ctrl.On(
		"DescribeInstances",
		mock.Anything,
		[]string{"detached"},
	).Return(output, nil)
	err := scaler.Scale(t.Context(), cfg)
	require.NoError(t, err)
}
func TestAutoScalerDesiredCapacitySanityCheck(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := internal.RuntimeConfig{
		AutoscalingMaxCreate: 20,
	}

	ctrl := new(MockController)
	defer ctrl.AssertExpectations(t)

	scaler := internal.NewAutoScaler(ctrl, slog.New(h))

	// Simulate AWS outage scenario: ASG desired capacity is 79,
	// but we only have 3 workers and 5 pending runs
	ctrl.On("GetWorkerPool", mock.Anything).Return(&internal.WorkerPool{
		Workers: []internal.Worker{
			{
				ID:       "1",
				Metadata: `{"asg_id": "group", "instance_id": "i-1"}`,
			},
			{
				ID:       "2",
				Metadata: `{"asg_id": "group", "instance_id": "i-2"}`,
			},
			{
				ID:       "3",
				Metadata: `{"asg_id": "group", "instance_id": "i-3"}`,
			},
		},
		PendingRuns: 5,
	}, nil)

	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&internal.AutoScalingGroup{
		Name:            "group",
		MinSize:         3,
		MaxSize:         100,
		DesiredCapacity: 79, // Suspiciously high!
		Instances: []internal.Instance{
			{ID: "i-1"},
			{ID: "i-2"},
			{ID: "i-3"},
		},
	}, nil)

	// Expected behavior:
	// 1. Autoscaler should first reset capacity to 8 (3 workers + 5 pending runs)
	// 2. Then normal scaling logic continues: with 3 idle workers and 5 pending runs,
	//    it needs 2 more workers, so scales from 8 to 10
	ctrl.On("ScaleUpASG", mock.Anything, 8).Return(nil).Once()
	ctrl.On("ScaleUpASG", mock.Anything, 10).Return(nil).Once()

	err := scaler.Scale(t.Context(), cfg)
	require.NoError(t, err)

	// Verify that error logs contain the sanity check message
	logOutput := buf.String()
	require.Contains(t, logOutput, "ASG desired capacity is suspiciously high")
	require.Contains(t, logOutput, "attempting to reset ASG desired capacity")
}

func TestAutoScalerDesiredCapacitySanityCheckRespectsMinSize(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := internal.RuntimeConfig{
		AutoscalingMaxCreate: 20,
	}

	ctrl := new(MockController)
	defer ctrl.AssertExpectations(t)

	scaler := internal.NewAutoScaler(ctrl, slog.New(h))

	// ASG desired capacity is 50, but we have 0 workers and 0 pending runs
	// Reset should respect min size of 3
	ctrl.On("GetWorkerPool", mock.Anything).Return(&internal.WorkerPool{
		Workers:     []internal.Worker{},
		PendingRuns: 0,
	}, nil)

	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&internal.AutoScalingGroup{
		Name:            "group",
		MinSize:         3,
		MaxSize:         100,
		DesiredCapacity: 50, // Suspiciously high!
		Instances:       []internal.Instance{},
	}, nil)

	// Expected behavior: autoscaler should reset capacity to 3 (min size)
	ctrl.On("ScaleUpASG", mock.Anything, 3).Return(nil)

	err := scaler.Scale(t.Context(), cfg)
	require.NoError(t, err)
}

func TestAutoScalerDesiredCapacitySanityCheckNoResetWhenReasonable(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := internal.RuntimeConfig{
		AutoscalingMaxCreate: 20,
	}

	ctrl := new(MockController)
	defer ctrl.AssertExpectations(t)

	scaler := internal.NewAutoScaler(ctrl, slog.New(h))

	// ASG desired capacity is 10, we have 8 workers and 2 pending runs
	// This is reasonable (8 + 2 = 10), so no reset should occur
	ctrl.On("GetWorkerPool", mock.Anything).Return(&internal.WorkerPool{
		Workers: []internal.Worker{
			{ID: "1", Metadata: `{"asg_id": "group", "instance_id": "i-1"}`},
			{ID: "2", Metadata: `{"asg_id": "group", "instance_id": "i-2"}`},
			{ID: "3", Metadata: `{"asg_id": "group", "instance_id": "i-3"}`},
			{ID: "4", Metadata: `{"asg_id": "group", "instance_id": "i-4"}`},
			{ID: "5", Metadata: `{"asg_id": "group", "instance_id": "i-5"}`},
			{ID: "6", Metadata: `{"asg_id": "group", "instance_id": "i-6"}`},
			{ID: "7", Metadata: `{"asg_id": "group", "instance_id": "i-7"}`},
			{ID: "8", Metadata: `{"asg_id": "group", "instance_id": "i-8"}`},
		},
		PendingRuns: 2,
	}, nil)

	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&internal.AutoScalingGroup{
		Name:            "group",
		MinSize:         3,
		MaxSize:         100,
		DesiredCapacity: 10,
		Instances: []internal.Instance{
			{ID: "i-1"}, {ID: "i-2"}, {ID: "i-3"}, {ID: "i-4"},
			{ID: "i-5"}, {ID: "i-6"}, {ID: "i-7"}, {ID: "i-8"},
		},
	}, nil)

	// No ScaleUpASG call expected for sanity check - capacity is reasonable

	err := scaler.Scale(t.Context(), cfg)
	require.NoError(t, err)

	// Verify that error logs do NOT contain the sanity check message
	logOutput := buf.String()
	require.NotContains(t, logOutput, "ASG desired capacity is suspiciously high")
}
