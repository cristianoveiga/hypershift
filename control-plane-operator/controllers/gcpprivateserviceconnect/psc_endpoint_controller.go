package gcpprivateserviceconnect

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util"

	"github.com/go-logr/logr"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"

	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	pscEndpointFinalizer               = "hypershift.openshift.io/gcp-psc-customer"
	pscEndpointDeletionRequeueDuration = 5 * time.Second // Match AWS pattern
	externalPrivateServiceLabelGCP     = "hypershift.openshift.io/gcp-psc-external-private-svc"
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
	if result, err := r.reconcilePSCEndpoint(ctx, gcpPSC, hcp, customerGCPClient, customerProject, region, log); err != nil || !result.IsZero() {
		return result, err
	}

	// 11. Reconcile DNS zones and records (after PSC endpoint is available)
	if result, err := r.reconcileDNS(ctx, gcpPSC, hcp, log); err != nil || !result.IsZero() {
		return result, err
	}

	// 12. Reconcile external-dns services for private clusters with external names
	return r.reconcileExternalServices(ctx, gcpPSC, hcp, log)
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

	// Clean up DNS zones and records using zone names from PSC status (following AWS blocking pattern)
	log.Info("Cleaning up DNS zones and records")

	if dnsErr := r.cleanupDNS(ctx, gcpPSC); dnsErr != nil {
		log.Error(dnsErr, "failed to clean up DNS zones")
		return false, fmt.Errorf("failed to clean up DNS zones: %w", dnsErr)
	}
	log.Info("DNS cleanup completed successfully")

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

// reconcileDNS reconciles DNS zones and records after PSC endpoint is available
func (r *GCPPrivateServiceConnectReconciler) reconcileDNS(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, hcp *hyperv1.HostedControlPlane, log logr.Logger) (ctrl.Result, error) {
	// DNS reconciliation should always run to manage records, regardless of CreateDnsZones setting
	// The ReconcileDNS function in dns.go handles the CreateDnsZones logic internally:
	// - true: Manages zones AND records (creates zones if missing)
	// - false/nil: Assumes zones are externally managed, only ensures records exist

	// Add nil safety checks
	if hcp == nil || hcp.Spec.Platform.GCP == nil {
		log.Info("Invalid HCP configuration for DNS reconciliation")
		return ctrl.Result{}, nil
	}

	// Ensure PSC endpoint is available before creating DNS records
	endpointAvailable := false
	for _, condition := range gcpPSC.Status.Conditions {
		if condition.Type == string(hyperv1.GCPEndpointAvailable) && condition.Status == metav1.ConditionTrue {
			endpointAvailable = true
			break
		}
	}

	if !endpointAvailable {
		log.Info("PSC endpoint not available yet, skipping DNS reconciliation")
		return ctrl.Result{}, nil
	}

	if gcpPSC.Status.EndpointIP == "" {
		log.Info("PSC endpoint IP not available yet, skipping DNS reconciliation")
		return ctrl.Result{}, nil
	}

	log.Info("Reconciling DNS zones and records", "endpointIP", gcpPSC.Status.EndpointIP)

	// Save current status for comparison
	patch := client.MergeFrom(gcpPSC.DeepCopy())

	// Call the existing ReconcileDNS function from dns.go
	dnsResult, err := ReconcileDNS(ctx, hcp, gcpPSC.Status.EndpointIP)
	if err != nil {
		log.Error(err, "Failed to reconcile DNS zones and records")

		// Set GCPDNSAvailable condition to False
		meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
			Type:               string(hyperv1.GCPDNSAvailable),
			Status:             metav1.ConditionFalse,
			Reason:             hyperv1.GCPErrorReason,
			Message:            fmt.Sprintf("DNS reconciliation failed: %s", err.Error()),
			LastTransitionTime: metav1.Now(),
		})

		if statusErr := r.Status().Patch(ctx, gcpPSC, patch); statusErr != nil {
			log.Error(statusErr, "failed to update DNS condition after error")
		}

		return ctrl.Result{RequeueAfter: time.Minute * 2}, nil
	}

	// Update status fields with DNS information using new DNSZones structure
	// Set ManagedByOperator based on CreateDnsZones setting
	managedByOperator := false
	if hcp.Spec.Platform.GCP.CreateDnsZones != nil && *hcp.Spec.Platform.GCP.CreateDnsZones {
		managedByOperator = true
	}

	gcpPSC.Status.DNSZones = []hyperv1.DNSZoneStatus{
		{
			Name:              dnsResult.HypershiftLocalZone.Name,
			Records:           dnsResult.HypershiftLocalCreatedRecords,
			ManagedByOperator: managedByOperator,
		},
		{
			Name:              dnsResult.PublicIngressZone.Name,
			Records:           dnsResult.PublicIngressCreatedRecords,
			ManagedByOperator: managedByOperator,
		},
		{
			Name:              dnsResult.PrivateIngressZone.Name,
			Records:           dnsResult.PrivateIngressCreatedRecords,
			ManagedByOperator: managedByOperator,
		},
	}

	// Set GCPDNSAvailable condition to True
	meta.SetStatusCondition(&gcpPSC.Status.Conditions, metav1.Condition{
		Type:               string(hyperv1.GCPDNSAvailable),
		Status:             metav1.ConditionTrue,
		Reason:             hyperv1.GCPSuccessReason,
		Message:            "DNS zones and records successfully created",
		LastTransitionTime: metav1.Now(),
	})

	// Update status with DNS information
	if err := r.Status().Patch(ctx, gcpPSC, patch); err != nil {
		log.Error(err, "failed to update status with DNS information")
		return ctrl.Result{}, err
	}

	log.Info("DNS reconciliation completed successfully", "zones", len(gcpPSC.Status.DNSZones))
	return ctrl.Result{}, nil
}

