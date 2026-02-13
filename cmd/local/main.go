package main

import (
	"context"
	"flag"
	"log/slog"
	"os"

	cmdinternal "github.com/spacelift-io/awsautoscalr/cmd/internal"
	spaceliftinternal "github.com/spacelift-io/awsautoscalr/internal"
	"github.com/spacelift-io/awsautoscalr/internal/tracing"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func main() {
	debug := flag.Bool("d", false, "enable debug tracing (logs spans to stdout)")
	flag.BoolVar(debug, "debug", false, "enable debug tracing (logs spans to stdout)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	// Detect platform at startup based on environment variables
	var cfg spaceliftinternal.RuntimeConfig
	var factory cmdinternal.ControllerFactory
	var platform spaceliftinternal.Platform

	if os.Getenv("GCP_IGM_SELF_LINK") != "" {
		// GCP platform detected
		if err := cfg.Parse(spaceliftinternal.PlatformGCP); err != nil {
			logger.Error("failed to parse GCP configuration", "error", err)
			os.Exit(1)
		}
		platform = spaceliftinternal.PlatformGCP
		factory = spaceliftinternal.NewGCPController
		logger.Info("Detected GCP platform")
	} else if os.Getenv("AZURE_VMSS_RESOURCE_ID") != "" {
		// Azure platform detected
		if err := cfg.Parse(spaceliftinternal.PlatformAzure); err != nil {
			logger.Error("failed to parse Azure configuration", "error", err)
			os.Exit(1)
		}
		platform = spaceliftinternal.PlatformAzure
		factory = spaceliftinternal.NewAzureController
		logger.Info("Detected Azure platform")
	} else {
		// Default to AWS platform
		if err := cfg.Parse(spaceliftinternal.PlatformAWS); err != nil {
			logger.Error("failed to parse AWS configuration", "error", err)
			os.Exit(1)
		}
		platform = spaceliftinternal.PlatformAWS
		factory = spaceliftinternal.NewAWSController
		logger.Info("Detected AWS platform")
	}

	var tp *sdktrace.TracerProvider
	if *debug {
		exporter, err := stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			logger.Error("failed to create stdout trace exporter", "error", err)
			os.Exit(1)
		}
		tp = sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter))
		otel.SetTracerProvider(tp)
		logger.Info("Debug tracing enabled, spans will be logged to stdout")
	} else if platform == spaceliftinternal.PlatformAWS {
		tp = tracing.InitOtelXrayTracer(ctx, logger, false)
	}

	if tp != nil {
		defer func(ctx context.Context) {
			err := tp.Shutdown(ctx)
			if err != nil {
				logger.Error("error shutting down tracer provider", "error", err)
			}
		}(ctx)
	}

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
