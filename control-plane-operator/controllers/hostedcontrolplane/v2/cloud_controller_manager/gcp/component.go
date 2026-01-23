package gcp

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
)

const (
	ComponentName = "gcp-cloud-controller-manager"
)

var _ component.ComponentOptions = &gcpOptions{}

type gcpOptions struct {
}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (c *gcpOptions) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (c *gcpOptions) MultiZoneSpread() bool {
	return true
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (c *gcpOptions) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &gcpOptions{}).
		WithPredicate(predicate).
		WithManifestAdapter(
			"config.yaml",
			component.WithAdaptFunction(adaptConfig),
		).
		WithManifestAdapter(
			"credentials-secret.yaml",
			component.WithAdaptFunction(adaptCredentialsSecret),
		).
		InjectTokenMinterContainer(component.TokenMinterContainerOptions{
			TokenType:               component.CloudToken,
			ServiceAccountNameSpace: "kube-system",
			ServiceAccountName:      "cloud-controller-manager",
		}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.GCPPlatform, nil
}
