package main

import (
	"context"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"golang.org/x/exp/slog"

	"github.com/spacelift-io/awsautoscalr/cmd/internal"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	lambda.Start(func(ctx context.Context) error {
		if lc, ok := lambdacontext.FromContext(ctx); ok {
			logger = logger.With("aws_request_id", lc.AwsRequestID)
		}

		return internal.Handle(ctx, logger)
	})
}
