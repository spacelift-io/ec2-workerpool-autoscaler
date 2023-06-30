package main

import (
	"context"
	"os"
	"time"

	"github.com/aws/aws-xray-sdk-go/xray"
	"github.com/caarlos0/env"
	"golang.org/x/exp/slog"

	"gh.com/mw/autoscalr/internal"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	var cfg internal.RuntimeConfig
	if err := env.Parse(&cfg); err != nil {
		logger.Error("could not parse environment variables: %v", err)
	}

	logger = logger.With(
		"asg_id", cfg.AutoscalingGroupName,
		"worker_pool_id", cfg.SpaceliftWorkerPoolID,
	)

	if err := xray.Configure(xray.Config{ServiceVersion: "1.2.3"}); err != nil {
		logger.Error("could not configure X-Ray: %v", err)
		os.Exit(1)
	}

	ctx := context.Background()

	controller, err := internal.NewController(ctx, &cfg)
	if err != nil {
		logger.Error("could not create controller: %v", err)
		os.Exit(1)
	}

	workerPool, err := controller.GetWorkerPool(ctx)
	if err != nil {
		logger.Error("could not get worker pool: %v", err)
		os.Exit(1)
	}

	asg, err := controller.GetAutoscalingGroup(ctx)
	if err != nil {
		logger.Error("could not get autoscaling group: %v", err)
		os.Exit(1)
	}

	state, err := internal.NewState(workerPool, asg)
	if err != nil {
		logger.Error("could not create state: %v", err)
		os.Exit(1)
	}

	// Let's make sure that for each of the in-service instances we have a
	// corresponding worker in Spacelift, or that we have "stray" machines.
	if strayInstances := state.StrayInstances(); len(strayInstances) > 0 {
		// There's a question of what to do with the "stray" machines. The
		// decision will be made based on the creation timestamp.
		instances, err := controller.DescribeInstances(ctx, strayInstances)
		if err != nil {
			logger.Error("could not list EC2 instances: %v", err)
			os.Exit(1)
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
					logger.Error("could not kill instance: %v", err)
					os.Exit(1)
				}

				// We don't want to kill too many instances at once, so let's
				// return after the first successfully killed one.
				logger.Info("instance successfully removed from the ASG and terminated", *instance.InstanceId)
				return
			}
		}
	}

	// Let's make sure that the number of workers matches the number of
	// instances in the ASG. Previously we covered a scenario where there are
	// "dead" instances without a corresponding worker, but there could also be
	// workers without a corresponding instance. This could happen if the worker
	// has crashed and we haven't yet marked it as gone. If this is the case,
	// we want to skip performing any scaling operations and just wait for
	// things to settle down.
	if len(workerPool.Workers) != len(asg.Instances) {
		logger.With(
			"workers", len(workerPool.Workers),
			"instances", len(asg.Instances),
		).Warn("number of workers does not match the number of instances in the ASG")

		return
	}

	// If we got this far, we can get to our main business of scaling.
	// The logic is as follows:
	//
	// 1. We look at the total number of workers. If it's at the minimum or the
	// maximum already, we do nothing.
	if len(workerPool.Workers) >= int(*asg.MaxSize) {
		logger.Warn("autoscaling group is already at maximum size")
		return
	}

	if len(workerPool.Workers) <= int(*asg.MinSize) {
		logger.Warn("autoscaling group is already at minimum size")
		return
	}

	idle := state.IdleWorkers()

	logger.With(
		"desired_asg_capacity", *asg.DesiredCapacity,
		"max_asg_size", *asg.MaxSize,
		"min_asg_size", *asg.MinSize,
		"pending_runs", int(workerPool.PendingRuns),
		"idle_workers", len(idle),
	).Debug("determining scaling action")

	// 2. We then look at the queue and calculate the difference between the
	// number of idle workers and the number of queued tasks.
	difference := int(workerPool.PendingRuns) - len(idle)

	switch {
	case difference == 0:
		// If there's no difference, we do nothing.
		logger.Info("autoscaling group exactly at the right size")
		return
	case difference > 0:
		// If there are more pending runs than idle workers, we need to scale up.
		// We will scale up by the difference, but there are two constraints:
		//
		// - we should not spin up more machines than the maximum capacity of the ASG;
		// - we should not spin up more machines at once than the maximum declared
		//   by the user.
		spinUpBy := int32(difference)
		if difference > cfg.AutoscalingMaxCreate {
			spinUpBy = int32(cfg.AutoscalingMaxCreate)
		}

		newASGCapacity := *asg.DesiredCapacity + spinUpBy
		if newASGCapacity > *asg.MaxSize {
			newASGCapacity = *asg.MaxSize
		}

		logger = logger.With("new_capacity", newASGCapacity)
		logger.Info("scaling up")

		if err = controller.ScaleUpASG(ctx, newASGCapacity); err != nil {
			logger.Error("could not scale autoscaling group: %v", err)
			os.Exit(1)
		}

		logger.Info("scaled up successfully")
		return
	case difference < 0:
		// If the number of idle workers is greater than the number of pending
		// runs, we need to scale down. We will scale down by the difference,
		// but there are two constraints:
		// - we should not spin down more machines than the minimum capacity of the ASG;
		// - we should not spin down more machines at once than the maximum declared
		//   by the user.
		killCount := -difference
		if killCount > cfg.AutoscalingMaxKill {
			killCount = cfg.AutoscalingMaxKill
		}

		// Check how many we can kill without going below the minimum capacity.
		overMinimum := *asg.DesiredCapacity - *asg.MinSize
		if killCount > int(overMinimum) {
			killCount = int(overMinimum)
		}

		logger.Info("killing up to %d workers", killCount)

		for i := 0; i < killCount; i++ {
			worker := idle[i]
			_, instanceID, _ := worker.InstanceIdentity()

			logger = logger.With(
				"worker_id", worker.ID,
				"instance_id", instanceID,
				"already_killed", i,
			)

			logger.Info("killing worker")

			drained, err := controller.DrainWorker(ctx, worker.ID)
			if err != nil {
				logger.Error("could not drain worker: %v", err)
				os.Exit(1)
			}

			if !drained {
				logger.Warn("idle worker got a job, not killing any more")
				return
			}

			if err := controller.KillInstance(ctx, string(instanceID)); err != nil {
				logger.Error("could not kill instance: %v", err)
				os.Exit(1)
			}

			logger.Info("worker killed successfully")
		}
	}
}
