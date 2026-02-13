package internal

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	compute "cloud.google.com/go/compute/apiv1"
	"cloud.google.com/go/compute/apiv1/computepb"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	"cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"github.com/spacelift-io/awsautoscalr/internal/ifaces"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/api/iterator"
)

// gcpIGMSelfLinkRegex matches GCP IGM self-links in both zonal and regional formats.
// Formats:
//
//	Zonal: projects/{project}/zones/{zone}/instanceGroupManagers/{name}
//	Regional: projects/{project}/regions/{region}/instanceGroupManagers/{name}
var gcpIGMSelfLinkRegex = regexp.MustCompile(`^projects/([^/]+)/(zones|regions)/([^/]+)/instanceGroupManagers/([^/]+)$`)

// gcpInstanceURLPrefixes are the URL prefixes that may be present in instance URLs
// returned by the GCP API. These are stripped before storing instance IDs.
var gcpInstanceURLPrefixes = []string{
	"https://www.googleapis.com/compute/v1/",
	"https://compute.googleapis.com/compute/v1/",
}

// gcpInstanceURLRegex matches GCP instance resource paths.
// The format is: projects/{project}/zones/{zone}/instances/{name}
//
// Note: Full URLs are stripped to resource paths in GetAutoscalingGroup before storage.
var gcpInstanceURLRegex = regexp.MustCompile(`^projects/([^/]+)/zones/([^/]+)/instances/([^/]+)$`)

// GCPController manages autoscaling operations for GCP Instance Group Managers.
type GCPController struct {
	Controller

	// Clients.
	Compute   ifaces.GCPCompute   // IGM operations (zonal or regional)
	Instances ifaces.GCPInstances // Instance operations (always zonal)

	// Configuration.
	Project     string
	Location    string // Zone for zonal IGMs, region for regional IGMs
	IGMName     string
	IGMSelfLink string // Full resource path (e.g., projects/{project}/zones/{zone}/instanceGroupManagers/{name})
	IsRegional  bool
	MinSize     uint
	MaxSize     uint
}

// igmID holds parsed components of an IGM self-link.
type igmID struct {
	Project    string
	Location   string // Zone or Region
	Name       string
	IsRegional bool
}

// instanceURL holds parsed components of an instance URL.
type instanceURL struct {
	Project string
	Zone    string
	Name    string
}

// gcpZonalComputeClient wraps the GCP Compute SDK client for zonal IGM operations.
// Instance operations are handled separately by gcpInstancesClient.
type gcpZonalComputeClient struct {
	igmClient *compute.InstanceGroupManagersClient
}

// gcpRegionalComputeClient wraps the GCP Compute SDK client for regional IGM operations.
// Instance operations are handled separately by gcpInstancesClient.
type gcpRegionalComputeClient struct {
	igmClient *compute.RegionInstanceGroupManagersClient
}

// gcpInstancesClient wraps the GCP Compute Instances SDK client.
// This is separate from the IGM clients because instances are always zonal,
// regardless of whether the IGM is zonal or regional.
type gcpInstancesClient struct {
	client *compute.InstancesClient
}

