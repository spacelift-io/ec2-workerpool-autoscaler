package internal

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type Instance struct {
	ID             string
	LaunchTime     time.Time
	LifecycleState string
}

type AutoScalingGroup struct {
	Name            string
	MinSize         int
	MaxSize         int
	DesiredCapacity int
	Instances       []Instance
}

//go:generate mockery --output ./ --name ControllerInterface --filename mock_controller_test.go --outpkg internal_test --structname MockController
type ControllerInterface interface {
	DescribeInstances(ctx context.Context, instanceIDs []string) (instances []Instance, err error)
	GetAutoscalingGroup(ctx context.Context) (out *AutoScalingGroup, err error)
	GetWorkerPool(ctx context.Context) (out *WorkerPool, err error)
	DrainWorker(ctx context.Context, workerID string) (drained bool, err error)
	// KillInstance terminates an instance and removes it from the scaling group.
	// Implementations MUST ensure that the group's desired capacity is decremented
	// as part of this operation (either explicitly or via platform auto-adjustment).
	// The caller does NOT separately adjust capacity during scale-down.
	KillInstance(ctx context.Context, instanceID string) (err error)
	ScaleUpASG(ctx context.Context, desiredCapacity int) (err error)
	InstanceIdentity(worker *Worker) (groupID GroupID, instanceID InstanceID, err error)
	Close() error // Release resources when done
}

type AutoScaler struct {
	controller ControllerInterface
	logger     *slog.Logger
}

func NewAutoScaler(controller ControllerInterface, logger *slog.Logger) *AutoScaler {
	return &AutoScaler{controller: controller, logger: logger}
}

func (s AutoScaler) Scale(ctx context.Context, cfg RuntimeConfig) error {
	error_count := 0

	groupKey, groupID := cfg.GroupKeyAndID()
	logger := s.logger.With(
		groupKey, groupID,
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

	state, err := NewState(workerPool, asg, cfg, logger, s.controller)
	if err != nil {
		return fmt.Errorf("could not create state: %w", err)
	}

	// Sanity check: Detect if ASG desired capacity is unreasonably high.
	// This can happen during AWS service disruptions when AWS sets incorrect capacity.
	// Calculate what we'd expect: valid workers + pending runs + generous buffer for scaling headroom
	expectedMaxCapacity := state.ValidWorkerCount() + int(workerPool.PendingRuns) + cfg.AutoscalingMaxCreate

	// Only trigger if capacity is SIGNIFICANTLY higher than expected
	// to avoid false positives during normal scaling operations
	sanityThreshold := cfg.AutoscalingCapacitySanityCheck
	if sanityThreshold == 0 {
		sanityThreshold = 10 // Default threshold if not configured
	}
	excessCapacity := asg.DesiredCapacity - expectedMaxCapacity
	if excessCapacity >= sanityThreshold && asg.DesiredCapacity > asg.MinSize {
		logger.Error("desired capacity is suspiciously high, possible cloud provider issue or external modification",
			"desired_capacity", asg.DesiredCapacity,
			"valid_workers", state.ValidWorkerCount(),
			"pending_runs", workerPool.PendingRuns,
			"expected_max_capacity", expectedMaxCapacity,
			"difference", asg.DesiredCapacity-expectedMaxCapacity,
		)

		// Reset to a sane value: valid workers + pending runs (capped by max size)
		saneCapacity := state.ValidWorkerCount() + int(workerPool.PendingRuns)
		if saneCapacity < asg.MinSize {
			saneCapacity = asg.MinSize
		}
		if saneCapacity > asg.MaxSize {
			saneCapacity = asg.MaxSize
		}

		logger.Warn("attempting to reset desired capacity to sane value",
			"current_capacity", asg.DesiredCapacity,
			"new_capacity", saneCapacity)

		if err := s.controller.ScaleUpASG(ctx, saneCapacity); err != nil {
			logger.Error("failed to reset desired capacity", "error", err)
			// Don't return error - continue with normal scaling logic
		} else {
			logger.Info("successfully reset desired capacity")
			// Update our local ASG state to reflect the change
			asg.DesiredCapacity = saneCapacity
		}
	}

	// Let's make sure that for each of the in-service instances we have a
	// corresponding worker in Spacelift, or that we have "stray" machines.
	if strayInstances := state.StrayInstances(); len(strayInstances) > 0 {
		// There's a question of what to do with the "stray" machines. The
		// decision will be made based on the creation timestamp.
		instances, err := s.controller.DescribeInstances(ctx, strayInstances)
		if err != nil {
			return fmt.Errorf("could not list instances: %w", err)
		}

		for _, instance := range instances {
			logger := logger.With("instance_id", instance.ID)
			instanceAge := time.Since(instance.LaunchTime)

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

				if err := s.controller.KillInstance(ctx, instance.ID); err != nil {
					logger.Error("could not kill stray instance", "error", err)
					error_count++
					continue
				}

				logger.Info("instance successfully removed from the ASG and terminated")
			}
		}
	}

	decision := state.Decide(cfg.AutoscalingMaxCreate, cfg.AutoscalingMaxKill)

	logger = logger.With(
		"asg_instances", len(asg.Instances),
		"asg_desired_capacity", asg.DesiredCapacity,
		"scaling_decision_comments", decision.Comments,
		"spacelift_workers", len(workerPool.Workers),
		"spacelift_pending_runs", workerPool.PendingRuns,
	)

	if decision.ScalingDirection == ScalingDirectionNone {
		logger.Info("not scaling the ASG")

		if error_count > 0 {
			return fmt.Errorf("encountered %d errors", error_count)
		}

		return nil
	}

	if decision.ScalingDirection == ScalingDirectionUp {
		logger.With("instances", decision.ScalingSize).Info("scaling up the ASG")

		if err := s.controller.ScaleUpASG(ctx, asg.DesiredCapacity+decision.ScalingSize); err != nil {
			logger.Error("could not scale up ASG: %w", "error", err)
			error_count++
		}

		if error_count > 0 {
			return fmt.Errorf("encountered %d errors during scale-up", error_count)
		}

		return nil
	}

	// If we got this far, we're scaling down.
	logger.With("instances", decision.ScalingSize).Info("scaling down ASG")

	scalableWorkers := state.ScalableWorkers()

	for i := 0; i < decision.ScalingSize; i++ {
		worker := scalableWorkers[i]

		_, instanceID, _ := s.controller.InstanceIdentity(&worker)

		logger := logger.With(
			"worker_id", worker.ID,
			"instance_id", instanceID,
		)
		logger.Info("scaling down ASG and killing worker")

		drained, err := s.controller.DrainWorker(ctx, worker.ID)
		if err != nil {
			logger.Error("could not drain worker", "error", err)
			error_count++
			continue
		}

		if !drained {
			logger.Warn("worker was busy; skipping termination")
			continue
		}

		if err := s.controller.KillInstance(ctx, string(instanceID)); err != nil {
			logger.Error("could not kill instance", "error", err)
			error_count++
			continue
		}
	}

	if error_count > 0 {
		return fmt.Errorf("encountered %d errors during scale-down", error_count)
	}

	return nil
}
