package ifaces

import (
	"context"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
)

// AzureCompute is an interface for the Azure Compute client.
//
//go:generate mockery --output ./ --name AzureCompute --filename mock_azure_compute.go --outpkg ifaces --structname MockAzureCompute
type AzureCompute interface {
	GetVMScaleSet(ctx context.Context, resourceGroupName string, vmScaleSetName string) (*armcompute.VirtualMachineScaleSet, error)
	ListVMScaleSetVMs(ctx context.Context, resourceGroupName string, vmScaleSetName string) ([]*armcompute.VirtualMachineScaleSetVM, error)
	GetVMScaleSetVM(ctx context.Context, resourceGroupName string, vmScaleSetName string, instanceID string) (*armcompute.VirtualMachineScaleSetVM, error)
	UpdateVMScaleSetCapacity(ctx context.Context, resourceGroupName string, vmScaleSetName string, capacity int64) error
	DeleteVMScaleSetVM(ctx context.Context, resourceGroupName string, vmScaleSetName string, instanceID string) error
}
