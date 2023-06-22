package internal

type WorkerPoolDetails struct {
	Pool *struct {
		PendingRuns int32    `graphql:"pendingRuns" json:"pendingRuns"`
		Workers     []Worker `graphql:"workers" json:"workers"`
	} `graphql:"workerPool(id: $workerPool)"`
}
