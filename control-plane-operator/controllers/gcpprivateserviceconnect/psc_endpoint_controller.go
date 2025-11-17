package gcpprivateserviceconnect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	"github.com/go-logr/logr"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"k8s.io/client-go/util/workqueue"
)

const (
	pscEndpointFinalizer                   = "hypershift.openshift.io/gcp-psc-customer"
	pscEndpointDeletionRequeueDuration     = 5 * time.Second // Match AWS pattern
)

// gcpClientBuilder manages GCP client creation with HCP configuration
type gcpClientBuilder struct {
	initialized             bool
	customerProject         string
	region                  string
	controlPlaneOperatorGSA string
}

func (b *gcpClientBuilder) initializeWithHCP(hcp *hyperv1.HostedControlPlane) {
	if !b.initialized {
		b.setFromHCP(hcp)
		b.initialized = true
	}
}

func (b *gcpClientBuilder) setFromHCP(hcp *hyperv1.HostedControlPlane) {
	if hcp.Spec.Platform.GCP != nil {
		b.customerProject = hcp.Spec.Platform.GCP.Project
		b.region = hcp.Spec.Platform.GCP.Region
		// TODO: Extract controlPlaneOperatorGSA when RolesRef is available
		b.controlPlaneOperatorGSA = ""
	} else {
		b.customerProject = ""
		b.region = ""
		b.controlPlaneOperatorGSA = ""
	}
}

func (b *gcpClientBuilder) getClient(ctx context.Context) (*compute.Service, error) {
	if !b.initialized {
		return nil, errors.New("client not initialized")
	}

	return InitCustomerGCPClient(ctx, b.customerProject, b.controlPlaneOperatorGSA)
}

// GCPPrivateServiceConnectReconciler manages PSC endpoints in customer projects
type GCPPrivateServiceConnectReconciler struct {
	client.Client
	upsert.CreateOrUpdateProvider
	gcpClientBuilder gcpClientBuilder
}

// SetupWithManager sets up the controller with the Manager.
func (r *GCPPrivateServiceConnectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	_, err := ctrl.NewControllerManagedBy(mgr).
		For(&hyperv1.GCPPrivateServiceConnect{}).
		WithOptions(controller.Options{
			RateLimiter:             workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](3*time.Second, 30*time.Second),
			MaxConcurrentReconciles: 10,
		}).
		Build(r)
	if err != nil {
		return fmt.Errorf("failed setting up with a controller manager: %w", err)
	}
	r.Client = mgr.GetClient()

	return nil
}

