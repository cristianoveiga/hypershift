package gcp

import (
	"encoding/json"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestConfig(t *testing.T) {
	hcp := newTestHCP()
	hcp.Namespace = "HCP_NAMESPACE"

	cm := &corev1.ConfigMap{}
	_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cpContext := component.WorkloadContext{
		HCP: hcp,
	}
	err = adaptConfig(cpContext, cm)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	yaml, err := util.SerializeResource(cm, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, yaml)
}

func TestCredentialsSecret(t *testing.T) {
	hcp := newTestHCP()

	secret := &corev1.Secret{}
	_, _, err := assets.LoadManifestInto(ComponentName, "credentials-secret.yaml", secret)
	if err != nil {
		t.Fatalf("unexpected error loading manifest: %v", err)
	}

	cpContext := component.WorkloadContext{
		HCP: hcp,
	}
	err = adaptCredentialsSecret(cpContext, secret)
	if err != nil {
		t.Fatalf("unexpected error adapting credentials secret: %v", err)
	}

	// Verify the credential configuration was generated correctly
	credJSON, ok := secret.Data[credentialsKey]
	if !ok {
		t.Fatalf("expected credentials.json key in secret data")
	}

	var cred gcpExternalAccountCredential
	if err := json.Unmarshal(credJSON, &cred); err != nil {
		t.Fatalf("failed to unmarshal credential JSON: %v", err)
	}

	// Verify expected values
	if cred.Type != "external_account" {
		t.Errorf("expected type 'external_account', got '%s'", cred.Type)
	}

	expectedAudience := "//iam.googleapis.com/projects/123456789012/locations/global/workloadIdentityPools/my-pool/providers/my-provider"
	if cred.Audience != expectedAudience {
		t.Errorf("expected audience '%s', got '%s'", expectedAudience, cred.Audience)
	}

	expectedImpersonationURL := "https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/cloud-controller@my-project.iam.gserviceaccount.com:generateAccessToken"
	if cred.ServiceAccountImpersonationURL != expectedImpersonationURL {
		t.Errorf("expected impersonation URL '%s', got '%s'", expectedImpersonationURL, cred.ServiceAccountImpersonationURL)
	}

	if cred.CredentialSource.File != tokenFilePath {
		t.Errorf("expected credential source file '%s', got '%s'", tokenFilePath, cred.CredentialSource.File)
	}

	yaml, err := util.SerializeResource(secret, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, yaml)
}

func TestConfigErrorStates(t *testing.T) {
	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectedErr string
	}{
		{
			name: "nil GCP platform configuration",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						GCP: nil,
					},
				},
			},
			expectedErr: "GCP platform configuration is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &corev1.ConfigMap{}
			_, _, err := assets.LoadManifestInto(ComponentName, "config.yaml", cm)
			if err != nil {
				t.Fatalf("failed to load manifest: %v", err)
			}
			cpContext := component.WorkloadContext{
				HCP: tt.hcp,
			}
			err = adaptConfig(cpContext, cm)
			if err == nil {
				t.Fatalf("expected error but got none")
			}
			if tt.expectedErr != "" && err.Error() != tt.expectedErr {
				t.Fatalf("expected error '%s', but got: %v", tt.expectedErr, err)
			}
		})
	}
}

