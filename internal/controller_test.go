package internal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingtypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/franela/goblin"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/mock"

	"gh.com/mw/autoscalr/internal"
	"gh.com/mw/autoscalr/internal/ifaces"
)

func TestController(t *testing.T) {
	g := goblin.Goblin(t)
	RegisterFailHandler(func(m string, _ ...int) { g.Fail(m) })

	g.Describe("Controller", func() {
		const asgName = "test-asg"
		const workerPoolID = "test-pool"

		var ctx context.Context
		var err error

		var mockAutoscaling *ifaces.MockAutoscaling
		var mockEC2 *ifaces.MockEC2
		var mockSpacelift *ifaces.MockSpacelift

		var sut *internal.Controller

		g.BeforeEach(func() {
			ctx = context.Background()
			err = nil

			mockAutoscaling = &ifaces.MockAutoscaling{}
			mockEC2 = &ifaces.MockEC2{}
			mockSpacelift = &ifaces.MockSpacelift{}

			sut = &internal.Controller{
				Autoscaling:             mockAutoscaling,
				EC2:                     mockEC2,
				Spacelift:               mockSpacelift,
				AWSAutoscalingGroupName: asgName,
				SpaceliftWorkerPoolID:   workerPoolID,
			}
		})

		g.Describe("DescribeInstances", func() {
			instanceIDs := []string{"i-1"}

			var instances []ec2types.Instance

			var input *ec2.DescribeInstancesInput
			var apiCall *mock.Call

			g.BeforeEach(func() {
				input = nil

				apiCall = mockEC2.On(
					"DescribeInstances",
					mock.Anything,
					mock.MatchedBy(func(in any) bool {
						input = in.(*ec2.DescribeInstancesInput)
						return true
					}),
					mock.Anything,
				)
			})

			g.JustBeforeEach(func() {
				instances, err = sut.DescribeInstances(ctx, instanceIDs)
			})

			g.Describe("when the API call fails", func() {
				g.BeforeEach(func() { apiCall.Return(nil, errors.New("bacon")) })

				g.It("sends the correct input", func() {
					Expect(input).NotTo(BeNil())
					Expect(input.InstanceIds).To(Equal(instanceIDs))
				})

				g.It("should return an error", func() {
					Expect(instances).To(BeEmpty())
					Expect(err).To(MatchError("could not describe instances: bacon"))
				})
			})

			g.Describe("when the API call succeeds", func() {
				var output *ec2.DescribeInstancesOutput

				g.BeforeEach(func() {
					output = &ec2.DescribeInstancesOutput{
						Reservations: []ec2types.Reservation{
							{Instances: []ec2types.Instance{{
								InstanceId: &instanceIDs[0],
								LaunchTime: nullable(time.Now()),
							}}},
						},
					}

					apiCall.Return(output, nil)
				})

				g.Describe("when the instance has no ID", func() {
					g.BeforeEach(func() { output.Reservations[0].Instances[0].InstanceId = nil })

					g.It("should return an error", func() {
						Expect(instances).To(BeEmpty())
						Expect(err).To(MatchError("could not find instance ID"))
					})
				})

				g.Describe("when the instance has no launch time", func() {
					g.BeforeEach(func() { output.Reservations[0].Instances[0].LaunchTime = nil })

					g.It("should return an error", func() {
						Expect(instances).To(BeEmpty())
						Expect(err).To(MatchError("could not find launch time for instance i-1"))
					})
				})

				g.Describe("when the instance has the correct ID and launch time", func() {
					g.It("should return the instance", func() {
						Expect(instances).To(HaveLen(1))
					})
				})
			})
		})

		g.Describe("GetAutoscalingGroup", func() {
			var group *autoscalingtypes.AutoScalingGroup

			var input *autoscaling.DescribeAutoScalingGroupsInput
			var apiCall *mock.Call

			g.BeforeEach(func() {
				input = nil

				apiCall = mockAutoscaling.On(
					"DescribeAutoScalingGroups",
					mock.Anything,
					mock.MatchedBy(func(in any) bool {
						input = in.(*autoscaling.DescribeAutoScalingGroupsInput)
						return true
					}),
					mock.Anything,
				)
			})

			g.JustBeforeEach(func() { group, err = sut.GetAutoscalingGroup(ctx) })

			g.Describe("when the API call fails", func() {
				g.BeforeEach(func() { apiCall.Return(nil, errors.New("bacon")) })

				g.It("sends the correct input", func() {
					Expect(input).NotTo(BeNil())
					Expect(input.AutoScalingGroupNames).To(Equal([]string{asgName}))
				})

				g.It("should return an error", func() {
					Expect(group).To(BeNil())
					Expect(err).To(MatchError("could not get autoscaling group details: bacon"))
				})
			})

			g.Describe("when the API call succeeds", func() {
				var output *autoscaling.DescribeAutoScalingGroupsOutput

				g.BeforeEach(func() {
					output = &autoscaling.DescribeAutoScalingGroupsOutput{}
					apiCall.Return(output, nil)
				})

				g.Describe("when it returns no groups", func() {
					g.BeforeEach(func() { output.AutoScalingGroups = nil })

					g.It("should return an error", func() {
						Expect(group).To(BeNil())
						Expect(err).To(MatchError("could not find autoscaling group test-asg"))
					})
				})

				g.Describe("when it returns multiple groups", func() {
					g.BeforeEach(func() {
						output.AutoScalingGroups = []autoscalingtypes.AutoScalingGroup{{}, {}}
					})

					g.It("should return an error", func() {
						Expect(group).To(BeNil())
						Expect(err).To(MatchError("found more than one autoscaling group with name test-asg"))
					})
				})

				g.Describe("when it returns a single group", func() {
					g.BeforeEach(func() { output.AutoScalingGroups = []autoscalingtypes.AutoScalingGroup{{}} })

					g.It("should return the group", func() {
						Expect(err).NotTo(HaveOccurred())
						Expect(group).NotTo(BeNil())
					})
				})
			})
		})
	})
}
