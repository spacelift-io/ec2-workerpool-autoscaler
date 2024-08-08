package internal

import (
	"context"
	"fmt"
	"github.com/caarlos0/env/v9"
	"github.com/spacelift-io/awsautoscalr/internal"
	"golang.org/x/exp/slog"
)

func Handle(ctx context.Context, logger *slog.Logger) error {
	var cfg internal.RuntimeConfig
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("could not parse environment variables: %w", err)
	}

	controller, err := internal.NewController(ctx, &cfg)
	if err != nil {
		return fmt.Errorf("could not create controller: %w", err)
	}
	return internal.NewAutoScaler(controller, logger).Scale(ctx, cfg)
}
