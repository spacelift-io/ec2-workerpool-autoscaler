package internal_test

import (
	"errors"
	"testing"
	"time"

	"cloud.google.com/go/compute/apiv1/computepb"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/spacelift-io/awsautoscalr/internal"
	"github.com/spacelift-io/awsautoscalr/internal/ifaces"
)

// Test constants for GCP controller tests
const (
	gcpWorkerPoolID = "test-worker-pool-01HV8"
	gcpProject      = "my-project"
	gcpZone         = "us-central1-a"
	gcpRegion       = "us-central1"
	gcpIGMName      = "my-mig"
	gcpIGMSelfLink  = "projects/my-project/zones/us-central1-a/instanceGroupManagers/my-mig"
)

// Helper function to create a pointer to a value
func ptr[T any](v T) *T {
	return &v
}

// Helper function to create an instance URL
func makeGCPInstanceURL(project, zone, name string) string {
	return "projects/" + project + "/zones/" + zone + "/instances/" + name
}

// setupGCPController creates a GCPController with mock dependencies for testing.
func setupGCPController(t *testing.T, isRegional bool) (*internal.GCPController, *ifaces.MockGCPCompute, *ifaces.MockGCPInstances, *ifaces.MockSpacelift) {
	mockCompute := ifaces.NewMockGCPCompute(t)
	mockInstances := ifaces.NewMockGCPInstances(t)
	mockSpacelift := &ifaces.MockSpacelift{}

	tp := trace.NewTracerProvider(
		trace.WithSpanProcessor(trace.NewSimpleSpanProcessor(tracetest.NewNoopExporter())),
	)
	otel.SetTracerProvider(tp)

	location := gcpZone
	if isRegional {
		location = gcpRegion
	}

	controller := &internal.GCPController{
		Controller: internal.Controller{
			Spacelift:             mockSpacelift,
			SpaceliftWorkerPoolID: gcpWorkerPoolID,
			Tracer:                tp.Tracer("unittest"),
		},
		Compute:    mockCompute,
		Instances:  mockInstances,
		Project:    gcpProject,
		Location:   location,
		IGMName:    gcpIGMName,
		IGMSelfLink: gcpIGMSelfLink,
		IsRegional: isRegional,
		MinSize:    0,
		MaxSize:    10,
	}

	return controller, mockCompute, mockInstances, mockSpacelift
}

// setupGCPZonalController creates a GCPController configured for a zonal IGM
func setupGCPZonalController(t *testing.T) (*internal.GCPController, *ifaces.MockGCPCompute, *ifaces.MockGCPInstances, *ifaces.MockSpacelift) {
	return setupGCPController(t, false)
}

// DescribeInstances tests

func TestGCPDescribeInstances_APICallFails_ReturnsError(t *testing.T) {
	instanceID := makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")
	instanceIDs := []string{instanceID}

	sut, _, mockInstances, _ := setupGCPZonalController(t)

	mockInstances.On("GetInstance", mock.Anything, gcpProject, gcpZone, "instance-1").
		Return(nil, errors.New("API error"))

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Empty(t, instances)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not describe instance")
}

