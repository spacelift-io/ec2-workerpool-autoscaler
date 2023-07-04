package ifaces

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
)

// Autoscaling is an interface which mocks the subset of the autoscaling client
// that we use in the controller.
//
//go:generate mockery --inpackage --name Autoscaling --filename mock_autoscaling.go
type Autoscaling interface {
	DescribeAutoScalingGroups(context.Context, *autoscaling.DescribeAutoScalingGroupsInput, ...func(*autoscaling.Options)) (*autoscaling.DescribeAutoScalingGroupsOutput, error)
	DetachInstances(context.Context, *autoscaling.DetachInstancesInput, ...func(*autoscaling.Options)) (*autoscaling.DetachInstancesOutput, error)
	SetDesiredCapacity(context.Context, *autoscaling.SetDesiredCapacityInput, ...func(*autoscaling.Options)) (*autoscaling.SetDesiredCapacityOutput, error)
}
