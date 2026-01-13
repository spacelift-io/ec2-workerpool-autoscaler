package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/spacelift-io/awsautoscalr/internal/ifaces"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
)

type AzureController struct {
	Controller

	// Clients.
	Compute  ifaces.AzureCompute
	KeyVault ifaces.AzureKeyVault

	// Configuration.
	AzureResourceGroupName string
	AzureVMSSName          string
}

// azureComputeClient wraps the Azure Compute SDK client to implement the AzureCompute interface.
type azureComputeClient struct {
	vmssClient   *armcompute.VirtualMachineScaleSetsClient
	vmssVMClient *armcompute.VirtualMachineScaleSetVMsClient
}

func (c *azureComputeClient) GetVMScaleSet(ctx context.Context, resourceGroupName string, vmScaleSetName string) (*armcompute.VirtualMachineScaleSet, error) {
	resp, err := c.vmssClient.Get(ctx, resourceGroupName, vmScaleSetName, nil)
	if err != nil {
		return nil, err
	}
	return &resp.VirtualMachineScaleSet, nil
}

func (c *azureComputeClient) ListVMScaleSetVMs(ctx context.Context, resourceGroupName string, vmScaleSetName string) ([]*armcompute.VirtualMachineScaleSetVM, error) {
	pager := c.vmssVMClient.NewListPager(resourceGroupName, vmScaleSetName, nil)
	var vms []*armcompute.VirtualMachineScaleSetVM

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		vms = append(vms, page.Value...)
	}

	return vms, nil
}

func (c *azureComputeClient) GetVMScaleSetVM(ctx context.Context, resourceGroupName string, vmScaleSetName string, instanceID string) (*armcompute.VirtualMachineScaleSetVM, error) {
	resp, err := c.vmssVMClient.Get(ctx, resourceGroupName, vmScaleSetName, instanceID, nil)
	if err != nil {
		return nil, err
	}
	return &resp.VirtualMachineScaleSetVM, nil
}