func TestGCPDescribeInstances_MissingCreationTimestamp_ReturnsError(t *testing.T) {
	instanceID := makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")
	instanceIDs := []string{instanceID}

	sut, _, mockInstances, _ := setupGCPZonalController(t)

	instance := &computepb.Instance{
		Name:              ptr("instance-1"),
		CreationTimestamp: nil,
	}

	mockInstances.On("GetInstance", mock.Anything, gcpProject, gcpZone, "instance-1").
		Return(instance, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Empty(t, instances)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not find creation time")
}

func TestGCPDescribeInstances_ValidInstance_ReturnsInstance(t *testing.T) {
	instanceID := makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")
	instanceIDs := []string{instanceID}

	sut, _, mockInstances, _ := setupGCPZonalController(t)

	timestamp := time.Now().Format(time.RFC3339)
	instance := &computepb.Instance{
		Name:              ptr("instance-1"),
		CreationTimestamp: ptr(timestamp),
	}

	mockInstances.On("GetInstance", mock.Anything, gcpProject, gcpZone, "instance-1").
		Return(instance, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.NoError(t, err)
	require.Len(t, instances, 1)
	require.Equal(t, instanceID, instances[0].ID)
}

func TestGCPDescribeInstances_MultipleInstances_ReturnsAllInstances(t *testing.T) {
	instanceID1 := makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")
	instanceID2 := makeGCPInstanceURL(gcpProject, gcpZone, "instance-2")
	instanceIDs := []string{instanceID1, instanceID2}

	sut, _, mockInstances, _ := setupGCPZonalController(t)

	timestamp := time.Now().Format(time.RFC3339)

	mockInstances.On("GetInstance", mock.Anything, gcpProject, gcpZone, "instance-1").
		Return(&computepb.Instance{
			Name:              ptr("instance-1"),
			CreationTimestamp: ptr(timestamp),
		}, nil)

	mockInstances.On("GetInstance", mock.Anything, gcpProject, gcpZone, "instance-2").
		Return(&computepb.Instance{
			Name:              ptr("instance-2"),
			CreationTimestamp: ptr(timestamp),
		}, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.NoError(t, err)
	require.Len(t, instances, 2)
}

func TestGCPDescribeInstances_InvalidInstanceURL_ReturnsError(t *testing.T) {
	sut, _, _, _ := setupGCPZonalController(t)

	invalidInstanceID := "not-a-valid-instance-url"
	instanceIDs := []string{invalidInstanceID}

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Nil(t, instances)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not parse instance ID")
}

func TestGCPDescribeInstances_InvalidTimestamp_ReturnsError(t *testing.T) {
	instanceID := makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")
	instanceIDs := []string{instanceID}

	sut, _, mockInstances, _ := setupGCPZonalController(t)

	instance := &computepb.Instance{
		Name:              ptr("instance-1"),
		CreationTimestamp: ptr("not-a-valid-timestamp"),
	}

	mockInstances.On("GetInstance", mock.Anything, gcpProject, gcpZone, "instance-1").
		Return(instance, nil)

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.Nil(t, instances)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not parse creation timestamp")
}

func TestGCPDescribeInstances_EmptyList_ReturnsEmptySlice(t *testing.T) {
	sut, _, _, _ := setupGCPZonalController(t)

	instanceIDs := []string{}

	instances, err := sut.DescribeInstances(t.Context(), instanceIDs)

	require.NoError(t, err)
	require.Empty(t, instances)
}

// GetAutoscalingGroup tests

func TestGCPGetAutoscalingGroup_APICallFails_ReturnsError(t *testing.T) {
	sut, mockCompute, _, _ := setupGCPZonalController(t)

	mockCompute.On("GetInstanceGroupManager", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(nil, errors.New("API error"))

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.Nil(t, group)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not get GCP IGM details")
}

func TestGCPGetAutoscalingGroup_IGMHasNoName_ReturnsError(t *testing.T) {
	sut, mockCompute, _, _ := setupGCPZonalController(t)

	igm := &computepb.InstanceGroupManager{
		Name: nil,
	}

	mockCompute.On("GetInstanceGroupManager", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(igm, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.Nil(t, group)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not find GCP IGM")
}

func TestGCPGetAutoscalingGroup_ListInstancesFails_ReturnsError(t *testing.T) {
	sut, mockCompute, _, _ := setupGCPZonalController(t)

	igm := &computepb.InstanceGroupManager{
		Name:       ptr(gcpIGMName),
		TargetSize: ptr(int32(3)),
	}

	mockCompute.On("GetInstanceGroupManager", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(igm, nil)

	mockCompute.On("ListManagedInstances", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(nil, errors.New("list error"))

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.Nil(t, group)
	require.Error(t, err)
	require.Contains(t, err.Error(), "could not list GCP IGM instances")
}

func TestGCPGetAutoscalingGroup_Success_ReturnsGroup(t *testing.T) {
	sut, mockCompute, _, _ := setupGCPZonalController(t)

	igm := &computepb.InstanceGroupManager{
		Name:       ptr(gcpIGMName),
		TargetSize: ptr(int32(3)),
	}

	// Use full URLs to verify URL prefix stripping
	instances := []*computepb.ManagedInstance{
		{
			Instance:      ptr("https://www.googleapis.com/compute/v1/" + makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")),
			CurrentAction: ptr("NONE"),
		},
		{
			Instance:      ptr("https://compute.googleapis.com/compute/v1/" + makeGCPInstanceURL(gcpProject, gcpZone, "instance-2")),
			CurrentAction: ptr("NONE"),
		},
	}

	mockCompute.On("GetInstanceGroupManager", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(igm, nil)

	mockCompute.On("ListManagedInstances", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(instances, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.NoError(t, err)
	require.NotNil(t, group)
	require.Equal(t, gcpIGMSelfLink, group.Name)
	require.Equal(t, 3, group.DesiredCapacity)
	require.Equal(t, 0, group.MinSize)
	require.Equal(t, 10, group.MaxSize)
	require.Len(t, group.Instances, 2)
	require.Equal(t, makeGCPInstanceURL(gcpProject, gcpZone, "instance-1"), group.Instances[0].ID)
	require.Equal(t, makeGCPInstanceURL(gcpProject, gcpZone, "instance-2"), group.Instances[1].ID)
}

func TestGCPGetAutoscalingGroup_NoTargetSize_SetsNegativeDesiredCapacity(t *testing.T) {
	sut, mockCompute, _, _ := setupGCPZonalController(t)

	igm := &computepb.InstanceGroupManager{
		Name:       ptr(gcpIGMName),
		TargetSize: nil,
	}

	mockCompute.On("GetInstanceGroupManager", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(igm, nil)

	mockCompute.On("ListManagedInstances", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return([]*computepb.ManagedInstance{}, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.NoError(t, err)
	require.NotNil(t, group)
	require.Equal(t, -1, group.DesiredCapacity, "DesiredCapacity should be -1 when TargetSize is nil")
}

func TestGCPGetAutoscalingGroup_InstanceWithNilPointer_Skipped(t *testing.T) {
	sut, mockCompute, _, _ := setupGCPZonalController(t)

	igm := &computepb.InstanceGroupManager{
		Name:       ptr(gcpIGMName),
		TargetSize: ptr(int32(3)),
	}

	instances := []*computepb.ManagedInstance{
		{
			Instance:      ptr(makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")),
			CurrentAction: ptr("NONE"),
		},
		{
			Instance:      nil,
			CurrentAction: ptr("CREATING"),
		},
		{
			Instance:      ptr(makeGCPInstanceURL(gcpProject, gcpZone, "instance-3")),
			CurrentAction: ptr("RUNNING"),
		},
	}

	mockCompute.On("GetInstanceGroupManager", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(igm, nil)

	mockCompute.On("ListManagedInstances", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(instances, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.NoError(t, err)
	require.NotNil(t, group)
	require.Len(t, group.Instances, 2)
	require.Equal(t, makeGCPInstanceURL(gcpProject, gcpZone, "instance-1"), group.Instances[0].ID)
	require.Equal(t, makeGCPInstanceURL(gcpProject, gcpZone, "instance-3"), group.Instances[1].ID)
}

func TestGCPGetAutoscalingGroup_InstanceWithNilCurrentAction_ShowsUnknown(t *testing.T) {
	sut, mockCompute, _, _ := setupGCPZonalController(t)

	igm := &computepb.InstanceGroupManager{
		Name:       ptr(gcpIGMName),
		TargetSize: ptr(int32(1)),
	}

	instances := []*computepb.ManagedInstance{
		{
			Instance:      ptr(makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")),
			CurrentAction: nil,
		},
	}

	mockCompute.On("GetInstanceGroupManager", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(igm, nil)

	mockCompute.On("ListManagedInstances", mock.Anything, gcpProject, gcpZone, gcpIGMName).
		Return(instances, nil)

	group, err := sut.GetAutoscalingGroup(t.Context())

	require.NoError(t, err)
	require.NotNil(t, group)
	require.Len(t, group.Instances, 1)
	require.Equal(t, "Unknown", group.Instances[0].LifecycleState, "Should show 'Unknown' when CurrentAction is nil")
}

// KillInstance tests

func TestGCPKillInstance_DeleteFails_ReturnsError(t *testing.T) {
	instanceID := makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")

	sut, mockCompute, _, _ := setupGCPZonalController(t)

	mockCompute.On("DeleteInstance", mock.Anything, gcpProject, gcpZone, gcpIGMName, instanceID).
		Return(errors.New("delete error"))

	err := sut.KillInstance(t.Context(), instanceID)

	require.Error(t, err)
	require.Contains(t, err.Error(), "could not delete GCP IGM instance")
}

func TestGCPKillInstance_Success_NoError(t *testing.T) {
	instanceID := makeGCPInstanceURL(gcpProject, gcpZone, "instance-1")

	sut, mockCompute, _, _ := setupGCPZonalController(t)

	mockCompute.On("DeleteInstance", mock.Anything, gcpProject, gcpZone, gcpIGMName, instanceID).
		Return(nil)

	err := sut.KillInstance(t.Context(), instanceID)

	require.NoError(t, err)
}

// ScaleUpASG tests

func TestGCPScaleUpASG_ResizeFails_ReturnsError(t *testing.T) {
	sut, mockCompute, _, _ := setupGCPZonalController(t)

	mockCompute.On("ResizeIGM", mock.Anything, gcpProject, gcpZone, gcpIGMName, int64(5)).
		Return(errors.New("resize error"))

	err := sut.ScaleUpASG(t.Context(), 5)

	require.Error(t, err)
	require.Contains(t, err.Error(), "could not resize GCP IGM")
}

func TestGCPScaleUpASG_Success_NoError(t *testing.T) {
	sut, mockCompute, _, _ := setupGCPZonalController(t)

	mockCompute.On("ResizeIGM", mock.Anything, gcpProject, gcpZone, gcpIGMName, int64(5)).
		Return(nil)

	err := sut.ScaleUpASG(t.Context(), 5)

	require.NoError(t, err)
}


