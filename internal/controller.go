package internal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/shurcooL/graphql"
	spacelift "github.com/spacelift-io/spacectl/client"
	"github.com/spacelift-io/spacectl/client/session"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

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

	// Telemetry.
	Tracer trace.Tracer
}

// NewController creates a new controller instance.
func NewController(ctx context.Context, cfg *RuntimeConfig) (*Controller, error) {
	awsConfig, err := config.LoadDefaultConfig(ctx, config.WithRegion(cfg.AutoscalingRegion))
	if err != nil {
		return nil, fmt.Errorf("could not load AWS configuration: %w", err)
	}

	otelaws.AppendMiddlewares(&awsConfig.APIOptions)

	ssmClient := ssm.NewFromConfig(awsConfig)
	var output *ssm.GetParameterOutput

	output, err = ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name:           aws.String(cfg.SpaceliftAPISecretName),
		WithDecryption: aws.Bool(true),
	})

	if err != nil {
		return nil, fmt.Errorf("could not get Spacelift API key secret from SSM: %w", err)
	} else if output.Parameter == nil {
		return nil, errors.New("could not find Spacelift API key secret in SSM")
	} else if output.Parameter.Value == nil {
		return nil, errors.New("could not find Spacelift API key secret value in SSM")
	}

	httpClient := &http.Client{
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
				return r.Host
			}),
		),
	}

	slSession, err := session.FromAPIKey(ctx, httpClient)(
		cfg.SpaceliftAPIEndpoint,
		cfg.SpaceliftAPIKeyID,
		*output.Parameter.Value,
	)

	if err != nil {
		return nil, fmt.Errorf("could not create Spacelift session: %w", err)
	}

	arnParts := strings.Split(cfg.AutoscalingGroupARN, "/")
	if len(arnParts) != 2 {
		return nil, fmt.Errorf("could not parse autoscaling group ARN")
	}

	return &Controller{
		Autoscaling:             autoscaling.NewFromConfig(awsConfig),
		EC2:                     ec2.NewFromConfig(awsConfig),
		Spacelift:               spacelift.New(httpClient, slSession),
		AWSAutoscalingGroupName: arnParts[1],
		SpaceliftWorkerPoolID:   cfg.SpaceliftWorkerPoolID,
		Tracer:                  otel.Tracer("github.com/spacelift-io/awsautoscalr/internal/controller"),
	}, nil
}

// DescribeInstances returns the details of the given instances from AWS,
// making sure that the instances are valid for further processing.
func (c *Controller) DescribeInstances(ctx context.Context, instanceIDs []string) (instances []ec2types.Instance, err error) {
	ctx, span := c.Tracer.Start(ctx, "aws.ec2.describeInstances")
	defer span.End()

	var output *ec2.DescribeInstancesOutput

	output, err = c.EC2.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: instanceIDs,
	})

	if err != nil {
		err = fmt.Errorf("could not describe instances: %w", err)
		return nil, err
	}

	for _, reservation := range output.Reservations {
		for _, instance := range reservation.Instances {
			if instance.InstanceId == nil {
				err = errors.New("could not find instance ID")
				return nil, err
			}

			if instance.LaunchTime == nil {
				err = fmt.Errorf("could not find launch time for instance %s", *instance.InstanceId)
				return nil, err
			}

			instances = append(instances, instance)
		}
	}

	return instances, nil
}

// GetAutoscalingGroup returns the autoscaling group details from AWS.
//
// It makes sure that the autoscaling group exists and that there is only
// one autoscaling group with the given name.
func (c *Controller) GetAutoscalingGroup(ctx context.Context) (out *autoscalingtypes.AutoScalingGroup, err error) {
	ctx, span := c.Tracer.Start(ctx, "aws.asg.get")
	defer span.End()

	var output *autoscaling.DescribeAutoScalingGroupsOutput

	output, err = c.Autoscaling.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{c.AWSAutoscalingGroupName},
	})

	if err != nil {
		err = fmt.Errorf("could not get autoscaling group details: %w", err)
		return nil, err
	}

	if len(output.AutoScalingGroups) == 0 {
		err = fmt.Errorf("could not find autoscaling group %s", c.AWSAutoscalingGroupName)
		return nil, err
	} else if len(output.AutoScalingGroups) > 1 {
		err = fmt.Errorf("found more than one autoscaling group with name %s", c.AWSAutoscalingGroupName)
		return nil, err
	}

	out = &output.AutoScalingGroups[0]

	return out, nil
}

