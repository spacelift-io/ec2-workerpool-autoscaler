package internal

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
)

// State represents the state of the world, as far as the autoscaler is
// concerned. It takes into account the current state of the worker pool, and
// the current state of the autoscaling group.
type State struct {
	WorkerPool *WorkerPool
	ASG        *types.AutoScalingGroup

	inServiceInstanceIDs map[InstanceID]struct{}
	workersByInstanceID  map[InstanceID]*Worker
}

func NewState(workerPool *WorkerPool, asg *types.AutoScalingGroup) (*State, error) {
	workersByInstanceID := make(map[InstanceID]*Worker)
	inServiceInstanceIDs := make(map[InstanceID]struct{})

	// Validate the ASG.
	if asg.AutoScalingGroupName == nil {
		return nil, fmt.Errorf("ASG name is not set")
	}

	if asg.MinSize == nil {
		return nil, fmt.Errorf("ASG minimum size is not set")
	}

	if asg.MaxSize == nil {
		return nil, fmt.Errorf("ASG maximum size is not set")
	}

	if asg.DesiredCapacity == nil {
		return nil, fmt.Errorf("ASG desired capacity is not set")
	}

	for _, worker := range workerPool.Workers {
		groupID, instanceID, err := worker.InstanceIdentity()

		if err != nil {
			return nil, err
		}

		if string(groupID) != *asg.AutoScalingGroupName {
			return nil, fmt.Errorf("incorrect worker ASG: %s", groupID)
		}

		workersByInstanceID[instanceID] = &worker
	}

	for _, instance := range asg.Instances {
		if instance.LifecycleState != types.LifecycleStateInService {
			continue
		}

		inServiceInstanceIDs[InstanceID(*instance.InstanceId)] = struct{}{}
	}

	return &State{
		WorkerPool:           workerPool,
		ASG:                  asg,
		inServiceInstanceIDs: inServiceInstanceIDs,
		workersByInstanceID:  workersByInstanceID,
	}, nil
}

// IdleWorkers returns a list of workers that are not currently busy.
func (s *State) IdleWorkers() []Worker {
	var out []Worker

	for _, worker := range s.WorkerPool.Workers {
		if worker.Busy {
			continue
		}

		out = append(out, worker)
	}

	return out
}

// StrayInstances returns a list of instance IDs that don't have a corresponding
// worker in the worker pool.
func (s *State) StrayInstances() (out []string) {
	for instanceID := range s.inServiceInstanceIDs {
		if _, ok := s.workersByInstanceID[instanceID]; !ok {
			out = append(out, string(instanceID))
		}
	}

	return
}

func (s *State) Decision(maxCreate, maxKill int) Decision {
	if len(s.WorkerPool.Workers) != len(s.ASG.Instances) {
		return Decision{
			ScalingDirection: ScalingDirectionNone,
			Comments:         []string{"number of workers does not match the number of instances in the ASG"},
		}
	}

	idle := s.IdleWorkers()

	difference := int(s.WorkerPool.PendingRuns) - len(idle)

	if difference > 0 {
		return s.determineScaleUp(difference, maxCreate)
	}

	if difference < 0 {
		return s.determineScaleDown(-difference, maxKill)
	}

	return Decision{
		ScalingDirection: ScalingDirectionNone,
		Comments:         []string{"autoscaling group exactly at the right size"},
	}
}

func (s *State) determineScaleUp(missingWorkers, maxCreate int) Decision {
	if len(s.WorkerPool.Workers) >= int(*s.ASG.MaxSize) {
		return Decision{
			ScalingDirection: ScalingDirectionNone,
			Comments:         []string{"autoscaling group is already at maximum size"},
		}
	}

	var comments []string

	if missingWorkers > maxCreate {
		comments = append(comments, fmt.Sprintf("need %d workers, but can only create %d", missingWorkers, maxCreate))
		missingWorkers = maxCreate
	}

	newASGCapacity := *s.ASG.DesiredCapacity + int32(missingWorkers)

	if newASGCapacity <= *s.ASG.MaxSize {
		return Decision{
			ScalingDirection: ScalingDirectionUp,
			ScalingSize:      missingWorkers,
			Comments:         append(comments, "adding workers to match pending runs"),
		}
	}

	return Decision{
		ScalingDirection: ScalingDirectionUp,
		ScalingSize:      int(*s.ASG.MaxSize - *s.ASG.DesiredCapacity),
		Comments:         append(comments, "adding workers to match pending runs, up to the ASG max size"),
	}
}

func (s *State) determineScaleDown(extraWorkers, maxKill int) Decision {
	if len(s.WorkerPool.Workers) <= int(*s.ASG.MinSize) {
		return Decision{
			ScalingDirection: ScalingDirectionNone,
			Comments:         []string{"autoscaling group is already at minimum size"},
		}
	}

	var comments []string

	if extraWorkers > maxKill {
		comments = append(comments, fmt.Sprintf("need to kill %d workers, but can only kill %d", extraWorkers, maxKill))
		extraWorkers = maxKill
	}

	if overMinimum := int(*s.ASG.DesiredCapacity - *s.ASG.MinSize); extraWorkers > overMinimum {
		comments = append(comments, fmt.Sprintf("need to kill %d workers, but can't get below minimum size of %d", extraWorkers, *s.ASG.MinSize))
		extraWorkers = overMinimum
	}

	return Decision{
		ScalingDirection: ScalingDirectionDown,
		ScalingSize:      extraWorkers,
		Comments:         append(comments, "removing idle workers"),
	}
}
