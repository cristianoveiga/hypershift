package provideridcontroller

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
)

const (
	ComponentName = "providerid-controller"
)

var _ component.ComponentOptions = &providerIDController{}

type providerIDController struct{}

// IsRequestServing implements controlplanecomponent.ComponentOptions.
func (p *providerIDController) IsRequestServing() bool {
	return false
}

// MultiZoneSpread implements controlplanecomponent.ComponentOptions.
func (p *providerIDController) MultiZoneSpread() bool {
	return false
}

// NeedsManagementKASAccess implements controlplanecomponent.ComponentOptions.
func (p *providerIDController) NeedsManagementKASAccess() bool {
	return false
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(ComponentName, &providerIDController{}).
		WithPredicate(predicate).
		WithAdaptFunction(adaptDeployment).
		InjectTokenMinterContainer(component.TokenMinterContainerOptions{
			TokenType:               component.CloudToken,
			ServiceAccountNameSpace: "kube-system",
			ServiceAccountName:      "control-plane-operator",
		}).
		Build()
}

func predicate(cpContext component.WorkloadContext) (bool, error) {
	return cpContext.HCP.Spec.Platform.Type == hyperv1.GCPPlatform, nil
}

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP
	if hcp.Spec.Platform.GCP == nil {
		return fmt.Errorf("GCP platform spec is nil")
	}

	gcpPlatform := hcp.Spec.Platform.GCP

	// Get the control-plane-operator image (same as token-minter)
	// The hypershift binary is built into the same image
	image := cpContext.ReleaseImageProvider.GetImage("token-minter")

	// Set image and environment variables
	for i := range deployment.Spec.Template.Spec.Containers {
		if deployment.Spec.Template.Spec.Containers[i].Name == "providerid-controller" {
			// Set the image to the control-plane-operator image
			deployment.Spec.Template.Spec.Containers[i].Image = image

			// Set GOOGLE_CLOUD_PROJECT and GCP_REGION environment variables
			for j := range deployment.Spec.Template.Spec.Containers[i].Env {
				env := &deployment.Spec.Template.Spec.Containers[i].Env[j]
				switch env.Name {
				case "GOOGLE_CLOUD_PROJECT":
					env.Value = gcpPlatform.Project
				case "GCP_REGION":
					env.Value = gcpPlatform.Region
				}
			}
			break
		}
	}

	return nil
}
