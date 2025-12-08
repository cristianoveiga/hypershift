package external_cert

import (
	"fmt"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

func adaptCertificate(cpContext component.WorkloadContext, m *component.ManifestAdapterInput) error {
	hcp := cpContext.HCP

	// Extract the domain from the API server route hostname
	// API hostname format: api.{cluster-domain}
	// We want wildcard: *.{cluster-domain}
	apiHostname := getAPIServerHostname(hcp)
	if apiHostname == "" {
		return fmt.Errorf("API server hostname not found in service publishing strategy")
	}

	// Strip the "api." prefix to get the base cluster domain
	clusterDomain := strings.TrimPrefix(apiHostname, "api.")
	if clusterDomain == apiHostname {
		return fmt.Errorf("unexpected API hostname format: %s (expected api.* prefix)", apiHostname)
	}

	// Generate wildcard domain for the certificate
	wildcardDomain := fmt.Sprintf("*.%s", clusterDomain)

	// Set template parameters
	m.Params = map[string]interface{}{
		"Namespace":      hcp.Namespace,
		"WildcardDomain": wildcardDomain,
		"SecretName":     "external-api-cert",
	}

	return nil
}

// getAPIServerHostname extracts the API server hostname from the HCP service publishing strategy
func getAPIServerHostname(hcp *hyperv1.HostedControlPlane) string {
	for _, svc := range hcp.Spec.Services {
		if svc.Service == hyperv1.APIServer {
			if svc.ServicePublishingStrategy.Route != nil {
				return svc.ServicePublishingStrategy.Route.Hostname
			}
		}
	}
	return ""
}
