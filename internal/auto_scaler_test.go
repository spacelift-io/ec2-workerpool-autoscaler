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
