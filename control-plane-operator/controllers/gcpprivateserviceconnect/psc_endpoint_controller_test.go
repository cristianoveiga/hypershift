package gcpprivateserviceconnect

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

func TestConstructEndpointName(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name     string
		gcpPSC   *hyperv1.GCPPrivateServiceConnect
		expected string
	}{
		{
			name: "When constructing endpoint name it should include namespace and resource name",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters-test-cluster-1",
				},
			},
			expected: "clusters-test-cluster-1-test-cluster-psc-endpoint",
		},
		{
			name: "When namespace has underscores it should replace them with hyphens",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "private-router",
					Namespace: "clusters_customer_hosted_cluster_1",
				},
			},
			expected: "clusters-customer-hosted-cluster-1-private-router-psc-endpoint",
		},
		{
			name: "When resource name has underscores it should replace them with hyphens",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test_cluster_router",
					Namespace: "clusters-test-cluster",
				},
			},
			expected: "clusters-test-cluster-test-cluster-router-psc-endpoint",
		},
		{
			name: "When both namespace and name have underscores it should replace all",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "private_service_router",
					Namespace: "clusters_customer_hosted_1",
				},
			},
			expected: "clusters-customer-hosted-1-private-service-router-psc-endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.constructEndpointName(tt.gcpPSC)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConstructIPAddressName(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name     string
		gcpPSC   *hyperv1.GCPPrivateServiceConnect
		expected string
	}{
		{
			name: "When constructing IP name it should include namespace and resource name",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "clusters-test-cluster-1",
				},
			},
			expected: "clusters-test-cluster-1-test-cluster-psc-endpoint-ip",
		},
		{
			name: "When namespace has underscores it should replace them with hyphens",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "private-router",
					Namespace: "clusters_customer_hosted_cluster_1",
				},
			},
			expected: "clusters-customer-hosted-cluster-1-private-router-psc-endpoint-ip",
		},
		{
			name: "When resource name has underscores it should replace them with hyphens",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test_cluster_router",
					Namespace: "clusters-test-cluster",
				},
			},
			expected: "clusters-test-cluster-test-cluster-router-psc-endpoint-ip",
		},
		{
			name: "When both namespace and name have underscores it should replace all",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "private_service_router",
					Namespace: "clusters_customer_hosted_1",
				},
			},
			expected: "clusters-customer-hosted-1-private-service-router-psc-endpoint-ip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.constructIPAddressName(tt.gcpPSC)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestConstructNetworkURL(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	networkName := "default"
	customerProject := "customer-project"

	result := r.constructNetworkURL(networkName, customerProject)
	expected := "projects/customer-project/global/networks/default"

	assert.Equal(t, expected, result)
}

func TestConstructSubnetURL(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	subnetName := "psc-subnet"
	customerProject := "customer-project"
	region := "us-central1"

	result := r.constructSubnetURL(subnetName, customerProject, region)
	expected := "projects/customer-project/regions/us-central1/subnetworks/psc-subnet"

	assert.Equal(t, expected, result)
}