// Reconcile implements the main reconciliation logic for PSC endpoints
func (r *GCPPrivateServiceConnectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.Log.WithName("gcp-psc-endpoint-controller").WithValues("gcpprivateserviceconnect", req.NamespacedName)

	// 1. Fetch GCPPrivateServiceConnect CR
	obj := &hyperv1.GCPPrivateServiceConnect{}
	if err := r.Get(ctx, req.NamespacedName, obj); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("failed to get GCPPrivateServiceConnect: %w", err)
	}

	// Don't change the cached object
	gcpPSC := obj.DeepCopy()

	// 2. Handle deletion
	if !gcpPSC.DeletionTimestamp.IsZero() {
		if !controllerutil.ContainsFinalizer(gcpPSC, pscEndpointFinalizer) {
			// If we previously removed our finalizer, don't delete again and return early
			return ctrl.Result{}, nil
		}

		// Attempt cleanup using client builder (following AWS pattern)
		customerGCPClient, err := r.gcpClientBuilder.getClient(ctx)
		if err != nil {
			log.Error(err, "failed to get GCP client, skipping PSC endpoint cleanup")
		} else {
			completed, err := r.reconcileDelete(ctx, gcpPSC, customerGCPClient, log)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to delete resource: %w", err)
			}
			if !completed {
				return ctrl.Result{RequeueAfter: pscEndpointDeletionRequeueDuration}, nil
			}
		}

		// Always remove finalizer regardless of cleanup success (following AWS pattern)
		if controllerutil.ContainsFinalizer(gcpPSC, pscEndpointFinalizer) {
			controllerutil.RemoveFinalizer(gcpPSC, pscEndpointFinalizer)
			if err := r.Update(ctx, gcpPSC); err != nil {
				return ctrl.Result{}, fmt.Errorf("failed to remove finalizer: %w", err)
			}
		}
		return ctrl.Result{}, nil
	}

	// 3. Add finalizer if not present
	if !controllerutil.ContainsFinalizer(gcpPSC, pscEndpointFinalizer) {
		controllerutil.AddFinalizer(gcpPSC, pscEndpointFinalizer)
		return ctrl.Result{}, r.Update(ctx, gcpPSC)
	}

	// 4. Find the hosted control plane (for normal reconciliation)
	hcp, err := r.getHostedControlPlane(ctx, gcpPSC)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get HostedControlPlane: %w", err)
	}

	// 5. Check if reconciliation is paused (following AWS pattern)
	if isPaused, duration := util.IsReconciliationPaused(log, hcp.Spec.PausedUntil); isPaused {
		log.Info("Reconciliation paused", "pausedUntil", *hcp.Spec.PausedUntil)
		return ctrl.Result{RequeueAfter: duration}, nil
	}

	// 6. Initialize client builder with HCP configuration
	r.gcpClientBuilder.initializeWithHCP(hcp)

	// 7. Wait for Service Attachment to be ready
	if !r.isServiceAttachmentReady(gcpPSC) {
		log.Info("Waiting for Service Attachment to be ready")
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	// 8. Get customer GCP client using client builder
	customerGCPClient, err := r.gcpClientBuilder.getClient(ctx)
	if err != nil {
		log.Error(err, "failed to create customer GCP client")
		return ctrl.Result{RequeueAfter: time.Minute * 5}, nil
	}

	// Extract project and region from client builder
	customerProject := r.gcpClientBuilder.customerProject
	region := r.gcpClientBuilder.region

	// 9. Ensure IP address is reserved
	if result, err := r.ensureIPAddress(ctx, gcpPSC, hcp, customerGCPClient, customerProject, region, log); err != nil || !result.IsZero() {
		return result, err
	}

	// 10. Reconcile PSC Endpoint
	return r.reconcilePSCEndpoint(ctx, gcpPSC, hcp, customerGCPClient, customerProject, region, log)
}

// isServiceAttachmentReady checks if the management-side Service Attachment is ready
func (r *GCPPrivateServiceConnectReconciler) isServiceAttachmentReady(gcpPSC *hyperv1.GCPPrivateServiceConnect) bool {
	// Check if management-side has populated ServiceAttachmentURI
	if gcpPSC.Status.ServiceAttachmentURI == "" {
		return false
	}

	// Check if GCPServiceAttachmentAvailable condition is True
	for _, condition := range gcpPSC.Status.Conditions {
		if condition.Type == string(hyperv1.GCPServiceAttachmentAvailable) {
			return condition.Status == metav1.ConditionTrue
		}
	}

	return false
}