// NewGCPController creates a new GCP controller instance.
func NewGCPController(ctx context.Context, cfg *RuntimeConfig) (ControllerInterface, error) {
	// Parse the IGM self-link
	parsedIGM, err := parseGCPIGMSelfLink(cfg.GCPIGMSelfLink)
	if err != nil {
		return nil, fmt.Errorf("could not parse GCP IGM self-link: %w", err)
	}

	// Validate configuration before creating any clients
	if cfg.AutoscalingMaxSize < cfg.AutoscalingMinSize {
		return nil, fmt.Errorf("AUTOSCALING_MAX_SIZE (%d) must be greater than or equal to AUTOSCALING_MIN_SIZE (%d)",
			cfg.AutoscalingMaxSize, cfg.AutoscalingMinSize)
	}

	ctrl := &GCPController{
		Project:     parsedIGM.Project,
		Location:    parsedIGM.Location,
		IGMName:     parsedIGM.Name,
		IGMSelfLink: cfg.GCPIGMSelfLink,
		IsRegional:  parsedIGM.IsRegional,
		MinSize:     cfg.AutoscalingMinSize,
		MaxSize:     cfg.AutoscalingMaxSize,
	}

	// Fetch Spacelift API key from Secret Manager (client is only needed during initialization)
	smClient, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not create GCP Secret Manager client: %w", err)
	}
	defer smClient.Close()

	secret, err := smClient.AccessSecretVersion(ctx, &secretmanagerpb.AccessSecretVersionRequest{
		Name: cfg.SpaceliftAPISecretName,
	})
	if err != nil {
		return nil, errors.Join(fmt.Errorf("could not get Spacelift API key secret from Secret Manager: %w", err), ctrl.Close())
	}

	if secret.Payload == nil || secret.Payload.Data == nil {
		return nil, errors.Join(errors.New("could not find Spacelift API key secret value in Secret Manager"), ctrl.Close())
	}

	spaceliftClient, err := newSpaceliftClient(ctx, cfg.SpaceliftAPIEndpoint, cfg.SpaceliftAPIKeyID, string(secret.Payload.Data))
	if err != nil {
		return nil, errors.Join(err, ctrl.Close())
	}

	ctrl.Controller = Controller{
		Spacelift:             spaceliftClient,
		SpaceliftWorkerPoolID: cfg.SpaceliftWorkerPoolID,
		Tracer:                otel.Tracer("github.com/spacelift-io/awsautoscalr/internal/controller"),
	}

	// Create Instances client (always zonal, regardless of IGM type)
	instancesSDKClient, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("could not create GCP Instances client: %w", err), ctrl.Close())
	}
	ctrl.Instances = &gcpInstancesClient{client: instancesSDKClient}

	// Create IGM client (zonal or regional based on IGM type)
	if parsedIGM.IsRegional {
		regionIGMClient, err := compute.NewRegionInstanceGroupManagersRESTClient(ctx)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("could not create GCP Regional Instance Group Managers client: %w", err), ctrl.Close())
		}
		ctrl.Compute = &gcpRegionalComputeClient{
			igmClient: regionIGMClient,
		}
	} else {
		zonalIGMClient, err := compute.NewInstanceGroupManagersRESTClient(ctx)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("could not create GCP Instance Group Managers client: %w", err), ctrl.Close())
		}
		ctrl.Compute = &gcpZonalComputeClient{
			igmClient: zonalIGMClient,
		}
	}

	return ctrl, nil
}

// DescribeInstances returns the details of the given instances from GCP Compute Engine.
func (c *GCPController) DescribeInstances(ctx context.Context, instanceIDs []string) (instances []Instance, err error) {
	ctx, span := c.Tracer.Start(ctx, "gcp.igm.describeInstances")
	defer span.End()

	for _, instanceID := range instanceIDs {
		// Parse the instance URL to get project, zone, and name
		instanceInfo, err := parseGCPInstanceURL(instanceID)
		if err != nil {
			return nil, fmt.Errorf("could not parse instance ID %s: %w", instanceID, err)
		}

		instance, err := c.Instances.GetInstance(ctx, instanceInfo.Project, instanceInfo.Zone, instanceInfo.Name)
		if err != nil {
			return nil, fmt.Errorf("could not describe instance %s: %w", instanceID, err)
		}

		if instance.CreationTimestamp == nil {
			return nil, fmt.Errorf("could not find creation time for instance %s", instanceID)
		}

		// Parse the creation timestamp
		// GCP returns RFC3339 format: 2021-01-01T00:00:00.000-07:00
		creationTime, err := time.Parse(time.RFC3339, *instance.CreationTimestamp)
		if err != nil {
			return nil, fmt.Errorf("could not parse creation timestamp for instance %s: %w", instanceID, err)
		}

		instances = append(instances, Instance{
			ID:         instanceID,
			LaunchTime: creationTime,
		})
	}

	return instances, nil
}