func TestCredentialsSecretErrorStates(t *testing.T) {
	tests := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectedErr string
	}{
		{
			name: "nil GCP platform configuration",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						GCP: nil,
					},
				},
			},
			expectedErr: "GCP platform configuration is nil",
		},
		{
			name: "missing cloud controller service account email",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						GCP: &hyperv1.GCPPlatformSpec{
							Project: "my-project",
							Region:  "us-central1",
							WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
								ProjectNumber: "123456789012",
								PoolID:        "my-pool",
								ProviderID:    "my-provider",
								ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
									NodePool:        "nodepool@my-project.iam.gserviceaccount.com",
									ControlPlane:    "controlplane@my-project.iam.gserviceaccount.com",
									CloudController: "", // Empty service account
								},
							},
						},
					},
				},
			},
			expectedErr: "CloudController service account email is not configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret := &corev1.Secret{}
			_, _, err := assets.LoadManifestInto(ComponentName, "credentials-secret.yaml", secret)
			if err != nil {
				t.Fatalf("failed to load manifest: %v", err)
			}
			cpContext := component.WorkloadContext{
				HCP: tt.hcp,
			}
			err = adaptCredentialsSecret(cpContext, secret)
			if err == nil {
				t.Fatalf("expected error but got none")
			}
			if tt.expectedErr != "" && err.Error() != tt.expectedErr {
				t.Fatalf("expected error '%s', but got: %v", tt.expectedErr, err)
			}
		})
	}
}

func TestAdaptDeployment(t *testing.T) {
	tests := []struct {
		name          string
		annotations   map[string]string
		expectedImage string
	}{
		{
			name:          "no image override",
			annotations:   map[string]string{},
			expectedImage: "original-image:latest",
		},
		{
			name: "with image override annotation",
			annotations: map[string]string{
				GCPCCMImageAnnotation: "custom-registry.io/gcp-ccm:v1.0.0",
			},
			expectedImage: "custom-registry.io/gcp-ccm:v1.0.0",
		},
		{
			name: "empty image override annotation",
			annotations: map[string]string{
				GCPCCMImageAnnotation: "",
			},
			expectedImage: "original-image:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := newTestHCP()
			hcp.Annotations = tt.annotations

			deployment := &appsv1.Deployment{
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Name:  "cloud-controller-manager",
									Image: "original-image:latest",
								},
							},
						},
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			err := adaptDeployment(cpContext, deployment)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			actualImage := deployment.Spec.Template.Spec.Containers[0].Image
			if actualImage != tt.expectedImage {
				t.Errorf("expected image '%s', got '%s'", tt.expectedImage, actualImage)
			}
		})
	}
}

func TestPredicate(t *testing.T) {
	tests := []struct {
		name           string
		platformType   hyperv1.PlatformType
		expectedResult bool
	}{
		{
			name:           "GCP platform returns true",
			platformType:   hyperv1.GCPPlatform,
			expectedResult: true,
		},
		{
			name:           "AWS platform returns false",
			platformType:   hyperv1.AWSPlatform,
			expectedResult: false,
		},
		{
			name:           "Azure platform returns false",
			platformType:   hyperv1.AzurePlatform,
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hcp := &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tt.platformType,
					},
				},
			}

			cpContext := component.WorkloadContext{
				HCP: hcp,
			}

			result, err := predicate(cpContext)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result != tt.expectedResult {
				t.Errorf("expected predicate to return %v for platform %s, got %v",
					tt.expectedResult, tt.platformType, result)
			}
		})
	}
}

// newTestHCP creates a HostedControlPlane with default GCP configuration for testing.
func newTestHCP() *hyperv1.HostedControlPlane {
	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   "test-namespace",
			Annotations: map[string]string{},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform,
				GCP: &hyperv1.GCPPlatformSpec{
					Project: "my-project",
					Region:  "us-central1",
					NetworkConfig: hyperv1.GCPNetworkConfig{
						Network: hyperv1.GCPResourceReference{
							Name: "my-network",
						},
					},
					WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
						ProjectNumber: "123456789012",
						PoolID:        "my-pool",
						ProviderID:    "my-provider",
						ServiceAccountsEmails: hyperv1.GCPServiceAccountsEmails{
							NodePool:        "nodepool@my-project.iam.gserviceaccount.com",
							ControlPlane:    "controlplane@my-project.iam.gserviceaccount.com",
							CloudController: "cloud-controller@my-project.iam.gserviceaccount.com",
						},
					},
				},
			},
			InfraID: "my-infra-ID",
		},
	}
}