// ensureIPAddress reserves a static IP address for the PSC endpoint
func (r *GCPPrivateServiceConnectReconciler) ensureIPAddress(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, hcp *hyperv1.HostedControlPlane, customerGCPClient *compute.Service, customerProject, region string, log logr.Logger) (ctrl.Result, error) {
	// Check if IP already allocated and recorded in status
	if gcpPSC.Status.EndpointIP != "" {
		// Verify IP still exists in GCP
		if exists, err := r.verifyIPExists(ctx, gcpPSC, customerGCPClient, customerProject, region); err != nil {
			return ctrl.Result{}, err
		} else if exists {
			return ctrl.Result{}, nil // IP ready
		}
		// IP was deleted, need to allocate new one
		log.Info("Previously allocated IP no longer exists, allocating new one")
	}

	pscSubnet := hcp.Spec.Platform.GCP.NetworkConfig.PrivateServiceConnectSubnet.Name
	if pscSubnet == "" {
		return ctrl.Result{}, fmt.Errorf("PrivateServiceConnectSubnet not specified in HostedControlPlane")
	}

	// Reserve static internal IP
	ipName := r.constructIPAddressName(gcpPSC)

	// First check if IP address already exists in GCP (make operation idempotent)
	existingAddress, err := customerGCPClient.Addresses.Get(customerProject, region, ipName).Context(ctx).Do()
	if err != nil && !isNotFoundError(err) {
		return ctrl.Result{}, fmt.Errorf("failed to check existing IP address: %w", err)
	}

	if existingAddress != nil {
		// IP already exists, update status and continue
		log.Info("IP address already exists, updating status", "name", ipName, "ip", existingAddress.Address)
		patch := client.MergeFrom(gcpPSC.DeepCopy())
		gcpPSC.Status.EndpointIP = existingAddress.Address
		if err := r.Status().Patch(ctx, gcpPSC, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("failed to update EndpointIP with existing address: %w", err)
		}
		return ctrl.Result{}, nil
	}

	// IP doesn't exist, create it
	ipAddress := &compute.Address{
		Name:        ipName,
		Description: fmt.Sprintf("PSC endpoint IP for HyperShift cluster %s", gcpPSC.Name),
		AddressType: "INTERNAL",
		Subnetwork:  r.constructSubnetURL(pscSubnet, customerProject, region),
		// Purpose not set for subnetwork addresses - PSC purpose is implicit when used with ForwardingRule
	}

	log.Info("Reserving new IP address for PSC endpoint", "name", ipName, "subnet", pscSubnet)
	op, err := customerGCPClient.Addresses.Insert(customerProject, region, ipAddress).Context(ctx).Do()
	if err != nil {
		return r.handleGCPError(ctx, gcpPSC, "IPReservationFailed", err)
	}

	if op.Status == "RUNNING" {
		log.Info("IP reservation in progress", "operation", op.Name)
		return ctrl.Result{RequeueAfter: time.Second * 15}, nil
	}

	// Get the allocated IP address
	allocatedAddress, err := customerGCPClient.Addresses.Get(customerProject, region, ipName).Context(ctx).Do()
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to get allocated IP: %w", err)
	}

	// Update status with allocated IP
	patch := client.MergeFrom(gcpPSC.DeepCopy())
	gcpPSC.Status.EndpointIP = allocatedAddress.Address
	if err := r.Status().Patch(ctx, gcpPSC, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("failed to update EndpointIP: %w", err)
	}

	log.Info("Successfully reserved IP address", "ip", allocatedAddress.Address)
	return ctrl.Result{}, nil
}

