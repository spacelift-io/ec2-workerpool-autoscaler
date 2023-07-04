package internal

type WorkerPool struct {
	PendingRuns int32    `graphql:"pendingRuns" json:"pendingRuns"`
	Workers     []Worker `graphql:"workers" json:"workers"`
}

type WorkerPoolDetails struct {
	Pool *WorkerPool `graphql:"workerPool(id: $workerPool)"`
}