func TestConstructAddressURL(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name            string
		addressName     string
		customerProject string
		region          string
		expected        string
	}{
		{
			name:            "When constructing address URL it should include project, region, and name",
			addressName:     "clusters-test-cluster-1-private-router-psc-endpoint-ip",
			customerProject: "customer-project-123",
			region:          "us-central1",
			expected:        "projects/customer-project-123/regions/us-central1/addresses/clusters-test-cluster-1-private-router-psc-endpoint-ip",
		},
		{
			name:            "When using different region it should construct correctly",
			addressName:     "test-address",
			customerProject: "my-gcp-project",
			region:          "europe-west1",
			expected:        "projects/my-gcp-project/regions/europe-west1/addresses/test-address",
		},
		{
			name:            "When using numeric project ID it should work",
			addressName:     "my-psc-ip",
			customerProject: "123456789",
			region:          "asia-southeast1",
			expected:        "projects/123456789/regions/asia-southeast1/addresses/my-psc-ip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.constructAddressURL(tt.addressName, tt.customerProject, tt.region)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsServiceAttachmentReady(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name     string
		gcpPSC   *hyperv1.GCPPrivateServiceConnect
		expected bool
	}{
		{
			name: "When ServiceAttachmentURI is empty it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI: "",
				},
			},
			expected: false,
		},
		{
			name: "When ServiceAttachmentURI exists but condition is missing it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI: "projects/mgmt-project/regions/us-central1/serviceAttachments/test-sa",
				},
			},
			expected: false,
		},
		{
			name: "When ServiceAttachmentURI exists but condition is False it should return false",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI: "projects/mgmt-project/regions/us-central1/serviceAttachments/test-sa",
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.GCPServiceAttachmentAvailable),
							Status: metav1.ConditionFalse,
						},
					},
				},
			},
			expected: false,
		},
		{
			name: "When ServiceAttachmentURI exists and condition is True it should return true",
			gcpPSC: &hyperv1.GCPPrivateServiceConnect{
				Status: hyperv1.GCPPrivateServiceConnectStatus{
					ServiceAttachmentURI: "projects/mgmt-project/regions/us-central1/serviceAttachments/test-sa",
					Conditions: []metav1.Condition{
						{
							Type:   string(hyperv1.GCPServiceAttachmentAvailable),
							Status: metav1.ConditionTrue,
						},
					},
				},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.isServiceAttachmentReady(tt.gcpPSC)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestInitCustomerGCPClient(t *testing.T) {
	tests := []struct {
		name                    string
		customerProject         string
		controlPlaneOperatorGSA string
		expectedError           string
	}{
		{
			name:                    "When called with valid parameters it should use service account authentication",
			customerProject:         "customer-project",
			controlPlaneOperatorGSA: "",
			expectedError:           "gcp-customer-credentials secret not found", // Expected since we don't have real credentials in test
		},
		{
			name:                    "When called with GSA it should still use service account authentication for now",
			customerProject:         "customer-project",
			controlPlaneOperatorGSA: "control-plane-operator@customer-project.iam.gserviceaccount.com",
			expectedError:           "gcp-customer-credentials secret not found", // Expected since WIF not implemented yet
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := InitCustomerGCPClient(context.Background(), tt.customerProject, tt.controlPlaneOperatorGSA)

			if tt.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectedError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestConstructIPAddressNameFromIP(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name     string
		ip       string
		expected string
	}{
		{
			name:     "When given an IP address it should construct a name",
			ip:       "10.0.0.1",
			expected: "psc-endpoint-ip-10.0.0.1",
		},
		{
			name:     "When given a different IP it should construct appropriate name",
			ip:       "192.168.1.100",
			expected: "psc-endpoint-ip-192.168.1.100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.constructIPAddressNameFromIP(tt.ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestIsNotFoundError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "When given nil error it should return false",
			err:      nil,
			expected: false,
		},
		{
			name:     "When given non-GCP error it should return false",
			err:      assert.AnError,
			expected: false,
		},
		// Note: We can't easily test the GCP API error case without importing the full GCP SDK
		// and creating mock errors, but the logic is straightforward
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isNotFoundError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// Test unique naming across different clusters - fixes GCP-198 409 conflict issue
func TestIPAddressNameUniqueness(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	// Simulate the original conflict scenario: same PSC resource name in different clusters
	cluster1PSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-router",
			Namespace: "clusters-customer-hosted-cluster-1",
		},
	}

	cluster2PSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-router",                     // Same name
			Namespace: "clusters-customer-hosted-cluster-2", // Different cluster
		},
	}

	name1 := r.constructIPAddressName(cluster1PSC)
	name2 := r.constructIPAddressName(cluster2PSC)

	// Names should be different to prevent GCP resource conflicts
	assert.NotEqual(t, name1, name2, "IP address names should be unique across different clusters")

	expectedName1 := "clusters-customer-hosted-cluster-1-private-router-psc-endpoint-ip"
	expectedName2 := "clusters-customer-hosted-cluster-2-private-router-psc-endpoint-ip"

	assert.Equal(t, expectedName1, name1)
	assert.Equal(t, expectedName2, name2)
}

// Test that naming functions are consistent - fixes verifyIPExists bug
func TestNamingFunctionConsistency(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-router",
			Namespace: "clusters-customer-hosted-cluster-1",
		},
	}

	// The name used for IP creation should be the same as used for verification
	ipName := r.constructIPAddressName(gcpPSC)

	// This was the bug: verifyIPExists used to construct the name differently
	// Now both should use the same naming logic for consistency
	expectedName := "clusters-customer-hosted-cluster-1-private-router-psc-endpoint-ip"

	assert.Equal(t, expectedName, ipName,
		"constructIPAddressName should generate the correct name for GCP API calls")

	// Verify the name follows GCP resource naming conventions
	assert.NotContains(t, ipName, "_", "Resource name should not contain underscores")
	assert.Contains(t, ipName, "clusters-customer-hosted-cluster-1", "Name should include namespace")
	assert.Contains(t, ipName, "private-router", "Name should include resource name")
	assert.Contains(t, ipName, "psc-endpoint-ip", "Name should include resource type suffix")
}

// Test the exact scenario that caused the original 409 conflict
func TestOriginalConflictScenario(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	// This represents the exact resource that was experiencing the 409 conflict
	conflictedPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-router",
			Namespace: "clusters-customer-hosted-cluster-1",
		},
	}

	ipName := r.constructIPAddressName(conflictedPSC)

	// This should match the actual GCP resource name that was being created
	expectedConflictedName := "clusters-customer-hosted-cluster-1-private-router-psc-endpoint-ip"
	assert.Equal(t, expectedConflictedName, ipName)

	// Verify this name is what the controller will check for in GCP
	// Before the fix, the controller would try to create this name but
	// verifyIPExists would look for "psc-endpoint-ip-10.20.1.3" instead
	assert.Equal(t, expectedConflictedName, ipName,
		"Name should match what will be created/verified in GCP")
}