// verifyIPExists checks if the IP address still exists in GCP
func (r *GCPPrivateServiceConnectReconciler) verifyIPExists(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, customerGCPClient *compute.Service, customerProject, region string) (bool, error) {
	ipName := r.constructIPAddressName(gcpPSC)
	_, err := customerGCPClient.Addresses.Get(customerProject, region, ipName).Context(ctx).Do()
	if err != nil {
		if isNotFoundError(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// reconcilePSCEndpoint creates or updates the PSC endpoint
func (r *GCPPrivateServiceConnectReconciler) reconcilePSCEndpoint(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, hcp *hyperv1.HostedControlPlane, customerGCPClient *compute.Service, customerProject, region string, log logr.Logger) (ctrl.Result, error) {
	endpointName := r.constructEndpointName(gcpPSC)

	// Check if endpoint already exists
	existingEndpoint, err := customerGCPClient.ForwardingRules.Get(customerProject, region, endpointName).Context(ctx).Do()
	if err != nil && !isNotFoundError(err) {
		return ctrl.Result{}, fmt.Errorf("failed to check existing PSC endpoint: %w", err)
	}

	if existingEndpoint != nil {
		// Update status from existing endpoint
		return r.updateStatusFromEndpoint(ctx, gcpPSC, existingEndpoint)
	}

	// Create new PSC endpoint
	ipName := r.constructIPAddressName(gcpPSC)
	endpoint := &compute.ForwardingRule{
		Name:        endpointName,
		Description: fmt.Sprintf("PSC endpoint for HyperShift cluster %s", gcpPSC.Name),
		Network:     r.constructNetworkURL(hcp.Spec.Platform.GCP.NetworkConfig.Network.Name, customerProject),
		Subnetwork:  r.constructSubnetURL(hcp.Spec.Platform.GCP.NetworkConfig.PrivateServiceConnectSubnet.Name, customerProject, region),
		Target:      gcpPSC.Status.ServiceAttachmentURI,                     // From management-side
		IPAddress:   r.constructAddressURL(ipName, customerProject, region), // Reserved IP resource URL
		// LoadBalancingScheme not set for PSC endpoints - it's implicit and setting it causes API errors
	}

	log.Info("Creating PSC endpoint", "name", endpointName, "serviceAttachment", gcpPSC.Status.ServiceAttachmentURI)
	op, err := customerGCPClient.ForwardingRules.Insert(customerProject, region, endpoint).Context(ctx).Do()
	if err != nil {
		return r.handleGCPError(ctx, gcpPSC, "PSCEndpointCreationFailed", err)
	}

	if op.Status == "RUNNING" {
		log.Info("PSC endpoint creation in progress", "operation", op.Name)
		return ctrl.Result{RequeueAfter: time.Second * 30}, nil
	}

	return ctrl.Result{}, nil
}

// updateStatusFromEndpoint updates the CR status based on the PSC endpoint state
func (r *GCPPrivateServiceConnectReconciler) updateStatusFromEndpoint(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, endpoint *compute.ForwardingRule) (ctrl.Result, error) {
	patch := client.MergeFrom(gcpPSC.DeepCopy())

	// Update condition based on endpoint status
	now := metav1.Now()

	// Check if endpoint is ready (has IP and target)
	if endpoint.IPAddress != "" && endpoint.Target != "" {
		meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.GCPEndpointAvailable),
			Status:             metav1.ConditionTrue,
			Reason:             hyperv1.GCPSuccessReason,
			Message:            "PSC endpoint is ready and accepting connections",
			LastTransitionTime: now,
		})
	} else {
		meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.GCPEndpointAvailable),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.GCPErrorReason,
			Message:            fmt.Sprintf("PSC endpoint not ready: IP=%s, Target=%s", endpoint.IPAddress, endpoint.Target),
			LastTransitionTime: now,
		})
	}

	return ctrl.Result{}, r.Status().Patch(ctx, gcpPSC, patch)
}

// reconcileDelete handles cleanup when the CR is being deleted
func (r *GCPPrivateServiceConnectReconciler) reconcileDelete(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, customerGCPClient *compute.Service, log logr.Logger) (bool, error) {
	// Get customer project and region from client builder
	customerProject := r.gcpClientBuilder.customerProject
	region := r.gcpClientBuilder.region

	// Delete PSC endpoint (following management-side GCP pattern)
	endpointName := r.constructEndpointName(gcpPSC)
	log.Info("Deleting PSC endpoint", "name", endpointName)

	op, err := customerGCPClient.ForwardingRules.Delete(customerProject, region, endpointName).Context(ctx).Do()
	if err != nil {
		if isNotFoundError(err) {
			// PSC endpoint already deleted, consider it completed
			log.Info("PSC endpoint not found, deletion already completed", "name", endpointName)
		} else {
			return false, fmt.Errorf("failed to delete PSC endpoint: %w", err)
		}
	} else {
		// Check operation status (following management-side pattern)
		if op != nil && op.Status == "RUNNING" {
			log.Info("PSC endpoint deletion in progress", "operation", op.Name)
			return false, nil // Not completed yet
		}

		log.Info("PSC endpoint deletion completed", "name", endpointName)
	}

	// Delete reserved IP address
	if gcpPSC.Status.EndpointIP != "" {
		ipName := r.constructIPAddressName(gcpPSC)
		log.Info("Deleting reserved IP address", "name", ipName, "ip", gcpPSC.Status.EndpointIP)

		_, err := customerGCPClient.Addresses.Delete(customerProject, region, ipName).Context(ctx).Do()
		if err != nil && !isNotFoundError(err) {
			log.Error(err, "failed to delete reserved IP, continuing with cleanup")
		} else {
			log.Info("Reserved IP address deleted", "name", ipName)
		}
	}

	return true, nil // Deletion completed
}

