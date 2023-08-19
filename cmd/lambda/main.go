package main

import (
	"context"
	"fmt"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-lambda-go/lambdacontext"
	"github.com/aws/aws-xray-sdk-go/xray"
	"golang.org/x/exp/slog"

	"github.com/spacelift-io/awsautoscalr/cmd/internal"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	lambda.Start(func(ctx context.Context) error {
		if err := xray.Configure(xray.Config{ServiceVersion: "1.2.3"}); err != nil {
			return fmt.Errorf("could not configure X-Ray: %w", err)
		}

		if lc, ok := lambdacontext.FromContext(ctx); ok {
			logger = logger.With("aws_request_id", lc.AwsRequestID)
		}

		return internal.Handle(ctx, logger)
	})
}
