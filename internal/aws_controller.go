package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/spacelift-io/awsautoscalr/internal/ifaces"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type AWSController struct {
	Controller

	// Clients.
	Autoscaling ifaces.Autoscaling
	EC2         ifaces.EC2

	// Configuration.
	AWSAutoscalingGroupName string
}

// NewAWSController creates a new AWS controller instance.
func NewAWSController(ctx context.Context, cfg *RuntimeConfig) (*AWSController, error) {
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

	spaceliftClient, err := newSpaceliftClient(ctx, cfg.SpaceliftAPIEndpoint, cfg.SpaceliftAPIKeyID, *output.Parameter.Value)
	if err != nil {
		return nil, err
	}

	arnParts := strings.Split(cfg.AutoscalingGroupARN, "/")
	if len(arnParts) != 2 {
		return nil, fmt.Errorf("could not parse autoscaling group ARN")
	}

	return &AWSController{
		Controller: Controller{
			Spacelift:             spaceliftClient,
			SpaceliftWorkerPoolID: cfg.SpaceliftWorkerPoolID,
			Tracer:                otel.Tracer("github.com/spacelift-io/awsautoscalr/internal/controller"),
		},
		Autoscaling:             autoscaling.NewFromConfig(awsConfig),
		EC2:                     ec2.NewFromConfig(awsConfig),
		AWSAutoscalingGroupName: arnParts[1],
	}, nil
}

// DescribeInstances returns the details of the given instances from AWS,
// making sure that the instances are valid for further processing.
func (c *AWSController) DescribeInstances(ctx context.Context, instanceIDs []string) (instances []Instance, err error) {
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

			instances = append(instances, Instance{
				ID:         *instance.InstanceId,
				LaunchTime: *instance.LaunchTime,
			})
		}
	}

	return instances, nil
}

// GetAutoscalingGroup returns the autoscaling group details from AWS.
//
// It makes sure that the autoscaling group exists and that there is only
// one autoscaling group with the given name.
func (c *AWSController) GetAutoscalingGroup(ctx context.Context) (out *AutoScalingGroup, err error) {
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

	// Convert AWS SDK v2 types to internal types.
	asg := output.AutoScalingGroups[0]
	out = &AutoScalingGroup{
		Name:            aws.ToString(asg.AutoScalingGroupName),
		MinSize:         -1,
		MaxSize:         -1,
		DesiredCapacity: -1,
		Instances:       make([]Instance, 0, len(asg.Instances)),
	}

	if asg.MinSize != nil {
		out.MinSize = int(*asg.MinSize)
	}

	if asg.MaxSize != nil {
		out.MaxSize = int(*asg.MaxSize)
	}

	if asg.DesiredCapacity != nil {
		out.DesiredCapacity = int(*asg.DesiredCapacity)
	}

	for _, instance := range asg.Instances {
		out.Instances = append(out.Instances, Instance{
			ID:             aws.ToString(instance.InstanceId),
			LifecycleState: string(instance.LifecycleState),
		})
	}

	return out, nil
}

func (c *AWSController) KillInstance(ctx context.Context, instanceID string) (err error) {
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

func (c *AWSController) ScaleUpASG(ctx context.Context, desiredCapacity int) (err error) {
	ctx, span := c.Tracer.Start(ctx, "aws.asg.scaleup")
	defer span.End()

	span.SetAttributes(attribute.Int("desired_capacity", desiredCapacity))

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