// Helper functions for resource naming and URL construction

func (r *GCPPrivateServiceConnectReconciler) constructEndpointName(gcpPSC *hyperv1.GCPPrivateServiceConnect) string {
	// Include namespace to ensure uniqueness across different clusters
	// Replace any characters that aren't valid for GCP resource names
	safeName := strings.ReplaceAll(gcpPSC.Name, "_", "-")
	safeNamespace := strings.ReplaceAll(gcpPSC.Namespace, "_", "-")
	return fmt.Sprintf("%s-%s-psc-endpoint", safeNamespace, safeName)
}

func (r *GCPPrivateServiceConnectReconciler) constructIPAddressName(gcpPSC *hyperv1.GCPPrivateServiceConnect) string {
	// Include namespace to ensure uniqueness across different clusters
	// Replace any characters that aren't valid for GCP resource names
	safeName := strings.ReplaceAll(gcpPSC.Name, "_", "-")
	safeNamespace := strings.ReplaceAll(gcpPSC.Namespace, "_", "-")
	return fmt.Sprintf("%s-%s-psc-endpoint-ip", safeNamespace, safeName)
}

func (r *GCPPrivateServiceConnectReconciler) constructIPAddressNameFromIP(ip string) string {
	// This is a placeholder - in reality we'd need to track the name more precisely
	// For now, we'll use a naming convention that can be reverse-engineered
	return fmt.Sprintf("psc-endpoint-ip-%s", ip)
}

func (r *GCPPrivateServiceConnectReconciler) constructNetworkURL(networkName, customerProject string) string {
	return fmt.Sprintf("projects/%s/global/networks/%s", customerProject, networkName)
}

func (r *GCPPrivateServiceConnectReconciler) constructSubnetURL(subnetName, customerProject, region string) string {
	return fmt.Sprintf("projects/%s/regions/%s/subnetworks/%s", customerProject, region, subnetName)
}

func (r *GCPPrivateServiceConnectReconciler) constructAddressURL(addressName, customerProject, region string) string {
	return fmt.Sprintf("projects/%s/regions/%s/addresses/%s", customerProject, region, addressName)
}

// getHostedControlPlane retrieves the HostedControlPlane from the CR's owner reference
func (r *GCPPrivateServiceConnectReconciler) getHostedControlPlane(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect) (*hyperv1.HostedControlPlane, error) {
	// Find HCP from owner reference
	for _, ownerRef := range gcpPSC.OwnerReferences {
		if ownerRef.Kind == "HostedControlPlane" && ownerRef.APIVersion == hyperv1.GroupVersion.String() {
			hcp := &hyperv1.HostedControlPlane{}
			if err := r.Get(ctx, types.NamespacedName{Namespace: gcpPSC.Namespace, Name: ownerRef.Name}, hcp); err != nil {
				return nil, fmt.Errorf("failed to get HostedControlPlane %s: %w", ownerRef.Name, err)
			}
			return hcp, nil
		}
	}

	return nil, fmt.Errorf("no HostedControlPlane owner reference found")
}

