package internal

import (
	"context"
	"fmt"
	"time"

	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"golang.org/x/exp/slog"
)

//go:generate mockery --output ./ --name ControllerInterface --filename mock_controller_test.go --outpkg internal_test
type ControllerInterface interface {
	DescribeInstances(ctx context.Context, instanceIDs []string) (instances []ec2types.Instance, err error)
	GetAutoscalingGroup(ctx context.Context) (out *autoscalingtypes.AutoScalingGroup, err error)
	GetWorkerPool(ctx context.Context) (out *WorkerPool, err error)
	DrainWorker(ctx context.Context, workerID string) (drained bool, err error)
	KillInstance(ctx context.Context, instanceID string) (err error)
	ScaleUpASG(ctx context.Context, desiredCapacity int32) (err error)
}

type AutoScaler struct {
	controller ControllerInterface
	logger     *slog.Logger
}

func NewAutoScaler(controller ControllerInterface, logger *slog.Logger) *AutoScaler {
	return &AutoScaler{controller: controller, logger: logger}
}

func (s AutoScaler) Scale(ctx context.Context, cfg RuntimeConfig) error {
	logger := s.logger.With(
		"asg_arn", cfg.AutoscalingGroupARN,
		"worker_pool_id", cfg.SpaceliftWorkerPoolID,
	)

	workerPool, err := s.controller.GetWorkerPool(ctx)
	if err != nil {
		return fmt.Errorf("could not get worker pool: %w", err)
	}

	asg, err := s.controller.GetAutoscalingGroup(ctx)
	if err != nil {
		return fmt.Errorf("could not get autoscaling group: %w", err)
	}

	state, err := NewState(workerPool, asg)
	if err != nil {
		return fmt.Errorf("could not create state: %w", err)
	}

	// Let's make sure that for each of the in-service instances we have a
	// corresponding worker in Spacelift, or that we have "stray" machines.
	if strayInstances := state.StrayInstances(); len(strayInstances) > 0 {
		// There's a question of what to do with the "stray" machines. The
		// decision will be made based on the creation timestamp.
		instances, err := s.controller.DescribeInstances(ctx, strayInstances)
		if err != nil {
			return fmt.Errorf("could not list EC2 instances: %w", err)
		}

		for _, instance := range instances {
			logger = logger.With("instance_id", *instance.InstanceId)
			instanceAge := time.Since(*instance.LaunchTime)

			logger = logger.With(
				"launch_timestamp", instance.LaunchTime.Unix(),
				"instance_age", instanceAge,
			)

			// If the machine was only created recently (say a generous window of 10
			// minutes), it is possible that it hasn't managed to register itself with
			// Spacelift yet. But if it's been around for a while we will want to kill
			// it and remove it from the ASG.
			if instanceAge > 10*time.Minute {
				logger.Warn("instance has no corresponding worker in Spacelift, removing from the ASG")

				if err := s.controller.KillInstance(ctx, *instance.InstanceId); err != nil {
					return fmt.Errorf("could not kill instance: %w", err)
				}

				// We don't want to kill too many instances at once, so let's
				// return after the first successfully killed one.
				logger.Info("instance successfully removed from the ASG and terminated")

				return nil
			}
		}
	}

	decision := state.Decide(cfg.AutoscalingMaxCreate, cfg.AutoscalingMaxKill)

	if decision.ScalingDirection == ScalingDirectionNone {
		logger.Info("no scaling decision to be made")
		return nil
	}

	if decision.ScalingDirection == ScalingDirectionUp {
		logger.With("instances", decision.ScalingSize).Info("scaling up the ASG")

		if err := s.controller.ScaleUpASG(ctx, *asg.DesiredCapacity+int32(decision.ScalingSize)); err != nil {
			return fmt.Errorf("could not scale up ASG: %w", err)
		}

		return nil
	}

	// If we got this far, we're scaling down.
	logger.With("instances", decision.ScalingSize).Info("scaling down ASG")

	idleWorkers := state.IdleWorkers()

	for i := 0; i < decision.ScalingSize; i++ {
		worker := idleWorkers[i]

		_, instanceID, _ := worker.InstanceIdentity()

		logger = logger.With(
			"worker_id", worker.ID,
			"instance_id", instanceID,
		)
		logger.Info("scaling down ASG and killing worker")

		drained, err := s.controller.DrainWorker(ctx, worker.ID)
		if err != nil {
			return fmt.Errorf("could not drain worker: %w", err)
		}

		if !drained {
			logger.Warn("worker was busy, stopping the scaling down process")
			return nil
		}

		if err := s.controller.KillInstance(ctx, string(instanceID)); err != nil {
			return fmt.Errorf("could not kill instance: %w", err)
		}
	}

	return nil
}