// cleanupDNS cleans up DNS zones using zone names and management info stored in PSC status
// This allows reliable DNS cleanup using actual zone information from when zones were created
// Only deletes zones that have ManagedByOperator=true in their status
func (r *GCPPrivateServiceConnectReconciler) cleanupDNS(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect) error {
	// Check if we have zone information in status
	if len(gcpPSC.Status.DNSZones) == 0 {
		return nil // No DNS zones to clean up
	}

	// Check if we have the customer project for cleanup
	if !r.gcpClientBuilder.initialized || r.gcpClientBuilder.customerProject == "" {
		return nil // Cannot clean up without project information
	}

	customerProject := r.gcpClientBuilder.customerProject

	// Create DNS client for cleanup operations
	svc, err := newDNSClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create DNS client for cleanup: %w", err)
	}

	// Clean up each zone based on management status
	var errs []error
	for _, zoneStatus := range gcpPSC.Status.DNSZones {
		if zoneStatus.Name == "" {
			continue // Skip empty zone names
		}

		if zoneStatus.ManagedByOperator {
			// Delete the entire zone (zone is managed by operator)
			if err := deleteZone(ctx, svc, customerProject, zoneStatus.Name); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete managed DNS zone %s: %w", zoneStatus.Name, err))
			}
		} else {
			// Delete only the records we created (zone is externally managed)
			if err := deleteAllRecordsFromZone(ctx, svc, customerProject, zoneStatus.Name); err != nil {
				errs = append(errs, fmt.Errorf("failed to delete records from external DNS zone %s: %w", zoneStatus.Name, err))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("DNS zone cleanup errors: %v", errs)
	}

	return nil
}

// Helper functions for resource naming and URL construction

func (r *GCPPrivateServiceConnectReconciler) constructEndpointName(gcpPSC *hyperv1.GCPPrivateServiceConnect) string {
	// Use service attachment name as base - it's unique and within GCP naming limits
	return fmt.Sprintf("%s-endpoint", gcpPSC.Status.ServiceAttachmentName)
}

func (r *GCPPrivateServiceConnectReconciler) constructIPAddressName(gcpPSC *hyperv1.GCPPrivateServiceConnect) string {
	// Use service attachment name as base - it's unique and within GCP naming limits
	return fmt.Sprintf("%s-ip", gcpPSC.Status.ServiceAttachmentName)
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

// InitCustomerGCPClient initializes the GCP client for customer project operations using WIF
func InitCustomerGCPClient(ctx context.Context, customerProject string, controlPlaneOperatorGSA string) (*compute.Service, error) {
	credentialsFile := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
	if credentialsFile == "" {
		return nil, fmt.Errorf("GOOGLE_APPLICATION_CREDENTIALS not set")
	}

	// Verify credentials file exists and is readable
	if _, err := os.Stat(credentialsFile); err != nil {
		return nil, fmt.Errorf("credentials file not accessible at %s: %w", credentialsFile, err)
	}

	// Create Google Cloud client using the credentials file from environment
	// google.DefaultClient() automatically reads GOOGLE_APPLICATION_CREDENTIALS
	// This supports both service account keys and WIF credential files
	client, err := google.DefaultClient(ctx, compute.CloudPlatformScope)
	if err != nil {
		return nil, fmt.Errorf("failed to create Google Cloud client using %s: %w", credentialsFile, err)
	}

	// Create the Compute Engine service
	computeService, err := compute.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("failed to create Compute Engine service: %w", err)
	}

	return computeService, nil
}

