package internal_test

import (
	"encoding/json"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/franela/goblin"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/spacelift-io/awsautoscalr/internal"
)

func TestState_StrayInstances(t *testing.T) {
	const asgName = "asg-name"
	const instanceID = "instance-id"
	const failedToTerminateInstanceID = "instance-id2"
	asg := &types.AutoScalingGroup{
		AutoScalingGroupName: nullable(asgName),
		MinSize:              nullable(int32(1)),
		MaxSize:              nullable(int32(5)),
		DesiredCapacity:      nullable(int32(3)),
		Instances: []types.Instance{
			{
				InstanceId: nullable(instanceID),
			},
		},
	}
	workerPool := &internal.WorkerPool{
		Workers: []internal.Worker{
			{
				Metadata: mustJSON(map[string]any{
					"asg_id":      asgName,
					"instance_id": instanceID,
				}),
			},
			{
				Drained: true,
				Metadata: mustJSON(map[string]any{
					"asg_id":      asgName,
					"instance_id": failedToTerminateInstanceID,
				}),
			},
		},
	}

	state, err := internal.NewState(workerPool, asg)
	require.NoError(t, err)

	strayInstances := state.StrayInstances()
	assert.Equal(t, []string{failedToTerminateInstanceID}, strayInstances)
}

func TestState(t *testing.T) {
	g := goblin.Goblin(t)
	RegisterFailHandler(func(m string, _ ...int) { g.Fail(m) })

	g.Describe("State", func() {
		var asg *types.AutoScalingGroup
		var workerPool *internal.WorkerPool

		var sut *internal.State

		g.Describe("NewState", func() {
			const asgName = "asg-name"

			var err error

			g.BeforeEach(func() {
				asg = &types.AutoScalingGroup{
					AutoScalingGroupName: nullable(asgName),
					MinSize:              nullable(int32(1)),
					MaxSize:              nullable(int32(5)),
					DesiredCapacity:      nullable(int32(3)),
				}
				workerPool = &internal.WorkerPool{}
			})

			g.JustBeforeEach(func() { sut, err = internal.NewState(workerPool, asg) })

			g.Describe("when the ASG is invalid", func() {
				g.Describe("when the name is not set", func() {
					g.BeforeEach(func() { asg.AutoScalingGroupName = nil })

					g.It("should return an error", func() {
						Expect(err).To(MatchError("ASG name is not set"))
					})
				})

				g.Describe("when the minimum size is not set", func() {
					g.BeforeEach(func() { asg.MinSize = nil })

					g.It("should return an error", func() {
						Expect(err).To(MatchError("ASG minimum size is not set"))
					})
				})

				g.Describe("when the maximum size is not set", func() {
					g.BeforeEach(func() { asg.MaxSize = nil })

					g.It("should return an error", func() {
						Expect(err).To(MatchError("ASG maximum size is not set"))
					})
				})

				g.Describe("when the desired capacity is not set", func() {
					g.BeforeEach(func() { asg.DesiredCapacity = nil })

					g.It("should return an error", func() {
						Expect(err).To(MatchError("ASG desired capacity is not set"))
					})
				})
			})

			g.Describe("when the ASG is valid", func() {
				g.Describe("when a worker does not have the required metadata", func() {
					g.BeforeEach(func() {
						workerPool.Workers = []internal.Worker{{
							Metadata: mustJSON(map[string]any{}),
						}}
					})

					g.It("should return an error", func() {
						Expect(err.Error()).To(ContainSubstring("metadata asg_id not present"))
						Expect(err.Error()).To(ContainSubstring("metadata instance_id not present"))
					})
				})

				g.Describe("when the worker does not belong to the ASG", func() {
					g.BeforeEach(func() {
						workerPool.Workers = []internal.Worker{{
							Metadata: mustJSON(map[string]any{
								"asg_id":      "other-asg",
								"instance_id": "i-1234567890",
							}),
						}}
					})

					g.It("should return an error", func() {
						Expect(err).To(MatchError("incorrect worker ASG: other-asg"))
					})
				})
			})

			// The StrayInstances function requires precalculated data from the
			// State constructor, so it's tested here.
			g.Describe("StrayInstances", func() {
				const instanceID = "i-1234567890"

				var instanceIDs []string

				g.BeforeEach(func() {
					asg.Instances = []types.Instance{{
						InstanceId:     nullable(instanceID),
						LifecycleState: types.LifecycleStateInService,
					}}
				})

				g.JustBeforeEach(func() { instanceIDs = sut.StrayInstances() })

				g.Describe("with no workers", func() {
					g.Describe("when the ASG instance is in service", func() {
						g.It("should return the instance as stray", func() {
							Expect(instanceIDs).To(ConsistOf(instanceID))
						})
					})

					g.Describe("when the ASG instance is not in service", func() {
						g.BeforeEach(func() { asg.Instances[0].LifecycleState = types.LifecycleStateTerminating })

						g.It("should return an empty collection", func() {
							Expect(instanceIDs).To(BeEmpty())
						})
					})
				})

				g.Describe("with a worker", func() {
					g.Describe("when it matches the ASG instance", func() {
						g.BeforeEach(func() {
							workerPool.Workers = []internal.Worker{{
								Metadata: mustJSON(map[string]any{
									"asg_id":      asgName,
									"instance_id": instanceID,
								}),
							}}
						})

						g.It("should return an empty collection", func() {
							Expect(instanceIDs).To(BeEmpty())
						})
					})

					g.Describe("when it does not match the ASG instance", func() {
						g.BeforeEach(func() {
							workerPool.Workers = []internal.Worker{{
								Metadata: mustJSON(map[string]any{
									"asg_id":      asgName,
									"instance_id": "i-0987654321",
								}),
							}}
						})

						g.It("should return the instance as stray", func() {
							Expect(instanceIDs).To(ConsistOf(instanceID))
						})
					})
				})
			})
		})

		g.Describe("IdleWorkers", func() {
			var idleWorkers []internal.Worker

			g.JustBeforeEach(func() { idleWorkers = sut.IdleWorkers() })

			g.BeforeEach(func() {
				workerPool.Workers = []internal.Worker{
					{Busy: true, ID: "busy"},
					{Busy: false, ID: "idle"},
				}
			})

			g.It("should return the idle workers", func() {
				Expect(idleWorkers).To(HaveLen(1))
				Expect(idleWorkers[0].ID).To(Equal("idle"))
			})
		})

		g.Describe("Decide", func() {
			var maxCreate, maxKill int

			var decision internal.Decision

			g.BeforeEach(func() {
				maxCreate = 2
				maxKill = 2

				asg = &types.AutoScalingGroup{
					MinSize: nullable(int32(0)),
					MaxSize: nullable(int32(2)),
				}
				workerPool = &internal.WorkerPool{}

				sut = &internal.State{
					WorkerPool: workerPool,
					ASG:        asg,
				}
			})

			g.JustBeforeEach(func() {
				decision = sut.Decide(maxCreate, maxKill)
			})

			g.Describe("when there are no workers", func() {
				g.Describe("when there are no pending runs (default)", func() {
					g.Describe("when there are no instances", func() {
						g.It("should not scale because the system is idle", func() {
							Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionNone))
							Expect(decision.ScalingSize).To(BeZero())
							Expect(decision.Comments).To(Equal([]string{
								"autoscaling group exactly at the right size",
							}))
						})
					})

					g.Describe("when there are instances", func() {
						g.BeforeEach(func() { asg.Instances = []types.Instance{{}} })

						g.It("should not scale because the system is not in balance", func() {
							Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionNone))
							Expect(decision.ScalingSize).To(BeZero())
							Expect(decision.Comments).To(Equal([]string{
								"number of workers does not match the number of instances in the ASG",
							}))
						})
					})
				})

				g.Describe("when there are pending runs (scaling up scenarios)", func() {
					g.BeforeEach(func() { workerPool.PendingRuns = 5 })

					g.Describe("when the ASG is already at maximum size", func() {
						g.BeforeEach(func() {
							asg.Instances = []types.Instance{{}, {}}
							workerPool.Workers = []internal.Worker{{}, {}}
						})

						g.It("should not scale because the system is at maximum size", func() {
							Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionNone))
							Expect(decision.ScalingSize).To(BeZero())
							Expect(decision.Comments).To(Equal([]string{
								"autoscaling group is already at maximum size",
							}))
						})
					})

					g.Describe("when the ASG is not at maximum size", func() {
						g.BeforeEach(func() { asg.DesiredCapacity = nullable(int32(0)) })

						g.Describe("when constrained by maxCreate", func() {
							g.BeforeEach(func() { maxCreate = 1 })

							g.It("scales up by 1", func() {
								Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionUp))
								Expect(decision.ScalingSize).To(Equal(1))
								Expect(decision.Comments).To(Equal([]string{
									"need 5 workers, but can only create 1",
									"adding workers to match pending runs",
								}))
							})
						})

						g.Describe("when not constrained by maxCreate", func() {
							g.BeforeEach(func() { maxCreate = 10 })

							g.Describe("when not constrained by max ASG size", func() {
								g.BeforeEach(func() { asg.MaxSize = nullable(int32(10)) })

								g.It("scales up by 5", func() {
									Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionUp))
									Expect(decision.ScalingSize).To(Equal(5))
									Expect(decision.Comments).To(Equal([]string{"adding workers to match pending runs"}))
								})
							})

							g.Describe("when constrained by max ASG size", func() {
								g.BeforeEach(func() { asg.MaxSize = nullable(int32(2)) })

								g.It("scales up by 2", func() {
									Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionUp))
									Expect(decision.ScalingSize).To(Equal(2))
									Expect(decision.Comments).To(Equal([]string{
										"adding workers to match pending runs, up to the ASG max size",
									}))
								})
							})
						})
					})
				})

				g.Describe("when there are no pending runs (scaling down scenarios)", func() {
					g.BeforeEach(func() {
						asg.DesiredCapacity = nullable(int32(2))
						asg.Instances = []types.Instance{{}, {}}
						asg.MaxSize = nullable(int32(10))
						workerPool.Workers = []internal.Worker{{}, {}}
					})

					g.Describe("when the ASG is already at minimum size", func() {
						g.BeforeEach(func() { asg.MinSize = nullable(int32(2)) })

						g.It("should not scale because the system is at minimum size", func() {
							Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionNone))
							Expect(decision.ScalingSize).To(BeZero())
							Expect(decision.Comments).To(Equal([]string{
								"autoscaling group is already at minimum size",
							}))
						})
					})

					g.Describe("when the ASG is not at minimum size", func() {
						g.BeforeEach(func() { asg.MinSize = nullable(int32(0)) })

						g.Describe("when constrained by maxKill", func() {
							g.BeforeEach(func() { maxKill = 1 })

							g.It("scales down by 1", func() {
								Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionDown))
								Expect(decision.ScalingSize).To(Equal(1))
								Expect(decision.Comments).To(Equal([]string{
									"need to kill 2 workers, but can only kill 1",
									"removing idle workers",
								}))
							})
						})

						g.Describe("when not constrained by maxKill", func() {
							g.BeforeEach(func() { maxKill = 10 })

							g.Describe("when constrained by min ASG size", func() {
								g.BeforeEach(func() { asg.MinSize = nullable(int32(1)) })

								g.It("scales down by 1", func() {
									Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionDown))
									Expect(decision.ScalingSize).To(Equal(1))
									Expect(decision.Comments).To(Equal([]string{
										"need to kill 2 workers, but can't get below minimum size of 1",
										"removing idle workers",
									}))
								})
							})

							g.Describe("when not constrained by min ASG size", func() {
								g.BeforeEach(func() { asg.MinSize = nullable(int32(0)) })

								g.It("scales down by 2", func() {
									Expect(decision.ScalingDirection).To(Equal(internal.ScalingDirectionDown))
									Expect(decision.ScalingSize).To(Equal(2))
									Expect(decision.Comments).To(Equal([]string{"removing idle workers"}))
								})
							})
						})
					})
				})
			})
		})
	})
}

func nullable[T any](t T) *T {
	out := t
	return &out
}

func mustJSON(T any) string {
	out, err := json.Marshal(T)
	if err != nil {
		panic(err)
	}
	return string(out)
}
