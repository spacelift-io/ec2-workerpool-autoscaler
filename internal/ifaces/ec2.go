package ifaces

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
)

// EC2 is an interface which mocks the subset of the EC2 client that we use in
// the controller.
//
//go:generate mockery --inpackage --name EC2 --filename mock_ec2.go
type EC2 interface {
	DescribeInstances(context.Context, *ec2.DescribeInstancesInput, ...func(*ec2.Options)) (*ec2.DescribeInstancesOutput, error)
	TerminateInstances(context.Context, *ec2.TerminateInstancesInput, ...func(*ec2.Options)) (*ec2.TerminateInstancesOutput, error)
}
