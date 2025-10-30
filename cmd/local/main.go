package main

import (
	"context"
	"log/slog"
	"os"

	cmdinternal "github.com/spacelift-io/awsautoscalr/cmd/internal"
	"github.com/spacelift-io/awsautoscalr/internal/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	tp := tracing.InitOtelXrayTracer(ctx, logger, false)
	defer func(ctx context.Context) {
		err := tp.Shutdown(ctx)
		if err != nil {
			logger.Error("error shutting down tracer provider", "error", err)
		}
	}(ctx)

	t := otel.Tracer("local")
	ctx, span := t.Start(ctx, "autoscaling")
	defer span.End()

	if err := cmdinternal.Handle(ctx, logger); err != nil {
		logger.With("msg", err.Error()).Error("could not handle request")
		span.RecordError(err)
		span.SetStatus(codes.Error, "")
		os.Exit(1)
	}
}
