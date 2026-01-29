package main

import (
	"context"
	"log/slog"
	"os"

	cmdinternal "github.com/spacelift-io/awsautoscalr/cmd/internal"
	spaceliftinternal "github.com/spacelift-io/awsautoscalr/internal"
	"github.com/spacelift-io/awsautoscalr/internal/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	// Detect platform at startup based on environment variables
	var cfg spaceliftinternal.RuntimeConfig
	var factory cmdinternal.ControllerFactory

	if os.Getenv("AZURE_VMSS_RESOURCE_ID") != "" {
		// Azure platform detected
		if err := cfg.Parse(spaceliftinternal.PlatformAzure); err != nil {
			logger.Error("failed to parse Azure configuration", "error", err)
			os.Exit(1)
		}
		factory = spaceliftinternal.NewAzureController
		logger.Info("Detected Azure platform")
	} else {
		// Default to AWS platform
		if err := cfg.Parse(spaceliftinternal.PlatformAWS); err != nil {
			logger.Error("failed to parse AWS configuration", "error", err)
			os.Exit(1)
		}
		factory = spaceliftinternal.NewAWSController
		logger.Info("Detected AWS platform")
	}

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

	if err := cmdinternal.Handle(ctx, logger, &cfg, factory); err != nil {
		logger.With("msg", err.Error()).Error("could not handle request")
		span.RecordError(err)
		span.SetStatus(codes.Error, "")
		os.Exit(1)
	}
}
