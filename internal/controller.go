package internal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/shurcooL/graphql"
	spacelift "github.com/spacelift-io/spacectl/client"
	"github.com/spacelift-io/spacectl/client/session"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/spacelift-io/awsautoscalr/internal/ifaces"
)

// Controller is responsible for handling interactions with the Spacelift API
// so that the main package can focus on the core logic. It is embedded in the
// cloud-specific controllers (e.g., AWSController) to provide cloud-specific
// methods which are required by the ControllerInterface interface.
type Controller struct {
	// Clients.
	Spacelift ifaces.Spacelift

	// Configuration.
	SpaceliftWorkerPoolID string

	// Telemetry.
	Tracer trace.Tracer
}

func newSpaceliftClient(ctx context.Context, endpoint, keyID, keySecret string) (ifaces.Spacelift, error) {
	httpClient := &http.Client{
		Transport: otelhttp.NewTransport(
			http.DefaultTransport,
			otelhttp.WithSpanNameFormatter(func(operation string, r *http.Request) string {
				return r.Host
			}),
		),
	}

	slSession, err := session.FromAPIKey(ctx, httpClient)(endpoint, keyID, keySecret)

	if err != nil {
		return nil, fmt.Errorf("could not create Spacelift session: %w", err)
	}

	return spacelift.New(httpClient, slSession), nil
}

// GetWorkerPool returns the worker pool details from Spacelift.
func (c *Controller) GetWorkerPool(ctx context.Context) (out *WorkerPool, err error) {
	ctx, span := c.Tracer.Start(ctx, "spacelift.workerpool.get")
	defer span.End()

	var wpDetails WorkerPoolDetails

	if err = c.Spacelift.Query(ctx, &wpDetails, map[string]any{"workerPool": c.SpaceliftWorkerPoolID}); err != nil {
		err = fmt.Errorf("could not get Spacelift worker pool details: %w", err)
		return nil, err
	}

	if wpDetails.Pool == nil {
		err = errors.New("worker pool not found or not accessible")
		return nil, err
	}

	worker_index := 0
	for _, worker := range wpDetails.Pool.Workers {
		if !worker.Drained {
			wpDetails.Pool.Workers[worker_index] = worker
			worker_index++
		}
	}
	wpDetails.Pool.Workers = wpDetails.Pool.Workers[:worker_index]

	sort.Slice(wpDetails.Pool.Workers, func(i, j int) bool {
		return wpDetails.Pool.Workers[i].CreatedAt < wpDetails.Pool.Workers[j].CreatedAt
	})

	span.SetAttributes(
		attribute.Int("workers", len(wpDetails.Pool.Workers)),
		attribute.Int("pending_runs", int(wpDetails.Pool.PendingRuns)),
	)

	out = wpDetails.Pool

	return out, nil
}

// Drain worker drains a worker in the Spacelift worker pool.
func (c *Controller) DrainWorker(ctx context.Context, workerID string) (drained bool, err error) {
	ctx, span := c.Tracer.Start(ctx, "spacelift.worker.drain")
	defer span.End()

	span.SetAttributes(attribute.String("worker_id", workerID))

	var worker *Worker

	if worker, err = c.workerDrainSet(ctx, workerID, true); err != nil {
		err = fmt.Errorf("could not drain worker: %w", err)
		return false, err
	}

	span.SetAttributes(
		attribute.String("worker.id", worker.ID),
		attribute.Bool("worker.busy", worker.Busy),
		attribute.Bool("worker.drained", worker.Drained),
	)

	if !worker.Busy {
		drained = true
		return true, nil
	}

	if _, err = c.workerDrainSet(ctx, workerID, false); err != nil {
		err = fmt.Errorf("could not undrain a busy worker: %w", err)
		return false, err
	}

	return false, nil
}

func (c *Controller) workerDrainSet(ctx context.Context, workerID string, drain bool) (worker *Worker, err error) {
	ctx, span := c.Tracer.Start(ctx, fmt.Sprintf("spacelift.worker.setdrain.%t", drain))
	defer span.End()

	span.SetAttributes(
		attribute.String("worker_id", workerID),
		attribute.String("worker_pool_id", c.SpaceliftWorkerPoolID),
		attribute.Bool("drain", drain),
	)

	var mutation WorkerDrainSet

	variables := map[string]any{
		"workerPoolId": graphql.ID(c.SpaceliftWorkerPoolID),
		"workerId":     graphql.ID(workerID),
		"drain":        graphql.Boolean(drain),
	}

	if err = c.Spacelift.Mutate(ctx, &mutation, variables); err != nil {
		err = fmt.Errorf("could not set worker drain to %t: %w", drain, err)
		return nil, err
	}

	worker = &mutation.Worker

	return worker, nil
}
