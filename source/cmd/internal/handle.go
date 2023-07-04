package internal

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-xray-sdk-go/xray"
	"github.com/caarlos0/env"
	"golang.org/x/exp/slog"

	"github.com/spacelift-io/awsautoscalr/internal"
)

func Handle(ctx context.Context, logger *slog.Logger) error {
	var cfg internal.RuntimeConfig
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("could not parse environment variables: %w", err)
	}

	logger = logger.With(
		"asg_id", cfg.AutoscalingGroupName,
		"worker_pool_id", cfg.SpaceliftWorkerPoolID,
	)

	if err := xray.Configure(xray.Config{ServiceVersion: "1.2.3"}); err != nil {
		return fmt.Errorf("could not configure X-Ray: %w", err)
	}

	controller, err := internal.NewController(ctx, &cfg)
	if err != nil {
		return fmt.Errorf("could not create controller: %w", err)
	}

	workerPool, err := controller.GetWorkerPool(ctx)
	if err != nil {
		return fmt.Errorf("could not get worker pool: %w", err)
	}

	asg, err := controller.GetAutoscalingGroup(ctx)
	if err != nil {
		return fmt.Errorf("could not get autoscaling group: %w", err)
	}

	state, err := internal.NewState(workerPool, asg)
	if err != nil {
		return fmt.Errorf("could not create state: %w", err)
	}

	// Let's make sure that for each of the in-service instances we have a
	// corresponding worker in Spacelift, or that we have "stray" machines.
	if strayInstances := state.StrayInstances(); len(strayInstances) > 0 {
		// There's a question of what to do with the "stray" machines. The
		// decision will be made based on the creation timestamp.
		instances, err := controller.DescribeInstances(ctx, strayInstances)
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

				if err := controller.KillInstance(ctx, *instance.InstanceId); err != nil {
					return fmt.Errorf("could not kill instance: %w", err)
				}

				// We don't want to kill too many instances at once, so let's
				// return after the first successfully killed one.
				logger.Info("instance successfully removed from the ASG and terminated", *instance.InstanceId)

				return nil
			}
		}
	}

	decision := state.Decide(cfg.AutoscalingMaxCreate, cfg.AutoscalingMaxKill)

	if decision.ScalingDirection == internal.ScalingDirectionNone {
		logger.Info("no scaling decision to be made")
		return nil
	}

	if decision.ScalingDirection == internal.ScalingDirectionUp {
		logger.Info("scaling up ASG by %d instances", decision.ScalingSize)

		if err := controller.ScaleUpASG(ctx, *asg.DesiredCapacity+int32(decision.ScalingSize)); err != nil {
			return fmt.Errorf("could not scale up ASG: %w", err)
		}

		return nil
	}

	// If we got this far, we're scaling down.
	logger.Info("scaling down ASG by %d instances", decision.ScalingSize)

	idleWorkers := state.IdleWorkers()

	for i := 0; i < decision.ScalingSize; i++ {
		worker := idleWorkers[i]

		_, instanceID, _ := worker.InstanceIdentity()

		logger = logger.With(
			"worker_id", worker.ID,
			"instance_id", instanceID,
		)
		logger.Info("scaling down ASG and killing worker")

		drained, err := controller.DrainWorker(ctx, worker.ID)
		if err != nil {
			return fmt.Errorf("could not drain worker: %w", err)
		}

		if !drained {
			logger.Warn("worker was busy, stopping the scaling down process")
			return nil
		}

		if err := controller.KillInstance(ctx, string(instanceID)); err != nil {
			return fmt.Errorf("could not kill instance: %w", err)
		}
	}

	return nil
}
