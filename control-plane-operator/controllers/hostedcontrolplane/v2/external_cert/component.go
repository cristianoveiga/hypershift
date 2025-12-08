package external_cert

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
)

const (
	ComponentName = "external-cert"
)

var _ component.ComponentOptions = &externalCert{}

type externalCert struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (e *externalCert) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (e *externalCert) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (e *externalCert) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewGenericComponent(ComponentName, &externalCert{}).
		WithPredicate(isGCPPlatform).
		WithManifestAdapter(
			"certificate.yaml",
			component.WithAdaptFunction(adaptCertificate),
		).
		Build()
}

// isGCPPlatform returns true if the platform is GCP.
// Let's Encrypt certificates are created for all GCP HCP clusters (public and private)
// because even private clusters have DNS records in public zones for certificate validation.
func isGCPPlatform(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.GCPPlatform, nil
}