// GetAutoscalingGroup returns the GCP Instance Group Manager (IGM) details.
//
// Note: This method implements the ControllerInterface, which uses AWS-centric naming
// (AutoScalingGroup), but it returns GCP IGM details for consistency with the interface.
func (c *GCPController) GetAutoscalingGroup(ctx context.Context) (out *AutoScalingGroup, err error) {
	ctx, span := c.Tracer.Start(ctx, "gcp.igm.get")
	defer span.End()

	igm, err := c.Compute.GetInstanceGroupManager(ctx, c.Project, c.Location, c.IGMName)
	if err != nil {
		return nil, fmt.Errorf("could not get GCP IGM details: %w", err)
	}

	if igm.Name == nil {
		return nil, fmt.Errorf("could not find GCP IGM %s", c.IGMName)
	}

	// Get IGM instances
	managedInstances, err := c.Compute.ListManagedInstances(ctx, c.Project, c.Location, c.IGMName)
	if err != nil {
		return nil, fmt.Errorf("could not list GCP IGM instances: %w", err)
	}

	out = &AutoScalingGroup{
		Name:            c.IGMSelfLink,
		MinSize:         int(c.MinSize),
		MaxSize:         int(c.MaxSize),
		DesiredCapacity: -1,
		Instances:       make([]Instance, 0, len(managedInstances)),
	}

	// GCP IGM uses TargetSize for desired capacity
	if igm.TargetSize != nil {
		out.DesiredCapacity = int(*igm.TargetSize)
	}

	for _, mi := range managedInstances {
		if mi.Instance == nil {
			continue
		}

		// GCP uses CurrentAction (NONE, CREATING, DELETING, etc.)
		// Map to standard LifecycleState constants for state.go compatibility
		currentAction := "Unknown"
		if mi.CurrentAction != nil {
			currentAction = *mi.CurrentAction
		}
		lifecycleState := mapGCPCurrentActionToLifecycleState(currentAction)

		// Strip URL prefix from instance URL, storing only the resource path.
		// This simplifies instance ID handling throughout the codebase.
		instanceID := stripGCPInstanceURLPrefix(*mi.Instance)

		out.Instances = append(out.Instances, Instance{
			ID:             instanceID,
			LifecycleState: lifecycleState,
		})
	}

	return out, nil
}

// KillInstance deletes an instance from the GCP IGM.
//
// Unlike AWS ASG, GCP IGM automatically adjusts capacity when an instance is deleted.
func (c *GCPController) KillInstance(ctx context.Context, instanceID string) (err error) {
	ctx, span := c.Tracer.Start(ctx, "gcp.igm.deleteInstance")
	defer span.End()

	span.SetAttributes(attribute.String("instance_id", instanceID))

	err = c.Compute.DeleteInstance(ctx, c.Project, c.Location, c.IGMName, instanceID)
	if err != nil {
		return fmt.Errorf("could not delete GCP IGM instance: %w", err)
	}

	return nil
}

// ScaleUpASG scales the GCP IGM to the desired capacity.
//
// Note: This method implements the ControllerInterface which uses AWS-centric naming (ScaleUpASG),
// but it scales the GCP IGM by updating the target size. Despite the name "ScaleUp", it can
// scale both up and down depending on the desiredCapacity parameter.
func (c *GCPController) ScaleUpASG(ctx context.Context, desiredCapacity int) (err error) {
	ctx, span := c.Tracer.Start(ctx, "gcp.igm.resize")
	defer span.End()

	span.SetAttributes(attribute.Int("desired_capacity", desiredCapacity))

	err = c.Compute.ResizeIGM(ctx, c.Project, c.Location, c.IGMName, int64(desiredCapacity))
	if err != nil {
		return fmt.Errorf("could not resize GCP IGM: %w", err)
	}

	return nil
}

// InstanceIdentity extracts the group ID and instance ID from worker metadata using GCP-specific keys.
// GCP workers use "gcp_igm_self_link" for the IGM self-link and "gcp_instance_self_link" for the instance self-link.
func (c *GCPController) InstanceIdentity(worker *Worker) (GroupID, InstanceID, error) {
	groupID, groupErr := worker.MetadataValue("gcp_igm_self_link")
	instanceID, instanceErr := worker.MetadataValue("gcp_instance_self_link")
	return GroupID(groupID), InstanceID(instanceID), errors.Join(groupErr, instanceErr)
}

// Close releases all client resources associated with the GCPController.
func (c *GCPController) Close() error {
	var computeErr, instancesErr error
	if c.Compute != nil {
		computeErr = c.Compute.Close()
	}
	if c.Instances != nil {
		instancesErr = c.Instances.Close()
	}
	return errors.Join(computeErr, instancesErr)
}

// gcpInstancesClient methods

