package internal

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/caarlos0/env/v9"

	"github.com/spacelift-io/awsautoscalr/internal"
)

func Handle(ctx context.Context, logger *slog.Logger) error {
	var cfg internal.RuntimeConfig
	if err := env.Parse(&cfg); err != nil {
		return fmt.Errorf("could not parse environment variables: %w", err)
	}

	// Detect cloud provider based on the AutoscalingGroupARN format
	var controller internal.ControllerInterface
	var err error

	if isAzureResourceID(cfg.AutoscalingGroupARN) {
		controller, err = internal.NewAzureController(ctx, &cfg)
		if err != nil {
			return fmt.Errorf("could not create Azure controller: %w", err)
		}
		logger.Info("Using Azure VMSS controller", "vmss", cfg.AutoscalingGroupARN)
	} else {
		controller, err = internal.NewAWSController(ctx, &cfg)
		if err != nil {
			return fmt.Errorf("could not create AWS controller: %w", err)
		}
		logger.Info("Using AWS ASG controller", "asg", cfg.AutoscalingGroupARN)
	}

	return internal.NewAutoScaler(controller, logger).Scale(ctx, cfg)
}

// isAzureResourceID checks if the given resource identifier is an Azure resource ID.
// Azure resource IDs follow the format:
// /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/{resourceProvider}/...
func isAzureResourceID(resourceID string) bool {
	return strings.HasPrefix(resourceID, "/subscriptions/") &&
		strings.Contains(resourceID, "/resourceGroups/") &&
		strings.Contains(resourceID, "/providers/Microsoft.Compute/virtualMachineScaleSets/")
}
