package internal

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-xray-sdk-go/instrumentation/awsv2"
	"github.com/aws/aws-xray-sdk-go/xray"
	spacelift "github.com/spacelift-io/spacectl/client"
	"github.com/spacelift-io/spacectl/client/session"

	"github.com/spacelift-io/awsautoscalr/internal/ifaces"
)

// Controller is responsible for handling interactions with external systems
// (Spacelift API as well as AWS Autoscaling and EC2 APIs) so that the main
// package can focus on the core logic.
type Controller struct {
	// Clients.
	Autoscaling ifaces.Autoscaling
	EC2         ifaces.EC2
	Spacelift   ifaces.Spacelift

	// Configuration.
	AWSAutoscalingGroupName string
	SpaceliftWorkerPoolID   string
}

// NewController creates a new controller instance.
func NewController(ctx context.Context, cfg *RuntimeConfig) (*Controller, error) {
	awsConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.AutoscalingRegion))
	if err != nil {
		return nil, fmt.Errorf("could not load AWS configuration: %w", err)
	}

	awsv2.AWSV2Instrumentor(&awsConfig.APIOptions)

	httpClient := xray.Client(nil)

	slSession, err := session.FromAPIKey(ctx, httpClient)(
		cfg.SpaceliftAPIEndpoint,
		cfg.SpaceliftAPIKeyID,
		cfg.SpaceliftAPISecret,
	)

	if err != nil {
		return nil, fmt.Errorf("could not create Spacelift session: %w", err)
	}

	return &Controller{
		Autoscaling:             autoscaling.NewFromConfig(awsConfig),
		EC2:                     ec2.NewFromConfig(awsConfig),
		Spacelift:               spacelift.New(httpClient, slSession),
		AWSAutoscalingGroupName: cfg.AutoscalingGroupName,
		SpaceliftWorkerPoolID:   cfg.SpaceliftWorkerPoolID,
	}, nil
}

// DescribeInstances returns the details of the given instances from AWS,
// making sure that the instances are valid for further processing.
func (c *Controller) DescribeInstances(ctx context.Context, instanceIDs []string) (instances []ec2types.Instance, err error) {
	xray.Capture(ctx, "aws.ec2.describeInstances", func(ctx context.Context) error {
		var output *ec2.DescribeInstancesOutput

		output, err = c.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: instanceIDs,
		})

		if err != nil {
			err = fmt.Errorf("could not describe instances: %w", err)
			return err
		}

		for _, reservation := range output.Reservations {
			for _, instance := range reservation.Instances {
				if instance.InstanceId == nil {
					err = errors.New("could not find instance ID")
					return err
				}

				if instance.LaunchTime == nil {
					err = fmt.Errorf("could not find launch time for instance %s", *instance.InstanceId)
					return err
				}

				instances = append(instances, instance)
			}
		}

		return nil
	})

	return instances, err
}

// GetAutoscalingGroup returns the autoscaling group details from AWS.
//
// It makes sure that the autoscaling group exists and that there is only
// one autoscaling group with the given name.
func (c *Controller) GetAutoscalingGroup(ctx context.Context) (out *autoscalingtypes.AutoScalingGroup, err error) {
	xray.Capture(ctx, "aws.asg.get", func(ctx context.Context) error {
		var output *autoscaling.DescribeAutoScalingGroupsOutput

		output, err = c.Autoscaling.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: []string{c.AWSAutoscalingGroupName},
		})

		if err != nil {
			err = fmt.Errorf("could not get autoscaling group details: %w", err)
			return err
		}

		if len(output.AutoScalingGroups) == 0 {
			err = fmt.Errorf("could not find autoscaling group %s", c.AWSAutoscalingGroupName)
			return err
		} else if len(output.AutoScalingGroups) > 1 {
			err = fmt.Errorf("found more than one autoscaling group with name %s", c.AWSAutoscalingGroupName)
			return err
		}

		out = &output.AutoScalingGroups[0]

		return nil
	})

	return
}

