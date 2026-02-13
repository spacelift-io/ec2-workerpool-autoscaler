package ifaces

import (
	"context"

	"cloud.google.com/go/compute/apiv1/computepb"
)

// GCPInstances is an interface for the GCP Compute Engine Instances client.
// It abstracts operations on individual Compute instances (which are always zonal).
//
//go:generate mockery --output ./ --name GCPInstances --filename mock_gcp_instances.go --outpkg ifaces --structname MockGCPInstances
type GCPInstances interface {
	// GetInstance retrieves details about a specific compute instance.
	// Instances are always zonal in GCP, regardless of whether the IGM is zonal or regional.
	GetInstance(ctx context.Context, project, zone, instanceName string) (*computepb.Instance, error)

	// Close releases resources held by the client.
	Close() error
}
