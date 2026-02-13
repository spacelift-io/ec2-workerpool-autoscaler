package ifaces

import (
	"context"

	"cloud.google.com/go/compute/apiv1/computepb"
)

// GCPCompute is an interface for the GCP Compute Engine client.
// It abstracts operations on Instance Group Managers (IGMs).
// Instance-specific operations are handled by the GCPInstances interface.
//
//go:generate mockery --output ./ --name GCPCompute --filename mock_gcp_compute.go --outpkg ifaces --structname MockGCPCompute
type GCPCompute interface {
	// GetInstanceGroupManager retrieves details about an Instance Group Manager.
	// For zonal IGMs, location is the zone (e.g., "us-central1-a").
	// For regional IGMs, location is the region (e.g., "us-central1").
	GetInstanceGroupManager(ctx context.Context, project, location, name string) (*computepb.InstanceGroupManager, error)

	// ListManagedInstances returns all instances in the IGM.
	ListManagedInstances(ctx context.Context, project, location, name string) ([]*computepb.ManagedInstance, error)

	// ResizeIGM changes the target size of the IGM.
	ResizeIGM(ctx context.Context, project, location, name string, newSize int64) error

	// DeleteInstance removes a specific instance from the IGM.
	// instanceURL is the full URL or resource path of the instance.
	DeleteInstance(ctx context.Context, project, location, igmName, instanceURL string) error

	// Close releases resources held by the client.
	Close() error
}
