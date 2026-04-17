package internal

type WorkerPool struct {
	PendingRuns int32    `graphql:"pendingRuns" json:"pendingRuns"`
	Workers     []Worker `graphql:"workers" json:"workers"`
}

type WorkerPoolDetails struct {
	Pool *WorkerPool `graphql:"workerPool(id: $workerPool)"`
}

// WorkerPoolLegacy is used when querying backends that don't support availableAt.
type WorkerPoolLegacy struct {
	PendingRuns int32          `graphql:"pendingRuns" json:"pendingRuns"`
	Workers     []WorkerLegacy `graphql:"workers" json:"workers"`
}

// WorkerPoolDetailsLegacy is used when querying backends that don't support availableAt.
type WorkerPoolDetailsLegacy struct {
	Pool *WorkerPoolLegacy `graphql:"workerPool(id: $workerPool)"`
}