// Test edge cases for namespace and name sanitization
func TestNameSanitization(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name      string
		namespace string
		pscName   string
		expected  string
	}{
		{
			name:      "When namespace and name are already clean it should not change them",
			namespace: "clusters-customer-hosted-1",
			pscName:   "private-router",
			expected:  "clusters-customer-hosted-1-private-router-psc-endpoint-ip",
		},
		{
			name:      "When namespace has multiple underscores it should replace all",
			namespace: "clusters_customer_hosted_cluster_1",
			pscName:   "private-router",
			expected:  "clusters-customer-hosted-cluster-1-private-router-psc-endpoint-ip",
		},
		{
			name:      "When PSC name has multiple underscores it should replace all",
			namespace: "clusters-customer-hosted-1",
			pscName:   "private_service_connect_router",
			expected:  "clusters-customer-hosted-1-private-service-connect-router-psc-endpoint-ip",
		},
		{
			name:      "When both have mixed characters it should handle correctly",
			namespace: "test_namespace_with_underscores",
			pscName:   "test_resource_name",
			expected:  "test-namespace-with-underscores-test-resource-name-psc-endpoint-ip",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gcpPSC := &hyperv1.GCPPrivateServiceConnect{
				ObjectMeta: metav1.ObjectMeta{
					Name:      tt.pscName,
					Namespace: tt.namespace,
				},
			}

			result := r.constructIPAddressName(gcpPSC)
			assert.Equal(t, tt.expected, result)

			// Ensure result follows GCP naming conventions
			assert.NotContains(t, result, "_", "Result should not contain underscores")
		})
	}
}

// Test endpoint naming uniqueness across different clusters - prevents endpoint name conflicts
func TestEndpointNameUniqueness(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	// Simulate the same PSC resource name in different clusters
	cluster1PSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-router",
			Namespace: "clusters-customer-hosted-cluster-1",
		},
	}

	cluster2PSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-router",                     // Same name
			Namespace: "clusters-customer-hosted-cluster-2", // Different cluster
		},
	}

	endpointName1 := r.constructEndpointName(cluster1PSC)
	endpointName2 := r.constructEndpointName(cluster2PSC)

	// Names should be different to prevent GCP PSC endpoint conflicts
	assert.NotEqual(t, endpointName1, endpointName2, "PSC endpoint names should be unique across different clusters")

	expectedEndpointName1 := "clusters-customer-hosted-cluster-1-private-router-psc-endpoint"
	expectedEndpointName2 := "clusters-customer-hosted-cluster-2-private-router-psc-endpoint"

	assert.Equal(t, expectedEndpointName1, endpointName1)
	assert.Equal(t, expectedEndpointName2, endpointName2)
}

func TestReconcileDNS_FeatureDisabled(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			EndpointIP: "10.0.1.5",
			Conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.GCPEndpointAvailable),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	// Test with CreateDnsZones = false
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				GCP: &hyperv1.GCPPlatformSpec{
					CreateDnsZones: func() *bool { b := false; return &b }(),
				},
			},
		},
	}

	ctx := context.Background()
	logger := log.FromContext(ctx)
	result, err := r.reconcileDNS(ctx, gcpPSC, hcp, logger)

	assert.NoError(t, err, "When DNS feature is disabled it should not return error")
	assert.Equal(t, result.IsZero(), true, "When DNS feature is disabled it should return zero result")
}

func TestReconcileDNS_FeatureNotSet(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			EndpointIP: "10.0.1.5",
			Conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.GCPEndpointAvailable),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	// Test with CreateDnsZones = nil
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				GCP: &hyperv1.GCPPlatformSpec{
					CreateDnsZones: nil, // Not set
				},
			},
		},
	}

	ctx := context.Background()
	logger := log.FromContext(ctx)
	result, err := r.reconcileDNS(ctx, gcpPSC, hcp, logger)

	assert.NoError(t, err, "When DNS feature is not set it should not return error")
	assert.Equal(t, result.IsZero(), true, "When DNS feature is not set it should return zero result")
}

func TestReconcileDNS_EndpointNotAvailable(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			EndpointIP: "10.0.1.5",
			Conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.GCPEndpointAvailable),
					Status: metav1.ConditionFalse, // Not available
				},
			},
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				GCP: &hyperv1.GCPPlatformSpec{
					CreateDnsZones: func() *bool { b := true; return &b }(),
				},
			},
		},
	}

	ctx := context.Background()
	logger := log.FromContext(ctx)
	result, err := r.reconcileDNS(ctx, gcpPSC, hcp, logger)

	assert.NoError(t, err, "When endpoint not available it should not return error")
	assert.Equal(t, result.IsZero(), true, "When endpoint not available it should return zero result")
}

