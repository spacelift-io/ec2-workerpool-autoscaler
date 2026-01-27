package internal

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/monitor/armmonitor"
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
	AzureMinSize           int
	AzureMaxSize           int
}

// azureComputeClient wraps the Azure Compute SDK client to implement the AzureCompute interface.
type azureComputeClient struct {
	vmssClient               *armcompute.VirtualMachineScaleSetsClient
	vmssVirtualMachineClient *armcompute.VirtualMachineScaleSetVMsClient
}

func (c *azureComputeClient) GetVMScaleSet(ctx context.Context, resourceGroupName string, vmScaleSetName string) (*armcompute.VirtualMachineScaleSet, error) {
	resp, err := c.vmssClient.Get(ctx, resourceGroupName, vmScaleSetName, nil)
	if err != nil {
		return nil, err
	}
	return &resp.VirtualMachineScaleSet, nil
}

func (c *azureComputeClient) ListVMScaleSetVMs(ctx context.Context, resourceGroupName string, vmScaleSetName string) ([]*armcompute.VirtualMachineScaleSetVM, error) {
	pager := c.vmssVirtualMachineClient.NewListPager(resourceGroupName, vmScaleSetName, nil)
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
	resp, err := c.vmssVirtualMachineClient.Get(ctx, resourceGroupName, vmScaleSetName, instanceID, nil)
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
	poller, err := c.vmssVirtualMachineClient.BeginDelete(ctx, resourceGroupName, vmScaleSetName, instanceID, nil)
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

// checkForAutoscaleSettings checks if the VMSS has any Azure autoscale settings configured.
// Returns an error if autoscale is enabled, as this would conflict with manual scaling.
func checkForAutoscaleSettings(ctx context.Context, subscriptionID, resourceGroupName, vmssResourceID string, cred *azidentity.DefaultAzureCredential) error {
	autoscaleClient, err := armmonitor.NewAutoscaleSettingsClient(subscriptionID, cred, nil)
	if err != nil {
		return fmt.Errorf("could not create Azure Monitor autoscale client: %w", err)
	}

	// List autoscale settings in the resource group
	pager := autoscaleClient.NewListByResourceGroupPager(resourceGroupName, nil)

	var enabledAutoscaleSettings []string

	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return fmt.Errorf("could not list autoscale settings: %w", err)
		}

		for _, setting := range page.Value {
			// Check if this autoscale setting targets our VMSS
			if setting.Properties != nil && setting.Properties.TargetResourceURI != nil {
				targetURI := *setting.Properties.TargetResourceURI

				// Compare the target resource URI with our VMSS resource ID
				if strings.EqualFold(targetURI, vmssResourceID) {
					// Check if the autoscale setting is enabled
					if setting.Properties.Enabled != nil && *setting.Properties.Enabled {
						settingName := "unknown"
						if setting.Name != nil {
							settingName = *setting.Name
						}
						enabledAutoscaleSettings = append(enabledAutoscaleSettings, settingName)
					}
				}
			}
		}
	}

	if len(enabledAutoscaleSettings) > 0 {
		return fmt.Errorf("VMSS has Azure autoscaling enabled (settings: %s). This conflicts with manual scaling. "+
			"Please disable Azure autoscaling for this VMSS or use a different VMSS for the autoscaler",
			strings.Join(enabledAutoscaleSettings, ", "))
	}

	return nil
}

// NewAzureController creates a new Azure controller instance.
func NewAzureController(ctx context.Context, cfg *RuntimeConfig) (ControllerInterface, error) {
	// Create Azure credential
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("could not create Azure credential: %w", err)
	}

	// Parse the VMSS resource ID to extract resource group and VMSS name
	// Expected format: /subscriptions/{subscriptionId}/resourceGroups/{resourceGroupName}/providers/Microsoft.Compute/virtualMachineScaleSets/{vmssName}
	resourceID, err := arm.ParseResourceID(cfg.AzureVMSSResourceID)
	if err != nil {
		return nil, fmt.Errorf("could not parse Azure VMSS resource ID: %w", err)
	}

	subscriptionID := resourceID.SubscriptionID
	resourceGroupName := resourceID.ResourceGroupName
	vmssName := resourceID.Name

	if subscriptionID == "" || resourceGroupName == "" || vmssName == "" {
		return nil, fmt.Errorf("could not parse Azure VMSS resource ID: missing required components (subscription: %q, resourceGroup: %q, vmss: %q)",
			subscriptionID, resourceGroupName, vmssName)
	}

	// Check for Azure autoscale settings that would conflict with manual scaling
	// We use the full resource ID for comparison with autoscale target URIs
	vmssResourceID := cfg.AzureVMSSResourceID
	if err := checkForAutoscaleSettings(ctx, subscriptionID, resourceGroupName, vmssResourceID, cred); err != nil {
		return nil, err
	}

	// Create Azure Compute clients
	vmssClient, err := armcompute.NewVirtualMachineScaleSetsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create Azure VMSS client: %w", err)
	}

	vmssVirtualMachineClient, err := armcompute.NewVirtualMachineScaleSetVMsClient(subscriptionID, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("could not create Azure VMSS VM client: %w", err)
	}

	computeClient := &azureComputeClient{
		vmssClient:               vmssClient,
		vmssVirtualMachineClient: vmssVirtualMachineClient,
	}

	// Create Azure Key Vault client using dedicated config fields
	// Note: AzureKeyVaultName and AzureSecretName are validated at parse time via azEnv tags
	vaultURL := fmt.Sprintf("https://%s.vault.azure.net", cfg.AzureKeyVaultName)
	secretName := cfg.AzureSecretName

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

	// Validate that AutoscalingMaxSize is positive (notEmpty rejects 0, this rejects negative)
	if cfg.AutoscalingMaxSize <= 0 {
		return nil, fmt.Errorf("AUTOSCALING_MAX_SIZE environment variable is required and must be greater than 0")
	}

	// Validate that max size is at least equal to min size
	if cfg.AutoscalingMaxSize < cfg.AutoscalingMinSize {
		return nil, fmt.Errorf("AUTOSCALING_MAX_SIZE (%d) must be greater than or equal to AUTOSCALING_MIN_SIZE (%d)",
			cfg.AutoscalingMaxSize, cfg.AutoscalingMinSize)
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
		AzureMinSize:           cfg.AutoscalingMinSize,
		AzureMaxSize:           cfg.AutoscalingMaxSize,
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
	skuCapacity := 2
	if vmss.SKU != nil && vmss.SKU.Capacity != nil {
		out.DesiredCapacity = int(*vmss.SKU.Capacity)
		skuCapacity = int(*vmss.SKU.Capacity)
	} else {
		out.DesiredCapacity = skuCapacity
	}

	// Use configured min/max from environment variables
	// Min size defaults to 0, max size is required and validated during controller initialization
	out.MinSize = c.AzureMinSize
	out.MaxSize = c.AzureMaxSize

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