// handleGCPError handles GCP API errors with appropriate retry logic
func (r *GCPPrivateServiceConnectReconciler) handleGCPError(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, reason string, err error) (ctrl.Result, error) {
	log, _ := logr.FromContext(ctx)

	var requeueAfter time.Duration
	var message string

	if googleErr, ok := err.(*googleapi.Error); ok {
		switch googleErr.Code {
		case 429: // Rate limit
			requeueAfter = time.Minute * 5
			message = "GCP API rate limit exceeded, retrying"
		case 403: // Permission denied
			requeueAfter = time.Minute * 10
			message = "GCP API permission denied, check customer project permissions"
		case 409: // Conflict (IP already allocated, etc.)
			requeueAfter = time.Second * 30
			message = "GCP resource conflict, retrying"
		case 400: // Bad request (subnet full, invalid config, etc.)
			requeueAfter = time.Minute * 5
			message = fmt.Sprintf("GCP configuration error: %s", googleErr.Message)
		default:
			requeueAfter = time.Minute * 2
			message = fmt.Sprintf("GCP API error: %s", googleErr.Message)
		}
	} else {
		requeueAfter = time.Minute * 2
		message = fmt.Sprintf("Unexpected error: %s", err.Error())
	}

	log.Error(err, message)

	// Update condition with error
	patch := client.MergeFrom(gcpPSC.DeepCopy())
	meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.GCPEndpointAvailable),
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Patch(ctx, gcpPSC, patch); err != nil {
		log.Error(err, "failed to update status condition")
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// isNotFoundError checks if the error is a GCP "not found" error
func isNotFoundError(err error) bool {
	if googleErr, ok := err.(*googleapi.Error); ok {
		return googleErr.Code == 404
	}
	return false
}

// InitCustomerGCPClient initializes the GCP client for customer project operations
func InitCustomerGCPClient(ctx context.Context, customerProject string, controlPlaneOperatorGSA string) (*compute.Service, error) {
	// For GCP-198 implementation, we'll use these approaches in order:
	// Option A: Use controlPlaneOperatorGSA from RolesRef (when WIF is fully implemented)
	// Option B: Use service account key from secret (immediate approach)

	// Option A: WIF-based authentication (future implementation)
	// TODO: Uncomment when RolesRef field is added to GCPPlatformSpec
	// if controlPlaneOperatorGSA != "" {
	//     return initWIFClient(ctx, customerProject, controlPlaneOperatorGSA)
	// }

	// Option B: Service Account Key authentication (immediate implementation)
	return initServiceAccountClient(ctx)
}

// Service Account Key authentication (immediate approach)
func initServiceAccountClient(ctx context.Context) (*compute.Service, error) {
	// Look for gcp-customer-credentials secret mounted at standard path
	credentialsPath := "/etc/gcp/service-account.json"
	if _, err := os.Stat(credentialsPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("gcp-customer-credentials secret not found at %s", credentialsPath)
	}

	// Create client with service account key from secret
	client, err := google.DefaultClient(ctx, compute.ComputeScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create service account client: %w", err)
	}

	service, err := compute.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create Compute service: %w", err)
	}

	return service, nil
}

// Future WIF-based authentication (when WIF integration is complete)
func initWIFClient(ctx context.Context, projectID, gsaEmail string) (*compute.Service, error) {
	// This will be implemented following the WIF integration pattern
	// Similar to: gcp-wif-integration.md credential file approach

	credentialFile := fmt.Sprintf(`{
        "type": "external_account",
        "audience": "//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/providers/%s",
        "subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
        "token_url": "https://sts.googleapis.com/v1/token",
        "service_account_impersonation_url": "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%s:generateAccessToken",
        "credential_source": {
            "file": "/var/run/secrets/openshift/serviceaccount/token"
        }
    }`, projectID, "POOL_ID", "PROVIDER_ID", gsaEmail)

	// Create temporary credential file and use with Google client
	// Implementation details follow WIF integration pattern
	_ = credentialFile // Suppress unused variable warning for now

	// Placeholder - actual implementation will be added when WIF is available
	return nil, fmt.Errorf("WIF authentication not yet implemented")
}
