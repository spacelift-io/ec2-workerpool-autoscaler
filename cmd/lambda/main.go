package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-lambda-go/otellambda"
	"go.opentelemetry.io/contrib/propagators/aws/xray"
	"go.opentelemetry.io/otel/propagation"

	"github.com/spacelift-io/awsautoscalr/cmd/internal"
	spaceliftinternal "github.com/spacelift-io/awsautoscalr/internal"
	"github.com/spacelift-io/awsautoscalr/internal/tracing"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx := context.Background()

	// Parse config at startup - fail fast on misconfiguration
	var cfg spaceliftinternal.RuntimeConfig
	if err := cfg.Parse(spaceliftinternal.PlatformAWS); err != nil {
		logger.Error("failed to parse configuration", "error", err)
		os.Exit(1)
	}

	// Parallel invocations each enforce AUTOSCALING_MAX_KILL alone, so together
	// they over-terminate. Since aws-lambda-go v1.54, _X_AMZN_TRACE_ID is also
	// only exported at concurrency 1, silently unparenting our spans above it.
	if concurrency := lambdacontext.MaxConcurrency(); concurrency > 1 {
		logger.Warn("AWS_LAMBDA_MAX_CONCURRENCY is greater than 1; concurrent invocations may over-terminate workers, and X-Ray trace parenting is disabled",
			"max_concurrency", concurrency)
	}

	tp := tracing.InitOtelXrayTracer(ctx, logger, true)
	defer func(ctx context.Context) {
		err := tp.Shutdown(ctx)
		if err != nil {
			logger.Error("error shutting down tracer provider", "error", err)
		}
	}(ctx)

	opts := []otellambda.Option{
		otellambda.WithTracerProvider(tp),
		otellambda.WithEventToCarrier(xrayEventToCarrier),
		otellambda.WithPropagator(xray.Propagator{}),
		otellambda.WithFlusher(tp),
	}

	lambda.Start(otellambda.InstrumentHandler(func(ctx context.Context) error {
		logger := logger
		if lc, ok := lambdacontext.FromContext(ctx); ok {
			logger = logger.With("aws_request_id", lc.AwsRequestID)
		}

		return internal.Handle(ctx, logger, &cfg, spaceliftinternal.NewAWSController)
	}, opts...))
}

func xrayEventToCarrier([]byte) propagation.TextMapCarrier {
	xrayTraceID := os.Getenv("_X_AMZN_TRACE_ID")
	return propagation.HeaderCarrier{"X-Amzn-Trace-Id": []string{xrayTraceID}}
}
