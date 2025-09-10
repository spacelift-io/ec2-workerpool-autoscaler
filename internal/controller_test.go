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
	"github.com/shurcooL/graphql"
	"github.com/stretchr/testify/mock"

	"github.com/spacelift-io/awsautoscalr/internal"
	"github.com/spacelift-io/awsautoscalr/internal/ifaces"
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

		g.Describe("GetWorkerPool", func() {
			var spaceliftCall *mock.Call
			var params map[string]any
			var workerPool *internal.WorkerPool

			g.BeforeEach(func() {
				params = nil
				workerPool = nil

				spaceliftCall = mockSpacelift.On(
					"Query",
					mock.Anything,
					mock.Anything,
					mock.MatchedBy(func(in any) bool {
						params = in.(map[string]any)
						return true
					}),
					mock.Anything,
				)
			})

			g.JustBeforeEach(func() { workerPool, err = sut.GetWorkerPool(ctx) })

			g.Describe("when the API call fails", func() {
				g.BeforeEach(func() { spaceliftCall.Return(errors.New("bacon")) })

				g.It("sends the correct input", func() {
					Expect(params).NotTo(BeNil())
					Expect(params["workerPool"]).To(Equal(workerPoolID))
				})

				g.It("should return an error", func() {
					Expect(workerPool).To(BeNil())
					Expect(err).To(MatchError("could not get Spacelift worker pool details: bacon"))
				})
			})

			g.Describe("when the API call succeeds", func() {
				var returnedPool *internal.WorkerPool

				g.BeforeEach(func() {
					returnedPool = nil

					spaceliftCall.Run(func(args mock.Arguments) {
						details := args.Get(1).(*internal.WorkerPoolDetails)
						details.Pool = returnedPool
					}).Return(nil)
				})

				g.Describe("when the worker pool is not found (default)", func() {
					g.It("should return an error", func() {
						Expect(workerPool).To(BeNil())
						Expect(err).To(MatchError("worker pool not found or not accessible"))
					})
				})

				g.Describe("when the worker pool is found", func() {
					g.BeforeEach(func() {
						returnedPool = &internal.WorkerPool{
							Workers: []internal.Worker{
								{ID: "newer", CreatedAt: 5, Drained: false},
								{ID: "drained", CreatedAt: 2, Drained: true},
								{ID: "older", CreatedAt: 1, Drained: false},
							},
						}
					})

					g.It("should return the worker pool with sorted and filtered workers", func() {
						Expect(err).NotTo(HaveOccurred())
						Expect(workerPool).NotTo(BeNil())
						Expect(workerPool.Workers).To(HaveLen(2))
						Expect(workerPool.Workers[0].ID).To(Equal("older"))
						Expect(workerPool.Workers[1].ID).To(Equal("newer"))
					})
				})
			})
		})

		g.Describe("DrainWorker", func() {
			const workerID = "test-worker"

			var drained bool
			var drainCall *mock.Call
			var drainParams map[string]any

			g.BeforeEach(func() {
				drained = false
				drainParams = nil

				drainCall = mockSpacelift.On(
					"Mutate",
					mock.Anything,
					mock.Anything,
					mock.MatchedBy(func(in any) bool {
						if params := in.(map[string]any); params["drain"].(graphql.Boolean) {
							drainParams = params
							return true
						}
						return false
					}),
					mock.Anything,
				)
			})

			g.JustBeforeEach(func() { drained, err = sut.DrainWorker(ctx, workerID) })

			g.Describe("when the drain call fails", func() {
				g.BeforeEach(func() { drainCall.Return(errors.New("bacon")) })

				g.It("send the correct input", func() {
					Expect(drainParams).NotTo(BeNil())
					Expect(drainParams["workerPoolId"]).To(Equal(workerPoolID))
					Expect(drainParams["workerId"]).To(Equal(workerID))
					Expect(bool(drainParams["drain"].(graphql.Boolean))).To(BeTrue())
				})

				g.It("should return an error", func() {
					Expect(drained).To(BeFalse())
					Expect(err).To(MatchError("could not drain worker: could not set worker drain to true: bacon"))
				})
			})

			g.Describe("when the API call succeeds", func() {
				var worker *internal.Worker

				g.BeforeEach(func() {
					worker = nil

					drainCall.Run(func(args mock.Arguments) {
						args.Get(1).(*internal.WorkerDrainSet).Worker = *worker
					}).Return(nil)
				})

				g.Describe("when the worker is not busy", func() {
					g.BeforeEach(func() { worker = &internal.Worker{Busy: false} })

					g.It("succeeds and reports the worker as drained", func() {
						Expect(drained).To(BeTrue())
						Expect(err).NotTo(HaveOccurred())
					})
				})

				g.Describe("when the worker is busy", func() {
					var undrainCall *mock.Call
					var undrainParams map[string]any

					g.BeforeEach(func() {
						worker = &internal.Worker{Busy: true}

						undrainParams = nil

						undrainCall = mockSpacelift.On(
							"Mutate",
							mock.Anything,
							mock.Anything,
							mock.MatchedBy(func(in any) bool {
								if params := in.(map[string]any); !params["drain"].(graphql.Boolean) {
									undrainParams = params
									return true
								}
								return false
							}),
							mock.Anything,
						)
					})

					g.Describe("when the undrain call fails", func() {
						g.BeforeEach(func() { undrainCall.Return(errors.New("bacon")) })

						g.It("send the correct input", func() {
							Expect(undrainParams).NotTo(BeNil())
							Expect(undrainParams["workerPoolId"]).To(Equal(workerPoolID))
							Expect(undrainParams["workerId"]).To(Equal(workerID))
							Expect(bool(undrainParams["drain"].(graphql.Boolean))).To(BeFalse())
						})

						g.It("should return an error", func() {
							Expect(drained).To(BeFalse())
							Expect(err).To(MatchError("could not undrain a busy worker: could not set worker drain to false: bacon"))
						})
					})

					g.Describe("when the undrain call succeeds", func() {
						g.BeforeEach(func() { undrainCall.Return(nil) })

						g.It("succeeds but reports the worker as not drained", func() {
							Expect(drained).To(BeFalse())
							Expect(err).NotTo(HaveOccurred())
						})
					})
				})
			})
		})

		g.Describe("KillInstance", func() {
			const instanceID = "test-instance"

			var detachCall *mock.Call
			var detachInput *autoscaling.DetachInstancesInput

			g.BeforeEach(func() {
				detachInput = nil

				detachCall = mockAutoscaling.On(
					"DetachInstances",
					mock.Anything,
					mock.MatchedBy(func(in *autoscaling.DetachInstancesInput) bool {
						detachInput = in
						return true
					}),
					mock.Anything,
				)
			})

			g.JustBeforeEach(func() { err = sut.KillInstance(ctx, instanceID) })

			g.Describe("when the detach call fails", func() {
				g.BeforeEach(func() { detachCall.Return(nil, errors.New("bacon")) })

				g.It("send the correct input", func() {
					Expect(detachInput).NotTo(BeNil())
					Expect(detachInput.InstanceIds).To(ConsistOf(instanceID))
					Expect(*detachInput.AutoScalingGroupName).To(Equal(asgName))
					Expect(*detachInput.ShouldDecrementDesiredCapacity).To(BeTrue())
				})

				g.It("should return an error", func() {
					Expect(err).To(MatchError("could not detach instance from autoscaling group: bacon"))
				})
			})

			g.Describe("when the detach call does not fail", func() {
				var terminateCall *mock.Call
				var terminateInput *ec2.TerminateInstancesInput

				g.BeforeEach(func() {
					terminateInput = nil

					terminateCall = mockEC2.On(
						"TerminateInstances",
						mock.Anything,
						mock.MatchedBy(func(in *ec2.TerminateInstancesInput) bool {
							terminateInput = in
							return true
						}),
						mock.Anything,
					)
				})

				g.Describe("when the instance is not part of the ASG", func() {
					g.BeforeEach(func() {
						detachCall.Return(nil, errors.New("instance is not part of Auto Scaling group"))
						terminateCall.Return(nil, errors.New("bacon"))
					})

					g.It("should return an error", func() {
						Expect(err).To(MatchError("could not terminate detached instance: bacon"))
					})
				})

				g.Describe("when the detach call succeeds", func() {
					g.BeforeEach(func() { detachCall.Return(nil, nil) })

					g.Describe("when the terminate call fails", func() {
						g.BeforeEach(func() { terminateCall.Return(nil, errors.New("bacon")) })

						g.It("send the correct input", func() {
							Expect(terminateInput).NotTo(BeNil())
							Expect(terminateInput.InstanceIds).To(ConsistOf(instanceID))
						})

						g.It("should return an error", func() {
							Expect(err).To(MatchError("could not terminate detached instance: bacon"))
						})
					})

					g.Describe("when the terminate call succeeds", func() {
						g.BeforeEach(func() { terminateCall.Return(nil, nil) })

						g.It("succeeds", func() { Expect(err).NotTo(HaveOccurred()) })
					})
				})
			})
		})

		g.Describe("ScaleUpASG", func() {
			const desiredCapacity = 42

			var setCapacityCall *mock.Call
			var setCapacityInput *autoscaling.SetDesiredCapacityInput

			g.BeforeEach(func() {
				setCapacityInput = nil

				setCapacityCall = mockAutoscaling.On(
					"SetDesiredCapacity",
					mock.Anything,
					mock.MatchedBy(func(in *autoscaling.SetDesiredCapacityInput) bool {
						setCapacityInput = in
						return true
					}),
					mock.Anything,
				)
			})

			g.JustBeforeEach(func() { err = sut.ScaleUpASG(ctx, desiredCapacity) })

			g.Describe("when the set capacity call fails", func() {
				g.BeforeEach(func() { setCapacityCall.Return(nil, errors.New("bacon")) })

				g.It("send the correct input", func() {
					Expect(setCapacityInput).NotTo(BeNil())
					Expect(*setCapacityInput.AutoScalingGroupName).To(Equal(asgName))
					Expect(*setCapacityInput.DesiredCapacity).To(BeEquivalentTo(desiredCapacity))
				})

				g.It("should return an error", func() {
					Expect(err).To(MatchError("could not set desired capacity: bacon"))
				})
			})

			g.Describe("when the set capacity call succeeds", func() {
				g.BeforeEach(func() { setCapacityCall.Return(nil, nil) })

				g.It("succeeds", func() { Expect(err).NotTo(HaveOccurred()) })
			})
		})
	})
}
