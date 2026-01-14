package internal_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute/v6"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/shurcooL/graphql"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/spacelift-io/awsautoscalr/internal"
	"github.com/spacelift-io/awsautoscalr/internal/ifaces"
)

const (
	azureResourceGroupName = "test-rg"
	azureVMSSName          = "test-vmss"
)

type MockAzureCompute struct {
	mock.Mock
}

func (m *MockAzureCompute) GetVMScaleSet(ctx context.Context, resourceGroupName string, vmScaleSetName string) (*armcompute.VirtualMachineScaleSet, error) {
	args := m.Called(ctx, resourceGroupName, vmScaleSetName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*armcompute.VirtualMachineScaleSet), args.Error(1)
}

func (m *MockAzureCompute) ListVMScaleSetVMs(ctx context.Context, resourceGroupName string, vmScaleSetName string) ([]*armcompute.VirtualMachineScaleSetVM, error) {
	args := m.Called(ctx, resourceGroupName, vmScaleSetName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*armcompute.VirtualMachineScaleSetVM), args.Error(1)
}

func (m *MockAzureCompute) GetVMScaleSetVM(ctx context.Context, resourceGroupName string, vmScaleSetName string, instanceID string) (*armcompute.VirtualMachineScaleSetVM, error) {
	args := m.Called(ctx, resourceGroupName, vmScaleSetName, instanceID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*armcompute.VirtualMachineScaleSetVM), args.Error(1)
}

func (m *MockAzureCompute) UpdateVMScaleSetCapacity(ctx context.Context, resourceGroupName string, vmScaleSetName string, capacity int64) error {
	args := m.Called(ctx, resourceGroupName, vmScaleSetName, capacity)
	return args.Error(0)
}

func (m *MockAzureCompute) DeleteVMScaleSetVM(ctx context.Context, resourceGroupName string, vmScaleSetName string, instanceID string) error {
	args := m.Called(ctx, resourceGroupName, vmScaleSetName, instanceID)
	return args.Error(0)
}

type MockAzureKeyVault struct {
	mock.Mock
}

func (m *MockAzureKeyVault) GetSecret(ctx context.Context, secretName string) (azsecrets.GetSecretResponse, error) {
	args := m.Called(ctx, secretName)
	return args.Get(0).(azsecrets.GetSecretResponse), args.Error(1)
}

func setupAzureController() (*internal.AzureController, *MockAzureCompute, *MockAzureKeyVault, *ifaces.MockSpacelift) {
	mockCompute := &MockAzureCompute{}
	mockKeyVault := &MockAzureKeyVault{}
	mockSpacelift := &ifaces.MockSpacelift{}

	tp := trace.NewTracerProvider(
		trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(tracetest.NewNoopExporter())),
	)
	otel.SetTracerProvider(tp)

	controller := &internal.AzureController{
		Controller: internal.Controller{
			Spacelift:             mockSpacelift,
			SpaceliftWorkerPoolID: workerPoolID,
			Tracer:                tp.Tracer("unittest"),
		},
		Compute:                mockCompute,
		KeyVault:               mockKeyVault,
		AzureResourceGroupName: azureResourceGroupName,
		AzureVMSSName:          azureVMSSName,
	}

	return controller, mockCompute, mockKeyVault, mockSpacelift
}

func stringPtr(s string) *string {
	return &s
}

func int64Ptr(i int64) *int64 {
	return &i
}

func timePtr(t time.Time) *time.Time {
	return &t
}

// DescribeInstances tests - verifies retrieving VMSS VM instance details

func TestAzureDescribeInstances_APICallFails_SendsCorrectInput(t *testing.T) {
	instanceIDs := []string{"vm-1"}

	sut, mockCompute, _, _ := setupAzureController()

	var capturedInstanceID string
	mockCompute.On(
		"GetVMScaleSetVM",
		mock.Anything,
		azureResourceGroupName,
		azureVMSSName,
		mock.MatchedBy(func(id string) bool {
			capturedInstanceID = id
			return true
		}),
	).Return(nil, errors.New("bacon"))

	_, _ = sut.DescribeInstances(t.Context(), instanceIDs)

	require.Equal(t, instanceIDs[0], capturedInstanceID)
}