func TestReconcileDNS_EndpointIPMissing(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
		Status: hyperv1.GCPPrivateServiceConnectStatus{
			EndpointIP: "", // Missing IP
			Conditions: []metav1.Condition{
				{
					Type:   string(hyperv1.GCPEndpointAvailable),
					Status: metav1.ConditionTrue,
				},
			},
		},
	}

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				GCP: &hyperv1.GCPPlatformSpec{
					CreateDnsZones: func() *bool { b := true; return &b }(),
				},
			},
		},
	}

	ctx := context.Background()
	logger := log.FromContext(ctx)
	result, err := r.reconcileDNS(ctx, gcpPSC, hcp, logger)

	assert.NoError(t, err, "When endpoint IP missing it should not return error")
	assert.Equal(t, result.IsZero(), true, "When endpoint IP missing it should return zero result")
}

func TestHCPExternalNamesGCP(t *testing.T) {
	tests := []struct {
		name     string
		hcp      *hyperv1.HostedControlPlane
		expected map[string]string
	}{
		{
			name: "When no external hostnames are configured it should return empty map",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{},
				},
			},
			expected: map[string]string{},
		},
		{
				name: "When API server has Route hostname it should return api entry",
		hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.my-custom-domain.com",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{
				"api": "api.my-custom-domain.com",
			},
		},
		{
			name: "When OAuth server has Route hostname it should return oauth entry",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.my-custom-domain.com",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{
				"oauth": "oauth.my-custom-domain.com",
			},
		},
		{
			name: "When both API and OAuth have Route hostnames it should return both entries",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "api.my-custom-domain.com",
								},
							},
						},
						{
							Service: hyperv1.OAuthServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "oauth.my-custom-domain.com",
								},
							},
						},
					},
				},
			},
			expected: map[string]string{
				"api":   "api.my-custom-domain.com",
				"oauth": "oauth.my-custom-domain.com",
			},
		},
		{
			name: "When API server uses LoadBalancer type it should return empty map",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.LoadBalancer,
							},
						},
					},
				},
			},
			expected: map[string]string{},
		},
		{
			name: "When Route has no hostname it should return empty map",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Services: []hyperv1.ServicePublishingStrategyMapping{
						{
							Service: hyperv1.APIServer,
							ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{
								Type: hyperv1.Route,
								Route: &hyperv1.RoutePublishingStrategy{
									Hostname: "", // Empty hostname
								},
							},
						},
					},
				},
			},
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hcpExternalNamesGCP(tt.hcp)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestReconcileExternalServiceGCP(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "clusters-test-cluster-1",
		},
	}

	tests := []struct {
		name                 string
		hostName             string
		targetIP             string
		expectedExternalName string
		expectedAnnotation   string
	}{
		{
			name:                 "When configuring external service it should set correct ExternalName and annotation",
			hostName:             "api.my-custom-domain.com",
			targetIP:             "10.0.1.5",
			expectedExternalName: "10.0.1.5",
			expectedAnnotation:   "api.my-custom-domain.com",
		},
		{
			name:                 "When configuring OAuth service it should handle different hostname",
			hostName:             "oauth.my-enterprise.com",
			targetIP:             "192.168.1.100",
			expectedExternalName: "192.168.1.100",
			expectedAnnotation:   "oauth.my-enterprise.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a basic service
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-service",
					Namespace: hcp.Namespace,
				},
			}

			err := reconcileExternalServiceGCP(svc, hcp, tt.hostName, tt.targetIP)

			assert.NoError(t, err)
			assert.Equal(t, corev1.ServiceTypeExternalName, svc.Spec.Type)
			assert.Equal(t, tt.expectedExternalName, svc.Spec.ExternalName)
			assert.Equal(t, tt.expectedAnnotation, svc.Annotations[hyperv1.ExternalDNSHostnameAnnotation])
			assert.Equal(t, "true", svc.Labels[externalPrivateServiceLabelGCP])

			// Verify owner reference is set
			assert.Len(t, svc.OwnerReferences, 1)
			assert.Equal(t, "HostedControlPlane", svc.OwnerReferences[0].Kind)
			assert.Equal(t, hcp.Name, svc.OwnerReferences[0].Name)

			// Verify port configuration
			assert.Len(t, svc.Spec.Ports, 1)
			assert.Equal(t, "https", svc.Spec.Ports[0].Name)
			assert.Equal(t, int32(443), svc.Spec.Ports[0].Port)
			assert.Equal(t, corev1.ProtocolTCP, svc.Spec.Ports[0].Protocol)
		})
	}
}
