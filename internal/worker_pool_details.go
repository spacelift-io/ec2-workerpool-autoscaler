package internal

type WorkerPool struct {
	PendingRuns int32    `graphql:"pendingRuns" json:"pendingRuns"`
	Workers     []Worker `graphql:"workers" json:"workers"`
}

func (wp *WorkerPool) ExtractDrainedWorkers() []Worker {
	drainedWorkers := make([]Worker, 0)
	nonDrainedWorkers := make([]Worker, 0)
	for _, worker := range wp.Workers {
		if worker.Drained {
			drainedWorkers = append(drainedWorkers, worker)
			continue
		}
		nonDrainedWorkers = append(nonDrainedWorkers, worker)
	}
	wp.Workers = nonDrainedWorkers
	return drainedWorkers
}

type WorkerPoolDetails struct {
	Pool *WorkerPool `graphql:"workerPool(id: $workerPool)"`
}