func (c *gcpInstancesClient) GetInstance(ctx context.Context, project, zone, instanceName string) (*computepb.Instance, error) {
	req := &computepb.GetInstanceRequest{
		Project:  project,
		Zone:     zone,
		Instance: instanceName,
	}
	return c.client.Get(ctx, req)
}

// Close releases the underlying client resources.
func (c *gcpInstancesClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// gcpZonalComputeClient methods

func (c *gcpZonalComputeClient) GetInstanceGroupManager(ctx context.Context, project, zone, name string) (*computepb.InstanceGroupManager, error) {
	req := &computepb.GetInstanceGroupManagerRequest{
		Project:              project,
		Zone:                 zone,
		InstanceGroupManager: name,
	}
	return c.igmClient.Get(ctx, req)
}

func (c *gcpZonalComputeClient) ListManagedInstances(ctx context.Context, project, zone, name string) ([]*computepb.ManagedInstance, error) {
	req := &computepb.ListManagedInstancesInstanceGroupManagersRequest{
		Project:              project,
		Zone:                 zone,
		InstanceGroupManager: name,
	}

	var instances []*computepb.ManagedInstance
	it := c.igmClient.ListManagedInstances(ctx, req)
	for {
		instance, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

func (c *gcpZonalComputeClient) DeleteInstance(ctx context.Context, project, zone, igmName, instanceURL string) error {
	req := &computepb.DeleteInstancesInstanceGroupManagerRequest{
		Project:              project,
		Zone:                 zone,
		InstanceGroupManager: igmName,
		InstanceGroupManagersDeleteInstancesRequestResource: &computepb.InstanceGroupManagersDeleteInstancesRequest{
			Instances: []string{instanceURL},
		},
	}
	op, err := c.igmClient.DeleteInstances(ctx, req)
	if err != nil {
		return err
	}
	return op.Wait(ctx)
}

func (c *gcpZonalComputeClient) ResizeIGM(ctx context.Context, project, zone, name string, newSize int64) error {
	req := &computepb.ResizeInstanceGroupManagerRequest{
		Project:              project,
		Zone:                 zone,
		InstanceGroupManager: name,
		Size:                 int32(newSize),
	}
	op, err := c.igmClient.Resize(ctx, req)
	if err != nil {
		return err
	}
	return op.Wait(ctx)
}

// Close releases the underlying client resources.
func (c *gcpZonalComputeClient) Close() error {
	if c.igmClient != nil {
		return c.igmClient.Close()
	}
	return nil
}

// gcpRegionalComputeClient methods

func (c *gcpRegionalComputeClient) GetInstanceGroupManager(ctx context.Context, project, region, name string) (*computepb.InstanceGroupManager, error) {
	req := &computepb.GetRegionInstanceGroupManagerRequest{
		Project:              project,
		Region:               region,
		InstanceGroupManager: name,
	}
	return c.igmClient.Get(ctx, req)
}

func (c *gcpRegionalComputeClient) ListManagedInstances(ctx context.Context, project, region, name string) ([]*computepb.ManagedInstance, error) {
	req := &computepb.ListManagedInstancesRegionInstanceGroupManagersRequest{
		Project:              project,
		Region:               region,
		InstanceGroupManager: name,
	}

	var instances []*computepb.ManagedInstance
	it := c.igmClient.ListManagedInstances(ctx, req)
	for {
		instance, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		instances = append(instances, instance)
	}
	return instances, nil
}

func (c *gcpRegionalComputeClient) DeleteInstance(ctx context.Context, project, region, igmName, instanceURL string) error {
	req := &computepb.DeleteInstancesRegionInstanceGroupManagerRequest{
		Project:              project,
		Region:               region,
		InstanceGroupManager: igmName,
		RegionInstanceGroupManagersDeleteInstancesRequestResource: &computepb.RegionInstanceGroupManagersDeleteInstancesRequest{
			Instances: []string{instanceURL},
		},
	}
	op, err := c.igmClient.DeleteInstances(ctx, req)
	if err != nil {
		return err
	}
	return op.Wait(ctx)
}

func (c *gcpRegionalComputeClient) ResizeIGM(ctx context.Context, project, region, name string, newSize int64) error {
	req := &computepb.ResizeRegionInstanceGroupManagerRequest{
		Project:              project,
		Region:               region,
		InstanceGroupManager: name,
		Size:                 int32(newSize),
	}
	op, err := c.igmClient.Resize(ctx, req)
	if err != nil {
		return err
	}
	return op.Wait(ctx)
}

// Close releases the underlying client resources.
func (c *gcpRegionalComputeClient) Close() error {
	if c.igmClient != nil {
		return c.igmClient.Close()
	}
	return nil
}

// --- Helpers (in order of first reference) ---

// parseGCPIGMSelfLink parses a GCP Instance Group Manager self-link.
// Uses a single regex pattern to handle both zonal and regional formats:
//   - Zonal: projects/{project}/zones/{zone}/instanceGroupManagers/{name}
//   - Regional: projects/{project}/regions/{region}/instanceGroupManagers/{name}
func parseGCPIGMSelfLink(selfLink string) (*igmID, error) {
	if selfLink == "" {
		return nil, errors.New("IGM self-link cannot be empty")
	}

	matches := gcpIGMSelfLinkRegex.FindStringSubmatch(selfLink)
	if matches == nil {
		return nil, fmt.Errorf("invalid IGM self-link format: %q does not match expected pattern "+
			"projects/{project}/zones/{zone}/instanceGroupManagers/{name} or "+
			"projects/{project}/regions/{region}/instanceGroupManagers/{name}", selfLink)
	}

	return &igmID{
		Project:    matches[1],
		Location:   matches[3],
		Name:       matches[4],
		IsRegional: matches[2] == "regions",
	}, nil
}

// parseGCPInstanceURL parses a GCP instance resource path to extract project, zone, and instance name.
// The expected format is: projects/{project}/zones/{zone}/instances/{name}
//
// Note: Full URLs from the GCP API are stripped to resource paths by stripGCPInstanceURLPrefix
// in GetAutoscalingGroup before being stored, so this function only handles resource paths.
func parseGCPInstanceURL(instanceURLStr string) (*instanceURL, error) {
	if instanceURLStr == "" {
		return nil, errors.New("instance URL cannot be empty")
	}

	matches := gcpInstanceURLRegex.FindStringSubmatch(instanceURLStr)
	if matches == nil {
		return nil, fmt.Errorf("invalid instance URL format: %q does not match expected pattern projects/{project}/zones/{zone}/instances/{name}", instanceURLStr)
	}

	return &instanceURL{
		Project: matches[1],
		Zone:    matches[2],
		Name:    matches[3],
	}, nil
}

// stripGCPInstanceURLPrefix strips the URL prefix from a GCP instance URL, returning only
// the resource path. If the URL is already a resource path, it is returned unchanged.
// Example: "https://www.googleapis.com/compute/v1/projects/p/zones/z/instances/i" -> "projects/p/zones/z/instances/i"
func stripGCPInstanceURLPrefix(instanceURL string) string {
	for _, prefix := range gcpInstanceURLPrefixes {
		if strings.HasPrefix(instanceURL, prefix) {
			return strings.TrimPrefix(instanceURL, prefix)
		}
	}
	return instanceURL
}

// mapGCPCurrentActionToLifecycleState maps GCP ManagedInstance CurrentAction values to the
// LifecycleState constants expected by state.go.
//
// GCP CurrentAction values:
//   - NONE: The instance is running and not undergoing any changes (available).
//   - CREATING: The instance is in the process of being created.
//   - CREATING_WITHOUT_RETRIES: Instance is being created without retries.
//   - DELETING: The instance is in the process of being deleted.
//   - RECREATING: The instance is being replaced.
//   - REFRESHING: The instance is being removed from target pools and readded.
//   - RESTARTING: The instance is being restarted.
//   - RESUMING, STARTING, STOPPING, SUSPENDING, VERIFYING: Additional transitional states.
//
// An instance is considered available when its currentAction is NONE.
// See: https://cloud.google.com/compute/docs/reference/rest/v1/instanceGroupManagers/listManagedInstances
func mapGCPCurrentActionToLifecycleState(currentAction string) string {
	switch currentAction {
	case "NONE":
		// NONE means the instance is running and not undergoing any changes.
		return LifecycleStateInService
	case "DELETING":
		// DELETING means the instance is being terminated.
		return LifecycleStateTerminating
	default:
		// For other transitional states (CREATING, RECREATING, RESTARTING, etc.),
		// keep the original value. These instances are not yet fully in-service.
		return currentAction
	}
}
