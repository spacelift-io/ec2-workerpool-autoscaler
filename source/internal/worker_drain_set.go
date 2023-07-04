package internal

type WorkerDrainSet struct {
	Worker Worker `graphql:"workerDrainSet(workerPool: $workerPoolId, id: $workerId, drain: $drain)"`
}
