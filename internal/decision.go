package internal

// ScalingDirection represents the direction in which the autoscaler should
// scale.
type ScalingDirection int

const (
	ScalingDirectionNone ScalingDirection = iota
	ScalingDirectionUp
	ScalingDirectionDown
)

// Decision represents the decision made by the autoscaler.
type Decision struct {
	// Which direction to scale in.
	ScalingDirection ScalingDirection

	// How many instances to create or destroy.
	ScalingSize int

	// A comment to be added to the decision.
	Comments []string
}
