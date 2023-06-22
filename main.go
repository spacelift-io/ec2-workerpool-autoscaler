package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/caarlos0/env"
	"github.com/spacelift-io/spacectl/client"
	"github.com/spacelift-io/spacectl/client/session"
	"golang.org/x/exp/slog"

	"gh.com/mw/autoscalr/internal"
)

type runtimeConfig struct {
	SpaceliftAPIKeyID     string `env:"SPACELIFT_API_KEY_ID,notEmpty"`
	SpaceliftAPISecret    string `env:"SPACELIFT_API_SECRET,notEmpty"`
	SpaceliftAPIEndpoint  string `env:"SPACELIFT_API_ENDPOINT,notEmpty"`
	SpaceliftWorkerPoolID string `env:"SPACELIFT_WORKER_POOL_ID,notEmpty"`

	AutoscalingGroupID   string `env:"AUTOSCALING_GROUP_ID,notEmpty"`
	AutoscalingRegion    string `env:"AUTOSCALING_REGION,notEmpty"`
	AutoscalingMaxKill   int    `env:"AUTOSCALING_MAX_KILL" envDefault:"1"`
	AutoscalingMaxCreate int    `env:"AUTOSCALING_MAX_CREATE" envDefault:"1"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	var cfg runtimeConfig
	if err := env.Parse(&cfg); err != nil {
		logger.Error("could not parse environment variables: %v", err)
	}

	logger = logger.With(
		"asg_id", cfg.AutoscalingGroupID,
		"worker_pool_id", cfg.SpaceliftWorkerPoolID,
	)

	ctx := context.Background()

	slSession, err := session.FromAPIKey(ctx, http.DefaultClient)(
		cfg.SpaceliftAPIEndpoint,
		cfg.SpaceliftAPIKeyID,
		cfg.SpaceliftAPISecret,
	)

	if err != nil {
		logger.Error("could not build Spacelift session: %v", err)
		os.Exit(1)
	}

	slClient := client.New(http.DefaultClient, slSession)

	var wpDetails internal.WorkerPoolDetails
	if err := slClient.Query(ctx, &wpDetails, map[string]any{"workerPool": cfg.SpaceliftWorkerPoolID}); err != nil {
		logger.Error("could not list Spacelift workers: %v", err)
		os.Exit(1)
	}

	if wpDetails.Pool == nil {
		logger.Error("worker pool does not exist or you have no access to it")
		os.Exit(1)
	}

	// First, let's sort the workers by their creation time. This is important
	// because Spacelift will always prioritize the newest workers for new runs,
	// so operating on the oldest ones first is going to be the safest.
	//
	// The backend should already return the workers in the order of their
	// creation, but let's be extra safe and not rely on that.
	sort.Slice(wpDetails.Pool.Workers, func(i, j int) bool {
		return wpDetails.Pool.Workers[i].CreatedAt < wpDetails.Pool.Workers[j].CreatedAt
	})

	workerInstanceIDs := make(map[internal.InstanceID]string)
	var idle []internal.Worker

	workers := wpDetails.Pool.Workers

	// Let's check for some preconditions. We want to make sure that all
	// the workers have the necessary metadata set, and that they belong to the
	// same autoscaling group. If either of these conditions is not met, we
	// should not proceed because the results are not going to be reliable.
	for _, worker := range workers {
		logger = logger.With("worker_id", worker.ID)

		groupId, instanceID, err := worker.InstanceIdentity()
		if err != nil {
			logger.Error("invalid metadata: %v", err)
			os.Exit(1)
		}

		logger = logger.With(
			"instance_id", instanceID,
			"instance_asg", groupId,
		)

		if string(groupId) != cfg.AutoscalingGroupID {
			logger.Error("worker belongs to a different autoscaling group")
			os.Exit(1)
		}

		workerInstanceIDs[instanceID] = worker.ID

		if !worker.Busy {
			idle = append(idle, worker)
		}
	}

	// Now that we have data from Spacelift, we can get the current state of
	// the autoscaling group from AWS.
	awsConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.AutoscalingRegion))
	if err != nil {
		logger.Error("could not load AWS config: %v", err)
		os.Exit(1)
	}

	asClient := autoscaling.NewFromConfig(awsConfig)
	groups, err := asClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{cfg.AutoscalingGroupID},
	})

	if err != nil {
		logger.Error("could not list autoscaling groups: %v", err)
		os.Exit(1)
	}

	if len(groups.AutoScalingGroups) != 1 {
		logger.Error("could not find autoscaling group")
		os.Exit(1)
	}

	group := groups.AutoScalingGroups[0]

	// Let's make sure that for each of the in-service instances we have a
	// corresponding worker in Spacelift, or that we have "stray" machines.
	instancesWithoutCorrespondingWorkers := make([]string, 0)
	for _, instance := range group.Instances {
		if instance.InstanceId == nil {
			// Should never happen, but let's be extra safe around nil pointers.
			logger.Error("autoscaling group contains an instance without an ID")
		}

		logger = logger.With(
			"instance_id", *instance.InstanceId,
			"state", instance.LifecycleState,
		)

		// Instance not in service, we don't care about it.
		if instance.LifecycleState != types.LifecycleStateInService {
			continue
		}

		if _, ok := workerInstanceIDs[internal.InstanceID(*instance.InstanceId)]; !ok {
			logger.Warn("instance has no corresponding worker")
			instancesWithoutCorrespondingWorkers = append(instancesWithoutCorrespondingWorkers, *instance.InstanceId)
		}
	}

	// There's a question of what to do with the "stray" machines. The
	// decision will be made based on the creation timestamp.
	// In order to get the start time of the instance, we will need to query
	// the EC2 API. This is a bit annoying, but there's no other way to get this
	// information.
	ec2Client := ec2.NewFromConfig(awsConfig)

	instancesOutput, err := ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: instancesWithoutCorrespondingWorkers,
	})

	if err != nil {
		logger.Error("could not list EC2 instances: %v", err)
		os.Exit(1)
	}

	for _, reservation := range instancesOutput.Reservations {
		for _, instance := range reservation.Instances {
			logger = logger.With("instance_id", *instance.InstanceId)

			if instance.LaunchTime == nil {
				logger.Error("instance has no launch time")
				os.Exit(1)
			}

			instanceAge := time.Since(*instance.LaunchTime)

			logger = logger.With(
				"launch_time", instance.LaunchTime,
				"instance_age", instanceAge,
			)

			// If the machine was only created recently (say a generous window of 10
			// minutes), it is possible that it hasn't managed to register itself with
			// Spacelift yet. But if it's been around for a while we will want to kill
			// it and remove it from the ASG.
			if instanceAge > 10*time.Minute {
				logger.Warn("instance has no corresponding worker in Spacelift, removing from the ASG")

				if err := killInstance(ctx, asClient, ec2Client, cfg.SpaceliftWorkerPoolID, *instance.InstanceId); err != nil {
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
	if len(workers) != len(group.Instances) {
		logger.With(
			"workers", len(workers),
			"instances", len(group.Instances),
		).Warn("number of workers does not match the number of instances in the ASG")

		return
	}

	// If we got this far, we can get to our main business of scaling.
	// The logic is as follows:
	//
	// 1. We look at the total number of workers. If it's at the minimum or the
	// maximum already, we do nothing.
	if group.MaxSize == nil {
		logger.Error("autoscaling group has no maximum size")
		os.Exit(1)
	}

	if len(workers) >= int(*group.MaxSize) {
		logger.Warn("autoscaling group is already at maximum size")
		return
	}

	if group.MinSize == nil {
		logger.Error("autoscaling group has no minimum size")
	}

	if len(workers) <= int(*group.MinSize) {
		logger.Warn("autoscaling group is already at minimum size")
		return
	}

	// 2. We then look at the queue and calculate the difference between the
	// number of idle workers and the number of queued tasks.
	difference := int(wpDetails.Pool.PendingRuns) - len(idle)

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

		newASGCapacity := *group.DesiredCapacity + spinUpBy
		if newASGCapacity > *group.MaxSize {
			newASGCapacity = *group.MaxSize
		}

		logger = logger.With("new_capacity", newASGCapacity)
		logger.Info("scaling up")

		_, err = asClient.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
			AutoScalingGroupName: aws.String(cfg.AutoscalingGroupID),
			DesiredCapacity:      aws.Int32(newASGCapacity),
		})

		if err != nil {
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
		overMinimum := *group.DesiredCapacity - *group.MinSize
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

			drained, err := drainWorker(ctx, slClient, cfg.SpaceliftWorkerPoolID, worker.ID)
			if err != nil {
				logger.Error("could not drain worker: %v", err)
				os.Exit(1)
			}

			if !drained {
				logger.Warn("idle worker got a job, not killing any more")
				return
			}

			if err := killInstance(ctx, asClient, ec2Client, cfg.AutoscalingGroupID, string(instanceID)); err != nil {
				logger.Error("could not kill instance: %v", err)

				// If the killing is unsuccessful, we don't want to kill any more
				// but we can undrain the worker for the next run.
				if _, err = workerDrainSet(ctx, slClient, cfg.SpaceliftWorkerPoolID, worker.ID, false); err != nil {
					logger.Error("could not undrain unkillable worker: %v", err)
				} else {
					logger.Warn("successfully undrained unkillable worker")
				}

				os.Exit(1)
			}

			logger.Info("worker killed successfully")
		}
	}
}

func killInstance(ctx context.Context, asClient *autoscaling.Client, ec2Client *ec2.Client, groupID, instanceID string) (err error) {
	_, err = asClient.DetachInstances(ctx, &autoscaling.DetachInstancesInput{
		AutoScalingGroupName:           aws.String(groupID),
		InstanceIds:                    []string{instanceID},
		ShouldDecrementDesiredCapacity: aws.Bool(true),
	})

	if err != nil {
		return fmt.Errorf("could not detach instance from autoscaling group: %v", err)
	}

	// Now that the instance is detached from the ASG, we can terminate it.
	_, err = ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})

	if err != nil {
		return fmt.Errorf("could not terminate instance: %v", err)
	}

	return
}

// workerDrainSet(workerPool: ID!, id: ID!, drain: Boolean!): Worker!
func drainWorker(ctx context.Context, client client.Client, wpID, workerID string) (drained bool, err error) {
	worker, err := workerDrainSet(ctx, client, workerID, wpID, true)
	if err != nil {
		return false, err
	}

	// If the worker is not busy, our job here is done.
	if !worker.Busy {
		return true, nil
	}

	// However, if the worker is now busy, we we should undrain it immediately
	// to let it process jobs since clearly some have arrived while we were busy
	// doing other things.
	_, err = workerDrainSet(ctx, client, workerID, wpID, false)

	return false, err
}

func workerDrainSet(ctx context.Context, client client.Client, wpID, workerID string, drain bool) (*internal.Worker, error) {
	var mutation internal.WorkerDrainSet

	err := client.Mutate(ctx, mutation, map[string]any{
		"workerPoolId": wpID,
		"id":           workerID,
		"drain":        drain,
	})

	if err != nil {
		return nil, err
	}

	return &mutation.Worker, nil
}