// GetWorkerPool returns the worker pool details from Spacelift.
func (c *Controller) GetWorkerPool(ctx context.Context) (out *WorkerPool, err error) {
	ctx, span := c.Tracer.Start(ctx, "spacelift.workerpool.get")
	defer span.End()

	var wpDetails WorkerPoolDetails

	if err = c.Spacelift.Query(ctx, &wpDetails, map[string]any{"workerPool": c.SpaceliftWorkerPoolID}); err != nil {
		err = fmt.Errorf("could not get Spacelift worker pool details: %w", err)
		return nil, err
	}

	if wpDetails.Pool == nil {
		err = errors.New("worker pool not found or not accessible")
		return nil, err
	}

	worker_index := 0
	for _, worker := range wpDetails.Pool.Workers {
		if !worker.Drained {
			wpDetails.Pool.Workers[worker_index] = worker
			worker_index++
		}
	}
	wpDetails.Pool.Workers = wpDetails.Pool.Workers[:worker_index]

	sort.Slice(wpDetails.Pool.Workers, func(i, j int) bool {
		return wpDetails.Pool.Workers[i].CreatedAt < wpDetails.Pool.Workers[j].CreatedAt
	})

	span.SetAttributes(
		attribute.Int("workers", len(wpDetails.Pool.Workers)),
		attribute.Int("pending_runs", int(wpDetails.Pool.PendingRuns)),
	)

	out = wpDetails.Pool

	return out, nil
}

// Drain worker drains a worker in the Spacelift worker pool.
func (c *Controller) DrainWorker(ctx context.Context, workerID string) (drained bool, err error) {
	ctx, span := c.Tracer.Start(ctx, "spacelift.worker.drain")
	defer span.End()

	span.SetAttributes(attribute.String("worker_id", workerID))

	var worker *Worker

	if worker, err = c.workerDrainSet(ctx, workerID, true); err != nil {
		err = fmt.Errorf("could not drain worker: %w", err)
		return false, err
	}

	span.SetAttributes(
		attribute.String("worker.id", worker.ID),
		attribute.Bool("worker.busy", worker.Busy),
		attribute.Bool("worker.drained", worker.Drained),
	)

	if !worker.Busy {
		drained = true
		return true, nil
	}

	if _, err = c.workerDrainSet(ctx, workerID, false); err != nil {
		err = fmt.Errorf("could not undrain a busy worker: %w", err)
		return false, err
	}

	return false, nil
}

func (c *Controller) KillInstance(ctx context.Context, instanceID string) (err error) {
	ctx, span := c.Tracer.Start(ctx, "aws.killinstance")
	defer span.End()

	span.SetAttributes(attribute.String("instance_id", instanceID))

	_, err = c.Autoscaling.DetachInstances(ctx, &autoscaling.DetachInstancesInput{
		AutoScalingGroupName:           aws.String(c.AWSAutoscalingGroupName),
		InstanceIds:                    []string{instanceID},
		ShouldDecrementDesiredCapacity: aws.Bool(true),
	})

	if err != nil && !strings.Contains(err.Error(), "is not part of Auto Scaling group") {
		err = fmt.Errorf("could not detach instance from autoscaling group: %v", err)
		return err
	}

	_, err = c.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	})

	if err != nil {
		err = fmt.Errorf("could not terminate detached instance: %v", err)
		return err
	}

	return nil
}

func (c *Controller) ScaleUpASG(ctx context.Context, desiredCapacity int32) (err error) {
	ctx, span := c.Tracer.Start(ctx, "aws.asg.scaleup")
	defer span.End()

	span.SetAttributes(attribute.Int("desired_capacity", int(desiredCapacity)))

	_, err = c.Autoscaling.SetDesiredCapacity(ctx, &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(c.AWSAutoscalingGroupName),
		DesiredCapacity:      aws.Int32(int32(desiredCapacity)),
	})

	if err != nil {
		err = fmt.Errorf("could not set desired capacity: %v", err)
		return err
	}

	return nil
}

func (c *Controller) workerDrainSet(ctx context.Context, workerID string, drain bool) (worker *Worker, err error) {
	ctx, span := c.Tracer.Start(ctx, fmt.Sprintf("spacelift.worker.setdrain.%t", drain))
	defer span.End()

	span.SetAttributes(
		attribute.String("worker_id", workerID),
		attribute.String("worker_pool_id", c.SpaceliftWorkerPoolID),
		attribute.Bool("drain", drain),
	)

	var mutation WorkerDrainSet

	variables := map[string]any{
		"workerPoolId": graphql.ID(c.SpaceliftWorkerPoolID),
		"workerId":     graphql.ID(workerID),
		"drain":        graphql.Boolean(drain),
	}

	if err = c.Spacelift.Mutate(ctx, &mutation, variables); err != nil {
		err = fmt.Errorf("could not set worker drain to %t: %w", drain, err)
		return nil, err
	}

	worker = &mutation.Worker

	return worker, nil
}
