package internal

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spacelift-io/awsautoscalr/internal"
)

// ControllerFactory creates a controller from the parsed config.
type ControllerFactory func(ctx context.Context, cfg *internal.RuntimeConfig) (internal.ControllerInterface, error)

// Handle processes the autoscaling request with pre-parsed config.
func Handle(ctx context.Context, logger *slog.Logger, cfg *internal.RuntimeConfig, factory ControllerFactory) error {
	controller, err := factory(ctx, cfg)
	if err != nil {
		return fmt.Errorf("could not create controller: %w", err)
	}

	return internal.NewAutoScaler(controller, logger).Scale(ctx, *cfg)
}
