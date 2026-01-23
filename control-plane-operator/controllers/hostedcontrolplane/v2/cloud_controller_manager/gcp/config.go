package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	configKey      = "cloud.conf"
	credentialsKey = "credentials.json"
	tokenFilePath  = "/var/run/secrets/openshift/serviceaccount/token"

	// GCPCCMImageAnnotation allows overriding the GCP CCM image until it's in the release payload
	GCPCCMImageAnnotation = "hypershift.openshift.io/gcp-cloud-controller-manager-image"
)

// gcpCredentialSource represents the credential source configuration for GCP external account credentials.
type gcpCredentialSource struct {
	File   string                    `json:"file"`
	Format gcpCredentialSourceFormat `json:"format"`
}

// gcpCredentialSourceFormat represents the format of the credential source.
type gcpCredentialSourceFormat struct {
	Type string `json:"type"`
}

// gcpExternalAccountCredential represents the complete GCP external account credential configuration
// for Workload Identity Federation. This follows the Google Cloud credential configuration format.
type gcpExternalAccountCredential struct {
	Type                           string              `json:"type"`
	Audience                       string              `json:"audience"`
	SubjectTokenType               string              `json:"subject_token_type"`
	TokenURL                       string              `json:"token_url"`
	ServiceAccountImpersonationURL string              `json:"service_account_impersonation_url"`
	CredentialSource               gcpCredentialSource `json:"credential_source"`
}

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	// Check for image override annotation
	if imageOverride, ok := cpContext.HCP.Annotations[GCPCCMImageAnnotation]; ok && imageOverride != "" {
		for i := range deployment.Spec.Template.Spec.Containers {
			if deployment.Spec.Template.Spec.Containers[i].Name == "cloud-controller-manager" {
				deployment.Spec.Template.Spec.Containers[i].Image = imageOverride
				break
			}
		}
	}

	return nil
}

func adaptConfig(cpContext component.WorkloadContext, cm *corev1.ConfigMap) error {
	gcpPlatform := cpContext.HCP.Spec.Platform.GCP
	if gcpPlatform == nil {
		return fmt.Errorf("GCP platform configuration is nil")
	}

	projectID := gcpPlatform.Project
	networkName := gcpPlatform.NetworkConfig.Network.Name
	subnetworkName := "" // Subnetwork is optional for CCM

	// Node tags are used for firewall rules. The nodepool controller applies
	// the tag "{infraID}-worker" to all worker nodes. GCP network tags must be
	// lowercase, so we apply the same transformation as the nodepool controller.
	nodeTags := strings.ToLower(fmt.Sprintf("%s-worker", cpContext.HCP.Spec.InfraID))

	// Get the config template and populate it
	configTemplate := cm.Data[configKey]
	config := fmt.Sprintf(configTemplate, projectID, networkName, subnetworkName, nodeTags)

	cm.Data[configKey] = config
	return nil
}

func adaptCredentialsSecret(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	gcpPlatform := cpContext.HCP.Spec.Platform.GCP
	if gcpPlatform == nil {
		return fmt.Errorf("GCP platform configuration is nil")
	}

	wif := gcpPlatform.WorkloadIdentity
	serviceAccountEmail := wif.ServiceAccountsEmails.CloudController

	if serviceAccountEmail == "" {
		return fmt.Errorf("CloudController service account email is not configured")
	}

	// Build the WIF credential configuration
	credConfig := gcpExternalAccountCredential{
		Type: "external_account",
		Audience: fmt.Sprintf("//iam.googleapis.com/projects/%s/locations/global/workloadIdentityPools/%s/providers/%s",
			wif.ProjectNumber, wif.PoolID, wif.ProviderID),
		SubjectTokenType:               "urn:ietf:params:oauth:token-type:jwt",
		TokenURL:                       "https://sts.googleapis.com/v1/token",
		ServiceAccountImpersonationURL: fmt.Sprintf("https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/%s:generateAccessToken", serviceAccountEmail),
		CredentialSource: gcpCredentialSource{
			File: tokenFilePath,
			Format: gcpCredentialSourceFormat{
				Type: "text",
			},
		},
	}

	credentialJSON, err := json.Marshal(credConfig)
	if err != nil {
		return fmt.Errorf("failed to marshal GCP credential configuration: %w", err)
	}

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[credentialsKey] = credentialJSON

	return nil
}
