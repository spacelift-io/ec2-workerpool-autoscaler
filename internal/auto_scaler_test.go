package internal

import (
	"bytes"
	"context"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/exp/slog"
	"testing"
)

func TestAutoScalerScalingNone(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := RuntimeConfig{}

	ctrl := new(MockControllerInterface)
	defer ctrl.AssertExpectations(t)

	scaler := NewAutoScaler(ctrl, slog.New(h))

	ctrl.On("GetWorkerPool", mock.Anything).Return(&WorkerPool{
		Workers: []Worker{
			{
				ID:       "1",
				Metadata: `{"asg_id": "group", "instance_id": "instance"}`,
			},
		},
	}, nil)
	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&types.AutoScalingGroup{
		AutoScalingGroupName: ptr("group"),
		MinSize:              ptr(int32(1)),
		MaxSize:              ptr(int32(3)),
		DesiredCapacity:      ptr(int32(2)),
	}, nil)
	err := scaler.Scale(context.Background(), cfg)
	require.NoError(t, err)
}

func TestAutoScalerScalingUp(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := RuntimeConfig{}

	ctrl := new(MockControllerInterface)
	defer ctrl.AssertExpectations(t)

	scaler := NewAutoScaler(ctrl, slog.New(h))

	ctrl.On("GetWorkerPool", mock.Anything).Return(&WorkerPool{
		Workers: []Worker{
			{
				ID:       "1",
				Metadata: `{"asg_id": "group", "instance_id": "instance"}`,
			},
		},
		PendingRuns: 2,
	}, nil)
	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&types.AutoScalingGroup{
		AutoScalingGroupName: ptr("group"),
		MinSize:              ptr(int32(1)),
		MaxSize:              ptr(int32(3)),
		DesiredCapacity:      ptr(int32(2)),
		Instances: []types.Instance{
			{},
		},
	}, nil)
	ctrl.On("ScaleUpASG", mock.Anything, int32(2)).Return(nil)
	err := scaler.Scale(context.Background(), cfg)
	require.NoError(t, err)
}

func TestAutoScalerScalingDown(t *testing.T) {
	var buf bytes.Buffer
	h := slog.NewTextHandler(&buf, nil)

	cfg := RuntimeConfig{
		AutoscalingMaxKill: 1,
	}

	ctrl := new(MockControllerInterface)
	defer ctrl.AssertExpectations(t)

	scaler := NewAutoScaler(ctrl, slog.New(h))

	ctrl.On("GetWorkerPool", mock.Anything).Return(&WorkerPool{
		Workers: []Worker{
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
	ctrl.On("GetAutoscalingGroup", mock.Anything).Return(&types.AutoScalingGroup{
		AutoScalingGroupName: ptr("group"),
		MinSize:              ptr(int32(1)),
		MaxSize:              ptr(int32(3)),
		DesiredCapacity:      ptr(int32(2)),
		Instances: []types.Instance{
			{},
			{},
		},
	}, nil)
	ctrl.On("DrainWorker", mock.Anything, "1").Return(true, nil)
	ctrl.On("KillInstance", mock.Anything, "instance").Return(nil)
	err := scaler.Scale(context.Background(), cfg)
	require.NoError(t, err)
}

func ptr[T any](v T) *T {
	return &v
}
