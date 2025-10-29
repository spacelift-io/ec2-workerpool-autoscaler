package tracing

import (
	"context"
	"log/slog"
	"os"

	"github.com/aws-observability/aws-otel-go/exporters/xrayudp"
	lambdadetector "go.opentelemetry.io/contrib/detectors/aws/lambda"
	"go.opentelemetry.io/contrib/propagators/aws/xray"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
)

func InitOtelXrayTracer(ctx context.Context, logger *slog.Logger, isLambda bool) *trace.TracerProvider {
	opts := []trace.TracerProviderOption{}

	if isLambda {
		detector := lambdadetector.NewResourceDetector()
		lambdaResource, err := detector.Detect(ctx)
		if err != nil {
			logger.Error("failed to detect lambda resource attributes", "error", err)
			os.Exit(1)
		}
		opts = append(opts, trace.WithResource(lambdaResource))
	}

	udpExporter, err := xrayudp.NewSpanExporter(ctx)
	if err != nil {
		logger.Error("failed to initialize xray exporter", "error", err)
		os.Exit(1)
	}

	opts = append(opts, trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(udpExporter)))
	opts = append(opts, trace.WithIDGenerator(xray.NewIDGenerator()))
	tp := trace.NewTracerProvider(opts...)

	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(xray.Propagator{})

	return tp
}