func (c *azureComputeClient) UpdateVMScaleSetCapacity(ctx context.Context, resourceGroupName string, vmScaleSetName string, capacity int64) error {
	// First, get the current VMSS to preserve other settings
	vmss, err := c.GetVMScaleSet(ctx, resourceGroupName, vmScaleSetName)
	if err != nil {
		return err
	}

	// Update only the capacity
	vmss.SKU.Capacity = &capacity

	poller, err := c.vmssClient.BeginCreateOrUpdate(ctx, resourceGroupName, vmScaleSetName, *vmss, nil)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

func (c *azureComputeClient) DeleteVMScaleSetVM(ctx context.Context, resourceGroupName string, vmScaleSetName string, instanceID string) error {
	poller, err := c.vmssVMClient.BeginDelete(ctx, resourceGroupName, vmScaleSetName, instanceID, nil)
	if err != nil {
		return err
	}

	_, err = poller.PollUntilDone(ctx, nil)
	return err
}

// azureKeyVaultClient wraps the Azure Key Vault SDK client to implement the AzureKeyVault interface.
type azureKeyVaultClient struct {
	client *azsecrets.Client
}

func (c *azureKeyVaultClient) GetSecret(ctx context.Context, secretName string) (azsecrets.GetSecretResponse, error) {
	return c.client.GetSecret(ctx, secretName, "", nil)
}

// NewAzureController creates a new Azure controller instance.
func NewAzureController(ctx context.Context, cfg *RuntimeConfig) (*AzureController, error) {
	// Create Azure credential
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("could not create Azure credential: %w", err)
	}

	// Parse the VMSS resource ID to extract resource group and VMSS name
	// Expected format: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Compute/virtualMachineScaleSets/{vmssName}
	resourceParts := strings.Split(cfg.AutoscalingGroupARN, "/")
	if len(resourceParts) < 9 {
		return nil, fmt.Errorf("could not parse Azure VMSS resource ID: invalid format")
	}

	var subscriptionID, resourceGroupName, vmssName string
	for i, part := range resourceParts {
		switch part {
		case "subscriptions":
			if i+1 < len(resourceParts) {
				subscriptionID = resourceParts[i+1]
			}
		case "resourceGroups":
			if i+1 < len(resourceParts) {
				resourceGroupName = resourceParts[i+1]
			}
		case "virtualMachineScaleSets":
			if i+1 < len(resourceParts) {
				vmssName = resourceParts[i+1]
			}
		}
	}

	if subscriptionID == "" || resourceGroupName == "" || vmssName == "" {
		return nil, fmt.Errorf("could not parse Azure VMSS resource ID: missing required components")
	}

	// Create Azure Compute clients
	vmssClient, err := armcompute.NewVirtualMachineScaleSetsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create Azure VMSS client: %w", err)
	}

	vmssVMClient, err := armcompute.NewVirtualMachineScaleSetVMsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create Azure VMSS VM client: %w", err)
	}

	computeClient := &azureComputeClient{
		vmssClient:   vmssClient,
		vmssVMClient: vmssVMClient,
	}

	// Create Azure Key Vault client
	// Parse Key Vault URL and secret name from config
	// Supported formats:
	//   1. Full URL: https://{vault-name}.vault.azure.net/secrets/{secret-name}
	//   2. Vault/secret: {vault-name}/{secret-name}
	//   3. Vault name only: {vault-name} (secret name must be provided separately)
	var vaultURL, secretName string
	input := cfg.SpaceliftAPISecretName

	if strings.HasPrefix(input, "https://") {
		// Format 1: Full URL
		if strings.Contains(input, "/secrets/") {
			parts := strings.Split(input, "/secrets/")
			vaultURL = parts[0]
			secretName = parts[1]
		} else {
			return nil, fmt.Errorf("invalid Key Vault URL format: %s (expected https://{vault}.vault.azure.net/secrets/{secret})", input)
		}
	} else if strings.Contains(input, "/") {
		// Format 2: Vault/secret pair
		parts := strings.SplitN(input, "/", 2)
		vaultURL = fmt.Sprintf("https://%s.vault.azure.net", parts[0])
		secretName = parts[1]
	} else {
		// Format 3: Vault name only - need additional config for secret name
		return nil, fmt.Errorf("invalid Key Vault configuration: %s (expected format: {vault}/{secret} or https://{vault}.vault.azure.net/secrets/{secret})", input)
	}

	kvClient, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create Azure Key Vault client: %w", err)
	}

	keyVaultClient := &azureKeyVaultClient{client: kvClient}

	// Get Spacelift API key from Key Vault
	secret, err := keyVaultClient.GetSecret(ctx, secretName)
	if err != nil {
		return nil, fmt.Errorf("could not get Spacelift API key secret from Key Vault: %w", err)
	}

	if secret.Value == nil {
		return nil, errors.New("could not find Spacelift API key secret value in Key Vault")
	}

	spaceliftClient, err := newSpaceliftClient(ctx, cfg.SpaceliftAPIEndpoint, cfg.SpaceliftAPIKeyID, *secret.Value)
	if err != nil {
		return nil, err
	}

	return &AzureController{
		Controller: Controller{
			Spacelift:             spaceliftClient,
			SpaceliftWorkerPoolID: cfg.SpaceliftWorkerPoolID,
			Tracer:                otel.Tracer("github.com/spacelift-io/awsautoscalr/internal/controller"),
		},
		Compute:                computeClient,
		KeyVault:               keyVaultClient,
		AzureResourceGroupName: resourceGroupName,
		AzureVMSSName:          vmssName,
	}, nil
}

// DescribeInstances returns the details of the given VM instances from Azure VMSS,
// making sure that the instances are valid for further processing.
func (c *AzureController) DescribeInstances(ctx context.Context, instanceIDs []string) (instances []Instance, err error) {
	ctx, span := c.Tracer.Start(ctx, "azure.vmss.describeVMs")
	defer span.End()

	for _, instanceID := range instanceIDs {
		var vm *armcompute.VirtualMachineScaleSetVM

		vm, err = c.Compute.GetVMScaleSetVM(ctx, c.AzureResourceGroupName, c.AzureVMSSName, instanceID)
		if err != nil {
			err = fmt.Errorf("could not describe VMSS VM instance %s: %w", instanceID, err)
			return nil, err
		}

		if vm.InstanceID == nil {
			err = errors.New("could not find VMSS VM instance ID")
			return nil, err
		}

		if vm.Properties == nil || vm.Properties.TimeCreated == nil {
			err = fmt.Errorf("could not find creation time for VMSS VM instance %s", *vm.InstanceID)
			return nil, err
		}

		instances = append(instances, Instance{
			ID:         *vm.InstanceID,
			LaunchTime: *vm.Properties.TimeCreated,
		})
	}

	return instances, nil
}

// GetVMSS returns the Azure Virtual Machine Scale Set (VMSS) details.
// This is an Azure-friendly alias for GetAutoscalingGroup.
func (c *AzureController) GetVMSS(ctx context.Context) (out *AutoScalingGroup, err error) {
	return c.GetAutoscalingGroup(ctx)
}

