package internal

// WorkerDrainSet is the workerDrainSet mutation. Its return type is WorkerLegacy
// (not Worker) on purpose: the drain result never needs availableAt, and
// selecting it would break self-hosted backends whose schema predates the field.
type WorkerDrainSet struct {
	Worker WorkerLegacy `graphql:"workerDrainSet(workerPool: $workerPoolId, id: $workerId, drain: $drain)"`
}
