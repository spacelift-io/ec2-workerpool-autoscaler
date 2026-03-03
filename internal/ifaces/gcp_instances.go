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
	// ListInstances retrieves instances in a zone matching the given filter.
	// The filter uses GCP's list filter syntax (e.g., `name eq "prefix-.*"`).
	ListInstances(ctx context.Context, project, zone, filter string) ([]*computepb.Instance, error)

	// Close releases resources held by the client.
	Close() error
}