func TestAzureDescribeInstances_APICallFails_ReturnsError(t *testing.T) {
	instanceIDs := []string{"vm-1"}

	sut, mockCompute, _, _ := setupAzureController()

	mockCompute.On("GetVMScaleSetVM", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("bacon"))

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Empty(t, instances)
	require.EqualError(t, err, "could not describe VMSS VM instance vm-1: bacon")
}

func TestAzureDescribeInstances_InstanceHasNoID_ReturnsError(t *testing.T) {
	instanceIDs := []string{"vm-1"}

	sut, mockCompute, _, _ := setupAzureController()

	vm := &armcompute.VirtualMachineScaleSetVM{
		InstanceID: nil,
		Properties: &armcompute.VirtualMachineScaleSetVMProperties{
			TimeCreated: timePtr(time.Now()),
		},
	}

	mockCompute.On("GetVMScaleSetVM", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(vm, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Empty(t, instances)
	require.EqualError(t, err, "could not find VMSS VM instance ID")
}

func TestAzureDescribeInstances_InstanceHasNoLaunchTime_ReturnsError(t *testing.T) {
	instanceIDs := []string{"vm-1"}

	sut, mockCompute, _, _ := setupAzureController()

	vm := &armcompute.VirtualMachineScaleSetVM{
		InstanceID: &instanceIDs[0],
		Properties: nil,
	}

	mockCompute.On("GetVMScaleSetVM", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(vm, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Empty(t, instances)
	require.EqualError(t, err, "could not find creation time for VMSS VM instance vm-1")
}

func TestAzureDescribeInstances_ValidInstance_ReturnsInstance(t *testing.T) {
	instanceIDs := []string{"vm-1"}

	sut, mockCompute, _, _ := setupAzureController()

	vm := &armcompute.VirtualMachineScaleSetVM{
		InstanceID: &instanceIDs[0],
		Properties: &armcompute.VirtualMachineScaleSetVMProperties{
			TimeCreated: timePtr(time.Now()),
		},
	}

	mockCompute.On("GetVMScaleSetVM", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(vm, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.NoError(t, err)
	require.Len(t, instances, 1)
}

// GetAutoscalingGroup tests - verifies retrieving Azure VMSS details and capacity

func TestAzureGetAutoscalingGroup_APICallFails_SendsCorrectInput(t *testing.T) {
	sut, mockCompute, _, _ := setupAzureController()

	var capturedRG, capturedVMSS string
	mockCompute.On(
		"GetVMScaleSet",
		mock.Anything,
		mock.MatchedBy(func(rg string) bool {
			capturedRG = rg
			return true
		}),
		mock.MatchedBy(func(vmss string) bool {
			capturedVMSS = vmss
			return true
		}),
	).Return(nil, errors.New("bacon"))

	_, _ = sut.GetAutoscalingGroup(t.Context())

	require.Equal(t, azureResourceGroupName, capturedRG)
	require.Equal(t, azureVMSSName, capturedVMSS)
}

func TestAzureGetAutoscalingGroup_APICallFails_ReturnsError(t *testing.T) {
	sut, mockCompute, _, _ := setupAzureController()

	mockCompute.On("GetVMScaleSet", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("bacon"))

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.Nil(t, group)
	require.EqualError(t, err, "could not get Azure VMSS details: bacon")
}

func TestAzureGetAutoscalingGroup_VMSSHasNoName_ReturnsError(t *testing.T) {
	sut, mockCompute, _, _ := setupAzureController()

	vmss := &armcompute.VirtualMachineScaleSet{
		Name: nil,
	}

	mockCompute.On("GetVMScaleSet", mock.Anything, mock.Anything, mock.Anything).
		Return(vmss, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.Nil(t, group)
	require.EqualError(t, err, "could not find Azure VMSS test-vmss")
}

func TestAzureGetAutoscalingGroup_ListVMsFails_ReturnsError(t *testing.T) {
	sut, mockCompute, _, _ := setupAzureController()

	vmss := &armcompute.VirtualMachineScaleSet{
		Name: stringPtr(azureVMSSName),
		SKU: &armcompute.SKU{
			Capacity: int64Ptr(3),
		},
	}

	mockCompute.On("GetVMScaleSet", mock.Anything, mock.Anything, mock.Anything).
		Return(vmss, nil)

	mockCompute.On("ListVMScaleSetVMs", mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("bacon"))

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.Nil(t, group)
	require.EqualError(t, err, "could not list Azure VMSS VM instances: bacon")
}

func TestAzureGetAutoscalingGroup_Success_ReturnsGroup(t *testing.T) {
	sut, mockCompute, _, _ := setupAzureController()

	capacity := int64(3)
	vmss := &armcompute.VirtualMachineScaleSet{
		Name: stringPtr(azureVMSSName),
		SKU: &armcompute.SKU{
			Capacity: &capacity,
		},
	}

	vms := []*armcompute.VirtualMachineScaleSetVM{
		{
			InstanceID: stringPtr("vm-1"),
			Properties: &armcompute.VirtualMachineScaleSetVMProperties{
				ProvisioningState: stringPtr("Succeeded"),
			},
		},
		{
			InstanceID: stringPtr("vm-2"),
			Properties: &armcompute.VirtualMachineScaleSetVMProperties{
				ProvisioningState: stringPtr("Succeeded"),
			},
		},
	}

	mockCompute.On("GetVMScaleSet", mock.Anything, mock.Anything, mock.Anything).
		Return(vmss, nil)

	mockCompute.On("ListVMScaleSetVMs", mock.Anything, mock.Anything, mock.Anything).
		Return(vms, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.NoError(t, err)
	require.NotNil(t, group)
	require.Equal(t, azureVMSSName, group.Name)
	require.Equal(t, 3, group.DesiredCapacity)
	require.Equal(t, 0, group.MinSize)
	require.Equal(t, 6, group.MaxSize)
	require.Len(t, group.Instances, 2)
}

// GetWorkerPool tests - verifies Spacelift worker pool integration (cloud-agnostic)

func TestAzureGetWorkerPool_APICallFails_SendsCorrectInput(t *testing.T) {
	sut, _, _, mockSpacelift := setupAzureController()

	var capturedParams map[string]any
	mockSpacelift.On(
		"Query",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			capturedParams = in.(map[string]any)
			return true
		}),
		mock.Anything,
	).Return(errors.New("bacon"))

	_, _ = sut.GetWorkerPool(t.Context())

	require.NotNil(t, capturedParams)
	require.Equal(t, workerPoolID, capturedParams["workerPool"])
}

func TestAzureGetWorkerPool_APICallFails_ReturnsError(t *testing.T) {
	sut, _, _, mockSpacelift := setupAzureController()

	mockSpacelift.On("Query", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("bacon"))

	workerPool, err := sut.GetWorkerPool(t.Context())

	require.Nil(t, workerPool)
	require.EqualError(t, err, "could not get Spacelift worker pool details: bacon")
}

// DrainWorker tests - verifies Spacelift worker draining (cloud-agnostic)

func TestAzureDrainWorker_DrainCallFails_SendsCorrectInput(t *testing.T) {
	const workerID = "test-worker"

	sut, _, _, mockSpacelift := setupAzureController()

	var capturedParams map[string]any
	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			if params := in.(map[string]any); params["drain"].(graphql.Boolean) {
				capturedParams = params
				return true
			}
			return false
		}),
		mock.Anything,
	).Return(errors.New("bacon"))

	_, _ = sut.DrainWorker(t.Context(), workerID)

	require.NotNil(t, capturedParams)
	require.Equal(t, workerPoolID, capturedParams["workerPoolId"])
	require.Equal(t, workerID, capturedParams["workerId"])
	require.True(t, bool(capturedParams["drain"].(graphql.Boolean)))
}

func TestAzureDrainWorker_WorkerNotBusy_SucceedsAndReportsDrained(t *testing.T) {
	const workerID = "test-worker"

	sut, _, _, mockSpacelift := setupAzureController()

	mockSpacelift.On(
		"Mutate",
		mock.Anything,
		mock.Anything,
		mock.MatchedBy(func(in any) bool {
			params := in.(map[string]any)
			return bool(params["drain"].(graphql.Boolean))
		}),
		mock.Anything,
	).Run(func(args mock.Arguments) {
		args.Get(1).(*internal.WorkerDrainSet).Worker = internal.Worker{Busy: false}
	}).Return(nil)

	drained, err := sut.DrainWorker(t.Context(), workerID)

	require.True(t, drained)
	require.NoError(t, err)
}

// KillInstance tests - verifies deleting VMSS VM instances

func TestAzureKillInstance_DeleteCallFails_SendsCorrectInput(t *testing.T) {
	const instanceID = "test-instance"

	sut, mockCompute, _, _ := setupAzureController()

	var capturedRG, capturedVMSS, capturedInstanceID string
	mockCompute.On(
		"DeleteVMScaleSetVM",
		mock.Anything,
		mock.MatchedBy(func(rg string) bool {
			capturedRG = rg
			return true
		}),
		mock.MatchedBy(func(vmss string) bool {
			capturedVMSS = vmss
			return true
		}),
		mock.MatchedBy(func(id string) bool {
			capturedInstanceID = id
			return true
		}),
	).Return(errors.New("bacon"))

	_ = sut.KillInstance(t.Context(), instanceID)

	require.Equal(t, azureResourceGroupName, capturedRG)
	require.Equal(t, azureVMSSName, capturedVMSS)
	require.Equal(t, instanceID, capturedInstanceID)
}

func TestAzureKillInstance_DeleteCallFails_ReturnsError(t *testing.T) {
	const instanceID = "test-instance"

	sut, mockCompute, _, _ := setupAzureController()

	mockCompute.On("DeleteVMScaleSetVM", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("bacon"))

	err := sut.KillInstance(t.Context(), instanceID)

	require.EqualError(t, err, "could not delete Azure VMSS VM instance: bacon")
}

func TestAzureKillInstance_Success_NoError(t *testing.T) {
	const instanceID = "test-instance"

	sut, mockCompute, _, _ := setupAzureController()

	mockCompute.On("DeleteVMScaleSetVM", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	err := sut.KillInstance(t.Context(), instanceID)

	require.NoError(t, err)
}

// ScaleUpASG tests - verifies scaling VMSS capacity (up or down)

func TestAzureScaleUpASG_UpdateCapacityCallFails_SendsCorrectInput(t *testing.T) {
	const desiredCapacity = 42

	sut, mockCompute, _, _ := setupAzureController()

	var capturedRG, capturedVMSS string
	var capturedCapacity int64
	mockCompute.On(
		"UpdateVMScaleSetCapacity",
		mock.Anything,
		mock.MatchedBy(func(rg string) bool {
			capturedRG = rg
			return true
		}),
		mock.MatchedBy(func(vmss string) bool {
			capturedVMSS = vmss
			return true
		}),
		mock.MatchedBy(func(capacity int64) bool {
			capturedCapacity = capacity
			return true
		}),
	).Return(errors.New("bacon"))

	_ = sut.ScaleUpASG(t.Context(), desiredCapacity)

	require.Equal(t, azureResourceGroupName, capturedRG)
	require.Equal(t, azureVMSSName, capturedVMSS)
	require.EqualValues(t, desiredCapacity, capturedCapacity)
}

func TestAzureScaleUpASG_UpdateCapacityCallFails_ReturnsError(t *testing.T) {
	const desiredCapacity = 42

	sut, mockCompute, _, _ := setupAzureController()

	mockCompute.On("UpdateVMScaleSetCapacity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("bacon"))

	err := sut.ScaleUpASG(t.Context(), desiredCapacity)

	require.EqualError(t, err, "could not update Azure VMSS capacity: bacon")
}

func TestAzureScaleUpASG_Success_NoError(t *testing.T) {
	const desiredCapacity = 42

	sut, mockCompute, _, _ := setupAzureController()

	mockCompute.On("UpdateVMScaleSetCapacity", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil)

	err := sut.ScaleUpASG(t.Context(), desiredCapacity)

	require.NoError(t, err)
}