// GetAutoscalingGroup returns the Azure Virtual Machine Scale Set (VMSS) details.
//
// Note: This method implements the ControllerInterface, which uses AWS-centric naming
// (AutoScalingGroup), but it returns Azure VMSS details for consistency with the interface.
// For Azure-specific code, consider using GetVMSS() instead for clearer semantics.
func (c *AzureController) GetAutoscalingGroup(ctx context.Context) (out *AutoScalingGroup, err error) {
	ctx, span := c.Tracer.Start(ctx, "azure.vmss.get")
	defer span.End()

	var vmss *armcompute.VirtualMachineScaleSet

	vmss, err = c.Compute.GetVMScaleSet(ctx, c.AzureResourceGroupName, c.AzureVMSSName)
	if err != nil {
		err = fmt.Errorf("could not get Azure VMSS details: %w", err)
		return nil, err
	}

	if vmss.Name == nil {
		err = fmt.Errorf("could not find Azure VMSS %s", c.AzureVMSSName)
		return nil, err
	}

	// Get VMSS VM instances
	vms, err := c.Compute.ListVMScaleSetVMs(ctx, c.AzureResourceGroupName, c.AzureVMSSName)
	if err != nil {
		err = fmt.Errorf("could not list Azure VMSS VM instances: %w", err)
		return nil, err
	}

	out = &AutoScalingGroup{
		Name:            *vmss.Name,
		MinSize:         -1,
		MaxSize:         -1,
		DesiredCapacity: -1,
		Instances:       make([]Instance, 0, len(vms)),
	}

	// Azure VMSS uses SKU capacity instead of ASG-style min/max/desired capacity
	if vmss.SKU != nil && vmss.SKU.Capacity != nil {
		out.DesiredCapacity = int(*vmss.SKU.Capacity)
		// Azure VMSS doesn't have explicit min/max, so we set reasonable defaults
		// Min is typically 0, and we use a high max to not artificially limit scaling
		out.MinSize = 0
		out.MaxSize = int(*vmss.SKU.Capacity) * 2
	}

	for _, vm := range vms {
		if vm.InstanceID == nil {
			continue
		}

		// Azure uses ProvisioningState (Succeeded, Creating, Deleting, etc.)
		// instead of AWS lifecycle states (InService, Pending, Terminating, etc.)
		provisioningState := "Unknown"
		if vm.Properties != nil && vm.Properties.ProvisioningState != nil {
			provisioningState = *vm.Properties.ProvisioningState
		}

		out.Instances = append(out.Instances, Instance{
			ID:             *vm.InstanceID,
			LifecycleState: provisioningState,
		})
	}

	return out, nil
}

// KillInstance deletes a VM instance from the Azure VMSS.
//
// Unlike AWS ASG, Azure VMSS automatically adjusts capacity when an instance is deleted.
func (c *AzureController) KillInstance(ctx context.Context, instanceID string) (err error) {
	ctx, span := c.Tracer.Start(ctx, "azure.vmss.deleteVM")
	defer span.End()

	span.SetAttributes(attribute.String("instance_id", instanceID))

	err = c.Compute.DeleteVMScaleSetVM(ctx, c.AzureResourceGroupName, c.AzureVMSSName, instanceID)
	if err != nil {
		err = fmt.Errorf("could not delete Azure VMSS VM instance: %v", err)
		return err
	}

	return nil
}

// ScaleVMSS scales the Azure VMSS to the desired capacity.
// This is an Azure-friendly alias for ScaleUpASG that can scale both up and down.
func (c *AzureController) ScaleVMSS(ctx context.Context, desiredCapacity int) (err error) {
	return c.ScaleUpASG(ctx, desiredCapacity)
}

// ScaleUpASG scales the Azure VMSS to the desired capacity.
//
// Note: This method implements the ControllerInterface which uses AWS-centric naming (ScaleUpASG),
// but it scales the Azure VMSS by updating the SKU capacity. Despite the name "ScaleUp", it can
// scale both up and down depending on the desiredCapacity parameter.
// For Azure-specific code, consider using ScaleVMSS() instead for clearer semantics.
func (c *AzureController) ScaleUpASG(ctx context.Context, desiredCapacity int) (err error) {
	ctx, span := c.Tracer.Start(ctx, "azure.vmss.scale")
	defer span.End()

	span.SetAttributes(attribute.Int("desired_capacity", desiredCapacity))

	err = c.Compute.UpdateVMScaleSetCapacity(ctx, c.AzureResourceGroupName, c.AzureVMSSName, int64(desiredCapacity))
	if err != nil {
		err = fmt.Errorf("could not update Azure VMSS capacity: %v", err)
		return err
	}

	return nil
}