// reconcileExternalServices creates external-dns services for private clusters with external names
// This enables external-dns to create DNS records for private PSC endpoints with custom hostnames
func (r *GCPPrivateServiceConnectReconciler) reconcileExternalServices(ctx context.Context, gcpPSC *hyperv1.GCPPrivateServiceConnect, hcp *hyperv1.HostedControlPlane, log logr.Logger) (ctrl.Result, error) {
	if isPublic, externalNames := util.IsPublicHCP(hcp), hcpExternalNamesGCP(hcp); !isPublic && len(externalNames) > 0 {
		// Only if not public and external names are configured, create services of type ExternalName so external-dns
		// can create records for them
		var errs []error
		for svcType, externalName := range externalNames {
			var svc *corev1.Service
			switch svcType {
			case "api":
				svc = manifests.KubeAPIServerExternalPrivateService(hcp.Namespace)
			case "oauth":
				svc = manifests.OauthServerExternalPrivateService(hcp.Namespace)
			}
			if _, err := r.CreateOrUpdate(ctx, r, svc, func() error {
				log.Info("Reconciling external name service for GCP PSC", "service", svc.Name, "externalName", externalName)
				return reconcileExternalServiceGCP(svc, hcp, externalName, gcpPSC.Status.EndpointIP)
			}); err != nil {
				errs = append(errs, fmt.Errorf("failed to reconcile %s external service: %w", svcType, err))
			}
		}
		if len(errs) > 0 {
			return ctrl.Result{}, fmt.Errorf("failed to create external services for private PSC endpoints: %w", utilerrors.NewAggregate(errs))
		}
	} else {
		// If the cluster is public, ensure that any ExternalName services are removed
		privateExternalServices := &corev1.ServiceList{}
		if err := r.List(ctx, privateExternalServices, client.HasLabels{externalPrivateServiceLabelGCP}); err != nil {
			return ctrl.Result{}, fmt.Errorf("cannot list private external services: %w", err)
		}
		if len(privateExternalServices.Items) > 0 {
			log.Info("Removing private external services for GCP PSC", "count", len(privateExternalServices.Items))
			var errs []error
			for i := range privateExternalServices.Items {
				svc := &privateExternalServices.Items[i]
				if err := r.Delete(ctx, svc); err != nil {
					errs = append(errs, fmt.Errorf("failed to delete private external service %s: %w", svc.Name, err))
				}
			}
			if len(errs) > 0 {
				return ctrl.Result{}, utilerrors.NewAggregate(errs)
			}
		}
	}

	return ctrl.Result{}, nil
}

// hcpExternalNamesGCP extracts external hostnames from HCP configuration for GCP
func hcpExternalNamesGCP(hcp *hyperv1.HostedControlPlane) map[string]string {
	result := map[string]string{}
	apiStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.APIServer)
	if apiStrategy != nil && apiStrategy.Type == hyperv1.Route && apiStrategy.Route != nil && apiStrategy.Route.Hostname != "" {
		result["api"] = apiStrategy.Route.Hostname
	}

	oauthStrategy := util.ServicePublishingStrategyByTypeForHCP(hcp, hyperv1.OAuthServer)
	if oauthStrategy != nil && oauthStrategy.Type == hyperv1.Route && oauthStrategy.Route != nil && oauthStrategy.Route.Hostname != "" {
		result["oauth"] = oauthStrategy.Route.Hostname
	}
	return result
}

// reconcileExternalServiceGCP configures external service for GCP PSC endpoint integration
func reconcileExternalServiceGCP(svc *corev1.Service, hcp *hyperv1.HostedControlPlane, hostName, targetIP string) error {
	ownerRef := config.OwnerRefFrom(hcp)
	ownerRef.ApplyTo(svc)
	if svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	svc.Labels[externalPrivateServiceLabelGCP] = "true"
	svc.Annotations[hyperv1.ExternalDNSHostnameAnnotation] = hostName

	// For GCP PSC, we point external-dns to the PSC endpoint IP
	// external-dns will create A records pointing to this IP
	svc.Spec.Type = corev1.ServiceTypeExternalName
	svc.Spec.ExternalName = targetIP
	svc.Spec.Ports = []corev1.ServicePort{
		{
			Name:     "https",
			Port:     443,
			Protocol: corev1.ProtocolTCP,
		},
	}

	return nil
}
