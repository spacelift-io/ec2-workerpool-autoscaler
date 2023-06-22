package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/spacelift-io/spacectl/client"
	"github.com/spacelift-io/spacectl/client/session"

	"gh.com/mw/autoscalr/internal"
)

var (
	// Spacelift-related variables.
	// TODO: use some library to parse these envvars.
	spaceliftAPIKeyID     = os.Getenv("SPACELIFT_API_KEY_ID")
	spaceliftAPISecret    = os.Getenv("SPACELIFT_API_SECRET")
	spaceliftAPIEndpoint  = os.Getenv("SPACELIFT_API_ENDPOINT")
	spaceliftWorkerPoolID = os.Getenv("SPACELIFT_WORKER_POOL_ID")

	// AWS-related variables.
	autoscalingGroupID   = os.Getenv("AUTOSCALING_GROUP_ID")
	autoscalingRegion    = os.Getenv("AUTOSCALING_REGION")
	autoscalingMaxKill   = os.Getenv("AUTOSCALING_MAX_KILL")
	autoscalingMaxCreate = os.Getenv("AUTOSCALING_MAX_CREATE")
)

func main() {
	maxKill, err := strconv.ParseInt(autoscalingMaxKill, 10, 64)
	if err != nil {
		log.Panicf("could not parse AUTOSCALING_MAX_KILL: %v", err)
	}

	maxCreate, err := strconv.ParseInt(autoscalingMaxCreate, 10, 64)
	if err != nil {
		log.Panicf("could not parse AUTOSCALING_MAX_CREATE: %v", err)
	}

	ctx := context.Background()

	slSession, err := session.FromAPIKey(ctx, http.DefaultClient)(
		spaceliftAPIEndpoint,
		spaceliftAPIKeyID,
		spaceliftAPISecret,
	)

	if err != nil {
		log.Panicf("could not build Spacelift session: %v", err)
	}

	slClient := client.New(http.DefaultClient, slSession)

	var wpDetails internal.WorkerPoolDetails
	if err := slClient.Query(ctx, &wpDetails, map[string]any{"workerPool": spaceliftWorkerPoolID}); err != nil {
		log.Panicf("could not list Spacelift workers: %v", err)
	}

	if wpDetails.Pool == nil {
		log.Panicf("Spacelift worker pool %q does not exist or you have no access to it", spaceliftWorkerPoolID)
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
		groupId, instanceID, err := worker.InstanceIdentity()
		if err != nil {
			log.Panicf("invalid metadata for worker %s: %v", worker.ID, err)
		}

		if string(groupId) != autoscalingGroupID {
			log.Panicf("worker %s belongs to a different autoscaling group (%s)", worker.ID, groupId)
		}

		workerInstanceIDs[instanceID] = worker.ID

		if !worker.Busy {
			idle = append(idle, worker)
		}
	}

	// Now that we have data from Spacelift, we can get the current state of
	// the autoscaling group from AWS.
	awsConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion(autoscalingRegion))
	if err != nil {
		log.Panicf("could not load AWS config: %v", err)
	}

	asClient := autoscaling.NewFromConfig(awsConfig)
	groups, err := asClient.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{autoscalingGroupID},
	})

	if err != nil {
		log.Panicf("could not list autoscaling groups: %v", err)
	}

	if len(groups.AutoScalingGroups) != 1 {
		log.Panicf("could not find autoscaling group %q", autoscalingGroupID)
	}

	group := groups.AutoScalingGroups[0]

	// Let's make sure that for each of the in-service instances we have a
	// corresponding worker in Spacelift, or that we have "stray" machines.
	instancesWithoutCorrespondingWorkers := make([]string, 0)
	for _, instance := range group.Instances {
		// Instance not in service, we don't care about it.
		if instance.LifecycleState != types.LifecycleStateInService {
			continue
		}

		if instance.InstanceId == nil {
			// Should never happen, but let's be extra safe around nil pointers.
			log.Fatal("autoscaling group contains an instance without an ID")
		}

		if _, ok := workerInstanceIDs[internal.InstanceID(*instance.InstanceId)]; !ok {
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
		log.Panicf("could not list EC2 instances: %v", err)
	}

	for _, reservation := range instancesOutput.Reservations {
		for _, instance := range reservation.Instances {
			if instance.LaunchTime == nil {
				log.Panicf("instance %s has no launch time", *instance.InstanceId)
			}

			// If the machine was only created recently (say a generous window of 10
			// minutes), it is possible that it hasn't managed to register itself with
			// Spacelift yet. But if it's been around for a while we will want to kill
			// it and remove it from the ASG.
			if time.Since(*instance.LaunchTime) > 10*time.Minute {
				log.Printf("instance %s has no corresponding worker in Spacelift, removing from the ASG", *instance.InstanceId)

				if err := killInstance(ctx, asClient, ec2Client, *instance.InstanceId); err != nil {
					log.Panicf("could not kill instance %s: %v", *instance.InstanceId, err)
				}

				// We don't want to kill too many instances at once, so let's
				// return after the first successfully killed one.
				log.Printf("instance %s successfully removed from the ASG and terminated", *instance.InstanceId)
			}
		}
	}

	// If we got this far, we can get to our main business of scaling.
	// The logic is as follows:
	//
	// 1. We look at the total number of workers. If it's at the minimum or the
	// maximum already, we do nothing.
	if group.MaxSize == nil {
		log.Panicf("autoscaling group %q has no maximum size", autoscalingGroupID)
	}

	if len(workers) >= int(*group.MaxSize) {
		log.Printf("autoscaling group %q is already at maximum size", autoscalingGroupID)
		return
	}

	if group.MinSize == nil {
		log.Panicf("autoscaling group %q has no minimum size", autoscalingGroupID)
	}

	if len(workers) <= int(*group.MinSize) {
		log.Printf("autoscaling group %q is already at minimum size", autoscalingGroupID)
		return
	}

	// 2. We then look at the queue and calculate the difference between the
	// number of idle workers and the number of queued tasks.
	difference := int(wpDetails.Pool.PendingRuns) - len(idle)

	switch {
	case difference == 0:
		// If there's no difference, we do nothing.
		log.Printf("autoscaling group %q is already at the desired size", autoscalingGroupID)
		return
	case difference > 0:
		// If there are more pending runs than idle workers, we need to scale up.
		// We will scale up by the difference, but there are two constraints:
		//
		// - we should not spin up more machines than the maximum capacity of the ASG;
		// - we should not spin up more machines at once than the maximum declared
		//   by the user.

		spinUpBy := int32(difference)
		if difference > int(maxCreate) {
			spinUpBy = int32(maxCreate)
		}

		newASGCapacity := *group.DesiredCapacity + spinUpBy
		if newASGCapacity > *group.MaxSize {
			newASGCapacity = *group.MaxSize
		}

		log.Printf("autoscaling group %q is scaling to %d", autoscalingGroupID, newASGCapacity)

		_, err = asClient.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
			AutoScalingGroupName: aws.String(autoscalingGroupID),
			DesiredCapacity:      aws.Int32(newASGCapacity),
		})

		if err != nil {
			log.Panicf("could not scale autoscaling group %q: %v", autoscalingGroupID, err)
		}

		log.Printf("autoscaling group %q scaled to %d", autoscalingGroupID, newASGCapacity)
		return
	case difference < 0:
		// If the number of idle workers is greater than the number of pending
		// runs, we need to scale down. We will scale down by the difference,
		// but there are two constraints:
		// - we should not spin down more machines than the minimum capacity of the ASG;
		// - we should not spin down more machines at once than the maximum declared
		//   by the user.
		killCount := -difference
		if killCount > int(maxKill) {
			killCount = int(maxKill)
		}

		// Check how many we can kill without going below the minimum capacity.
		overMinimum := *group.DesiredCapacity - *group.MinSize
		if killCount > int(overMinimum) {
			killCount = int(overMinimum)
		}

		fmt.Printf("we will try killing %s workers", killCount)

		for i := 0; i < killCount; i++ {
			worker := idle[i]

			log.Printf("autoscaling group %q is scaling down, killing worker %q", autoscalingGroupID, worker.ID)

			drained, err := drainWorker(ctx, slClient, worker.ID)
			if err != nil {
				log.Panicf("could not drain worker %q: %v", worker.ID, err)
			}

			if !drained {
				log.Printf("worker %q was not drained, not killing any more workers", worker.ID)
			}

			_, instanceID, _ := worker.InstanceIdentity()

			if err := killInstance(ctx, asClient, ec2Client, string(instanceID)); err != nil {
				log.Printf("could not kill instance %q: %v", instanceID, err)

				// If the killing is unsuccessful, we don't want to kill any more
				// but we can undrain the worker for the next run.
				_, err = workerDrainSet(ctx, slClient, worker.ID, false)
				if err != nil {
					log.Panicf("could not undrain unkillable worker %q: %v", worker.ID, err)
				} else {
					log.Panicf("undrained unkillable worker %q", worker.ID)
				}
			}
		}
	}
}

