package internal_test

import (
	"testing"

	"github.com/franela/goblin"
	. "github.com/onsi/gomega"

	"gh.com/mw/autoscalr/internal"
)

func TestWorker(t *testing.T) {
	g := goblin.Goblin(t)
	RegisterFailHandler(func(m string, _ ...int) { g.Fail(m) })

	g.Describe("Worker", func() {
		var sut *internal.Worker

		g.BeforeEach(func() { sut = &internal.Worker{} })

		g.Describe("InstanceIdentity", func() {
			var groupID internal.GroupID
			var instanceID internal.InstanceID
			var err error

			g.JustBeforeEach(func() { groupID, instanceID, err = sut.InstanceIdentity() })

			g.Describe("with no metadata", func() {
				g.BeforeEach(func() { sut.Metadata = "{}" })

				g.It("should return an error", func() {
					Expect(err).To(HaveOccurred())
					Expect(err.Error()).To(ContainSubstring("metadata asg_id not present"))
					Expect(err.Error()).To(ContainSubstring("metadata instance_id not present"))
				})
			})

			g.Describe("with valid metadata", func() {
				g.BeforeEach(func() {
					sut.Metadata = `{"asg_id": "group", "instance_id": "instance"}`
				})

				g.It("should return the group and instance IDs", func() {
					Expect(err).NotTo(HaveOccurred())
					Expect(groupID).To(Equal(internal.GroupID("group")))
					Expect(instanceID).To(Equal(internal.InstanceID("instance")))
				})
			})
		})
	})
}
