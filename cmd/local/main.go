package main

import (
	"context"
	"os"

	"golang.org/x/exp/slog"

	"github.com/aws/aws-xray-sdk-go/v2/xray"
	cmdinternal "github.com/spacelift-io/awsautoscalr/cmd/internal"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := xray.Configure(xray.Config{ServiceVersion: "1.2.3"}); err != nil {
		logger.With("msg", err.Error()).Error("could not configure X-Ray")
		os.Exit(1)
	}

	ctx, segment := xray.BeginSegment(context.Background(), "autoscaling")

	if err := cmdinternal.Handle(ctx, logger); err != nil {
		logger.With("msg", err.Error()).Error("could not handle request")
		segment.Close(err)
		os.Exit(1)
	}

	segment.Close(nil)
}
