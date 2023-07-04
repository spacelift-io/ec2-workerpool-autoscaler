package main

import (
	"context"
	"os"

	"golang.org/x/exp/slog"

	cmdinternal "github.com/spacelift-io/awsautoscalr/cmd/internal"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := cmdinternal.Handle(context.Background(), logger); err != nil {
		logger.Error("handling failure: %v", err)
		os.Exit(1)
	}
}