func killInstance(ctx context.Context, asClient *autoscaling.Client, ec2Client *ec2.Client, instanceID string) (err error) {
	_, err = asClient.DetachInstances(ctx, &autoscaling.DetachInstancesInput{
		AutoScalingGroupName:           aws.String(autoscalingGroupID),
		InstanceIds:                    []string{instanceID},
		ShouldDecrementDesiredCapacity: aws.Bool(true),
	})

	if err != nil {
		return
	}

	// Now that the instance is detached from the ASG, we can terminate it.
	_, err = ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})

	return
}

// workerDrainSet(workerPool: ID!, id: ID!, drain: Boolean!): Worker!
func drainWorker(ctx context.Context, client client.Client, workerID string) (drained bool, err error) {
	worker, err := workerDrainSet(ctx, client, workerID, true)
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
	_, err = workerDrainSet(ctx, client, workerID, false)

	return false, err
}

func workerDrainSet(ctx context.Context, client client.Client, workerID string, drain bool) (*internal.Worker, error) {
	var mutation internal.WorkerDrainSet

	err := client.Mutate(ctx, mutation, map[string]any{
		"workerPoolId": spaceliftWorkerPoolID,
		"id":           workerID,
		"drain":        drain,
	})

	if err != nil {
		return nil, err
	}

	return &mutation.Worker, nil
}
