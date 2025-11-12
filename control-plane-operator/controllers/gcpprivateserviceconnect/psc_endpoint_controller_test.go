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

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-cluster",
		},
	}

	result := r.constructIPAddressName(gcpPSC)
	expected := "test-cluster-psc-endpoint-ip"

	assert.Equal(t, expected, result)
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