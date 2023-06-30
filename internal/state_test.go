package internal_test

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/franela/goblin"
	. "github.com/onsi/gomega"

	"gh.com/mw/autoscalr/internal"
)

func TestState(t *testing.T) {
	g := goblin.Goblin(t)
	RegisterFailHandler(func(m string, _ ...int) { g.Fail(m) })

	g.Describe("State", func() {
		var sut *internal.State

		g.Describe("Decision", func() {
			var maxCreate, maxKill int

			var asg *types.AutoScalingGroup
			var workerPool *internal.WorkerPool

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
				decision = sut.Decision(maxCreate, maxKill)
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