// GetWorkerPool returns the worker pool details from Spacelift.
func (c *Controller) GetWorkerPool(ctx context.Context) (out *WorkerPool, err error) {
	xray.Capture(ctx, "spacelift.workerpool.get", func(ctx context.Context) error {
		var wpDetails WorkerPoolDetails

		if err = c.Spacelift.Query(ctx, &wpDetails, map[string]any{"workerPool": c.SpaceliftWorkerPoolID}); err != nil {
			err = fmt.Errorf("could not get Spacelift worker pool details: %w", err)
			return err
		}

		if wpDetails.Pool == nil {
			err = errors.New("worker pool not found or not accessible")
			return err
		}

		// Let's sort the workers by their creation time. This is important
		// because Spacelift will always prioritize the newest workers for new runs,
		// so operating on the oldest ones first is going to be the safest.
		//
		// The backend should already return the workers in the order of their
		// creation, but let's be extra safe and not rely on that.
		sort.Slice(wpDetails.Pool.Workers, func(i, j int) bool {
			return wpDetails.Pool.Workers[i].CreatedAt < wpDetails.Pool.Workers[j].CreatedAt
		})

		xray.AddMetadata(ctx, "workers", len(wpDetails.Pool.Workers))
		xray.AddMetadata(ctx, "pending_runs", wpDetails.Pool.PendingRuns)

		out = wpDetails.Pool

		return nil
	})

	return
}

// Drain worker drains a worker in the Spacelift worker pool.
func (c *Controller) DrainWorker(ctx context.Context, workerID string) (drained bool, err error) {
	xray.Capture(ctx, "spacelift.worker.drain", func(ctx context.Context) error {
		xray.AddAnnotation(ctx, "worker_id", workerID)

		var worker *Worker

		if worker, err = c.workerDrainSet(ctx, workerID, true); err != nil {
			err = fmt.Errorf("could not drain worker: %w", err)
			return err
		}

		xray.AddMetadata(ctx, "worker_id", worker.ID)
		xray.AddMetadata(ctx, "worker_busy", worker.Busy)
		xray.AddMetadata(ctx, "worker_drained", worker.Drained)

		// If the worker is not busy, our job here is done.
		if !worker.Busy {
			drained = true
			return nil
		}

		if _, err = c.workerDrainSet(ctx, workerID, false); err != nil {
			err = fmt.Errorf("could not undrain a busy worker: %w", err)
			return err
		}

		return nil
	})

	return
}

func (c *Controller) KillInstance(ctx context.Context, instanceID string) (err error) {
	xray.Capture(ctx, "aws.killinstance", func(ctx context.Context) error {
		xray.AddAnnotation(ctx, "instance_id", instanceID)

		_, err = c.Autoscaling.DetachInstances(ctx, &autoscaling.DetachInstancesInput{
			AutoScalingGroupName:           aws.String(c.AWSAutoscalingGroupName),
			InstanceIds:                    []string{instanceID},
			ShouldDecrementDesiredCapacity: aws.Bool(true),
		})

		if err != nil {
			err = fmt.Errorf("could not detach instance from autoscaling group: %v", err)
			return err
		}

		// Now that the instance is detached from the ASG, we can terminate it.
		_, err = c.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds: []string{instanceID},
		})

		if err != nil {
			err = fmt.Errorf("could not terminate detached instance: %v", err)
			return err
		}

		return nil
	})

	return
}

func (c *Controller) ScaleUpASG(ctx context.Context, desiredCapacity int32) (err error) {
	xray.Capture(ctx, "aws.asg.scaleup", func(ctx context.Context) error {
		xray.AddMetadata(ctx, "desired_capacity", desiredCapacity)

		_, err = c.Autoscaling.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
			AutoScalingGroupName: aws.String(c.AWSAutoscalingGroupName),
			DesiredCapacity:      aws.Int32(int32(desiredCapacity)),
		})

		if err != nil {
			err = fmt.Errorf("could not set desired capacity: %v", err)
			return err
		}

		return nil
	})

	return
}

func (c *Controller) workerDrainSet(ctx context.Context, workerID string, drain bool) (worker *Worker, err error) {
	xray.Capture(ctx, fmt.Sprintf("spacelift.worker.setdrain.%t", drain), func(ctx context.Context) error {
		var mutation WorkerDrainSet

		variables := map[string]any{
			"workerPoolId": c.SpaceliftWorkerPoolID,
			"id":           workerID,
			"drain":        drain,
		}

		if err = c.Spacelift.Mutate(ctx, &mutation, variables); err != nil {
			err = fmt.Errorf("could not set worker drain to %t: %w", drain, err)
			return err
		}

		worker = &mutation.Worker

		return nil
	})

	return
}
