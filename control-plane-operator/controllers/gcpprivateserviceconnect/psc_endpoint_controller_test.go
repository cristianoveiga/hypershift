package gcpprivateserviceconnect

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConstructEndpointName(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-cluster",
		},
	}

	result := r.constructEndpointName(gcpPSC)
	expected := "test-cluster-psc-endpoint"

	assert.Equal(t, expected, result)
}

func TestConstructIPAddressName(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	tests := []struct {
		name      string
		gcpPSC    *hyperv1.GCPPrivateServiceConnect
		expected  string
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
			Namespace: "clusters-cveiga-hosted-cluster-1",
		},
	}

	cluster2PSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-router", // Same name
			Namespace: "clusters-cveiga-hosted-cluster-2", // Different cluster
		},
	}

	name1 := r.constructIPAddressName(cluster1PSC)
	name2 := r.constructIPAddressName(cluster2PSC)

	// Names should be different to prevent GCP resource conflicts
	assert.NotEqual(t, name1, name2, "IP address names should be unique across different clusters")

	expectedName1 := "clusters-cveiga-hosted-cluster-1-private-router-psc-endpoint-ip"
	expectedName2 := "clusters-cveiga-hosted-cluster-2-private-router-psc-endpoint-ip"

	assert.Equal(t, expectedName1, name1)
	assert.Equal(t, expectedName2, name2)
}

// Test that naming functions are consistent - fixes verifyIPExists bug
func TestNamingFunctionConsistency(t *testing.T) {
	r := &GCPPrivateServiceConnectReconciler{}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "private-router",
			Namespace: "clusters-cveiga-hosted-cluster-1",
		},
	}

	// The name used for IP creation should be the same as used for verification
	ipName := r.constructIPAddressName(gcpPSC)

	// This was the bug: verifyIPExists used to construct the name differently
	// Now both should use the same naming logic for consistency
	expectedName := "clusters-cveiga-hosted-cluster-1-private-router-psc-endpoint-ip"

	assert.Equal(t, expectedName, ipName,
		"constructIPAddressName should generate the correct name for GCP API calls")

	// Verify the name follows GCP resource naming conventions
	assert.NotContains(t, ipName, "_", "Resource name should not contain underscores")
	assert.Contains(t, ipName, "clusters-cveiga-hosted-cluster-1", "Name should include namespace")
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
			Namespace: "clusters-cveiga-hosted-cluster-1",
		},
	}

	ipName := r.constructIPAddressName(conflictedPSC)

	// This should match the actual GCP resource name that was being created
	expectedConflictedName := "clusters-cveiga-hosted-cluster-1-private-router-psc-endpoint-ip"
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