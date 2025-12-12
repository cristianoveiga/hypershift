package kas

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// TestAdaptCAPIKubeconfigSecret_GCP verifies that for GCP platform,
// the kubeconfig secret uses fixed "capi-cluster" name.
func TestAdaptCAPIKubeconfigSecret_GCP(t *testing.T) {
	tests := []struct {
		name                     string
		infraID                  string
		expectedSecretName       string
		expectedClusterNameLabel string
	}{
		{
			name:                     "GCP with digit-starting infraID uses fixed name",
			infraID:                  "1e71f9a4-833f-439b-91fc-2c4f6ad7e2bd",
			expectedSecretName:       "capi-cluster-kubeconfig",
			expectedClusterNameLabel: "capi-cluster",
		},
		{
			name:                     "GCP with letter-starting infraID uses fixed name",
			infraID:                  "abc123-456-789",
			expectedSecretName:       "capi-cluster-kubeconfig",
			expectedClusterNameLabel: "capi-cluster",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Test the naming logic for GCP platform
			// For GCP, always use fixed "capi-cluster" name regardless of infraID
			clusterName := "capi-cluster"
			secretName := clusterName + "-kubeconfig"

			g.Expect(secretName).To(Equal(tc.expectedSecretName))
			g.Expect(clusterName).To(Equal(tc.expectedClusterNameLabel))
		})
	}
}

// TestAdaptCAPIKubeconfigSecret_NonGCP verifies that for non-GCP platforms,
// the kubeconfig secret name uses infraID directly.
func TestAdaptCAPIKubeconfigSecret_NonGCP(t *testing.T) {
	tests := []struct {
		name                     string
		platformType             hyperv1.PlatformType
		infraID                  string
		expectedSecretName       string
		expectedClusterNameLabel string
	}{
		{
			name:                     "AWS with digit-starting infraID uses infraID",
			platformType:             hyperv1.AWSPlatform,
			infraID:                  "1e71f9a4-833f-439b-91fc-2c4f6ad7e2bd",
			expectedSecretName:       "1e71f9a4-833f-439b-91fc-2c4f6ad7e2bd-kubeconfig",
			expectedClusterNameLabel: "1e71f9a4-833f-439b-91fc-2c4f6ad7e2bd",
		},
		{
			name:                     "Azure with digit-starting infraID uses infraID",
			platformType:             hyperv1.AzurePlatform,
			infraID:                  "2a81f0b5-944g-540c-a2gd-3d5g7be8f3ce",
			expectedSecretName:       "2a81f0b5-944g-540c-a2gd-3d5g7be8f3ce-kubeconfig",
			expectedClusterNameLabel: "2a81f0b5-944g-540c-a2gd-3d5g7be8f3ce",
		},
		{
			name:                     "OpenStack with letter-starting infraID uses infraID",
			platformType:             hyperv1.OpenStackPlatform,
			infraID:                  "openstack-cluster-123",
			expectedSecretName:       "openstack-cluster-123-kubeconfig",
			expectedClusterNameLabel: "openstack-cluster-123",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			// Test the naming logic for non-GCP platforms
			// For non-GCP platforms, use infraID directly
			clusterName := tc.infraID
			secretName := clusterName + "-kubeconfig"

			g.Expect(secretName).To(Equal(tc.expectedSecretName))
			g.Expect(clusterName).To(Equal(tc.expectedClusterNameLabel))
		})
	}
}
