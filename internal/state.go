package internal

import (
	"fmt"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"time"
)

// State represents the state of the world, as far as the autoscaler is
// concerned. It takes into account the current state of the worker pool, and
// the current state of the autoscaling group.
type State struct {
	WorkerPool *WorkerPool
	ASG        *types.AutoScalingGroup

	inServiceInstanceIDs map[InstanceID]struct{}
	workersByInstanceID  map[InstanceID]Worker
	cfg                  RuntimeConfig
}

func NewState(workerPool *WorkerPool, asg *types.AutoScalingGroup, cfg RuntimeConfig) (*State, error) {
	workersByInstanceID := make(map[InstanceID]Worker)
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

		if string(groupID) == "" {
			return nil, fmt.Errorf("worker %s has empty ASG ID in metadata", worker.ID)
		}

		if string(groupID) != *asg.AutoScalingGroupName {
			return nil, fmt.Errorf("worker %s has incorrect ASG: %s (expected: %s)", worker.ID, groupID, *asg.AutoScalingGroupName)
		}

		workersByInstanceID[instanceID] = worker
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
		cfg:                  cfg,
	}, nil
}

// ScalableWorkers returns a list of workers that are not currently busy.
func (s *State) ScalableWorkers() []Worker {
	var out []Worker
	for _, worker := range s.WorkerPool.Workers {
		if worker.Busy {
			continue
		}

		// Even though the worker might be idle, we will give it some time to pick up more work
		// if the customer wants
		if s.cfg.AutoscalingScaleDownDelay != 0 {
			workerCreationTime := time.Unix(int64(worker.CreatedAt), 0)
			minimumAliveTime := workerCreationTime.Add(time.Duration(s.cfg.AutoscalingScaleDownDelay) * time.Minute)
			if !time.Now().After(minimumAliveTime) {
				continue
			}
		}

		out = append(out, worker)
	}

	return out
}

// StrayInstances returns a list of instance IDs that don't have a corresponding
// worker in the worker pool.
func (s *State) StrayInstances() []string {
	var res []string
	for instanceID := range s.inServiceInstanceIDs {
		if _, ok := s.workersByInstanceID[instanceID]; !ok {
			res = append(res, string(instanceID))
		}
	}

	res = append(res, s.detachedNotTerminatedInstances()...)

	return res
}

func (s *State) detachedNotTerminatedInstances() []string {
	instanceIDs := make(map[InstanceID]struct{})
	for _, instance := range s.ASG.Instances {
		instanceIDs[InstanceID(*instance.InstanceId)] = struct{}{}
	}

	var res []string
	for instanceID, worker := range s.workersByInstanceID {
		if !worker.Drained {
			continue
		}
		if _, ok := instanceIDs[instanceID]; ok {
			continue
		}

		res = append(res, string(instanceID))
	}
	return res
}

func (s *State) Decide(maxCreate, maxKill int) Decision {
	if len(s.WorkerPool.Workers) != len(s.ASG.Instances) {
		return Decision{
			ScalingDirection: ScalingDirectionNone,
			Comments:         []string{"number of workers does not match the number of instances in the ASG"},
		}
	}

	scalable := s.ScalableWorkers()

	difference := int(s.WorkerPool.PendingRuns) - len(scalable)

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
			Comments:         append(comments, fmt.Sprintf("adding %d workers to match pending runs", missingWorkers)),
		}
	}

	scalingSize := int(*s.ASG.MaxSize - *s.ASG.DesiredCapacity)

	return Decision{
		ScalingDirection: ScalingDirectionUp,
		ScalingSize:      scalingSize,
		Comments:         append(comments, fmt.Sprintf("adding %d workers to match pending runs, up to the ASG max size", scalingSize)),
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
		Comments:         append(comments, fmt.Sprintf("removing %d idle workers", extraWorkers)),
	}
}
