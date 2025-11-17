package hostedcluster

import (
	"context"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/networkpolicy"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/blang/semver"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestReconcileNetworkPolicies_PrivateRouter_PlatformSupport(t *testing.T) {
	testCases := []struct {
		name                string
		platform            hyperv1.PlatformType
		expectPrivateRouter bool
	}{
		{
			name:                "AWS platform should create private-router NetworkPolicy",
			platform:            hyperv1.AWSPlatform,
			expectPrivateRouter: true,
		},
		{
			name:                "Azure platform should create private-router NetworkPolicy",
			platform:            hyperv1.AzurePlatform,
			expectPrivateRouter: true,
		},
		{
			name:                "GCP platform should create private-router NetworkPolicy",
			platform:            hyperv1.GCPPlatform,
			expectPrivateRouter: true,
		},
		{
			name:                "IBMCloud platform should not create private-router NetworkPolicy",
			platform:            hyperv1.IBMCloudPlatform,
			expectPrivateRouter: false,
		},
		{
			name:                "KubeVirt platform should not create private-router NetworkPolicy",
			platform:            hyperv1.KubevirtPlatform,
			expectPrivateRouter: false,
		},
		{
			name:                "PowerVS platform should not create private-router NetworkPolicy",
			platform:            hyperv1.PowerVSPlatform,
			expectPrivateRouter: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create test objects
			hcluster := &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platform,
					},
				},
			}

			// Initialize platform-specific fields to avoid nil pointer dereference
			if tc.platform == hyperv1.KubevirtPlatform {
				hcluster.Spec.Platform.Kubevirt = &hyperv1.KubevirtPlatformSpec{
					Credentials: nil, // This will trigger the condition that creates NetworkPolicy
				}
			}

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: manifests.HostedControlPlaneNamespace(hcluster.Namespace, hcluster.Name),
				},
			}

			// Create fake Kubernetes endpoints for testing
			kubernetesEndpoint := &corev1.Endpoints{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "kubernetes",
					Namespace: "default",
				},
				Subsets: []corev1.EndpointSubset{
					{
						Addresses: []corev1.EndpointAddress{
							{IP: "10.0.0.1"},
						},
					},
				},
			}

			// Create fake management cluster network
			managementClusterNetwork := &configv1.Network{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: configv1.NetworkSpec{
					ClusterNetwork: []configv1.ClusterNetworkEntry{
						{CIDR: "10.128.0.0/14"},
					},
					ServiceNetwork: []string{"172.30.0.0/16"},
				},
			}

			// Create fake client with test objects
			scheme := runtime.NewScheme()
			hyperv1.AddToScheme(scheme)
			corev1.AddToScheme(scheme)
			configv1.AddToScheme(scheme)
			networkingv1.AddToScheme(scheme)

			objs := []client.Object{kubernetesEndpoint, managementClusterNetwork}
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()

			// Create reconciler
			reconciler := &HostedClusterReconciler{
				Client:                        fakeClient,
				ManagementClusterCapabilities: fakecapabilities.NewSupportAllExcept(),
			}

			// Create mock createOrUpdate function that tracks created objects
			createdNetworkPolicies := make(map[string]*networkingv1.NetworkPolicy)
			createOrUpdate := upsert.CreateOrUpdateFN(func(ctx context.Context, client client.Client, obj client.Object, f controllerutil.MutateFn) (controllerutil.OperationResult, error) {
				if netPol, ok := obj.(*networkingv1.NetworkPolicy); ok {
					// Apply the mutation function to populate the policy
					if err := f(); err != nil {
						return controllerutil.OperationResultNone, err
					}
					// Store the policy for verification
					createdNetworkPolicies[netPol.Name] = netPol
				}
				return controllerutil.OperationResultCreated, nil
			})

			// Test version
			version := semver.MustParse("4.15.0")

			// Call the function under test
			ctx := context.Background()
			log := ctrl.Log.WithName("test")
			err := reconciler.reconcileNetworkPolicies(ctx, log, createOrUpdate, hcluster, hcp, version, false)

			// Verify no error occurred
			if err != nil {
				t.Fatalf("reconcileNetworkPolicies failed: %v", err)
			}

			// Verify private-router NetworkPolicy creation based on platform
			privateRouterPolicy, exists := createdNetworkPolicies["private-router"]

			if tc.expectPrivateRouter {
				if !exists {
					t.Errorf("Expected private-router NetworkPolicy to be created for platform %s, but it was not", tc.platform)
				} else {
					// Verify the policy has the correct configuration
					verifyPrivateRouterNetworkPolicy(t, privateRouterPolicy)
				}
			} else {
				if exists {
					t.Errorf("Expected private-router NetworkPolicy to NOT be created for platform %s, but it was", tc.platform)
				}
			}

			// Verify that other common policies are always created
			expectedPolicies := []string{"openshift-ingress", "same-namespace", "kas", "openshift-monitoring"}
			for _, policyName := range expectedPolicies {
				if _, exists := createdNetworkPolicies[policyName]; !exists {
					t.Errorf("Expected %s NetworkPolicy to be created, but it was not", policyName)
				}
			}
		})
	}
}

// verifyPrivateRouterNetworkPolicy verifies that the private-router NetworkPolicy has the correct configuration
func verifyPrivateRouterNetworkPolicy(t *testing.T, policy *networkingv1.NetworkPolicy) {
	// Verify policy types
	expectedTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}
	if len(policy.Spec.PolicyTypes) != len(expectedTypes) {
		t.Errorf("Expected %d policy types, got %d", len(expectedTypes), len(policy.Spec.PolicyTypes))
	}
	for i, expectedType := range expectedTypes {
		if i >= len(policy.Spec.PolicyTypes) || policy.Spec.PolicyTypes[i] != expectedType {
			t.Errorf("Expected policy type %s at index %d, got %s", expectedType, i, policy.Spec.PolicyTypes[i])
		}
	}

	// Verify pod selector
	expectedLabels := map[string]string{"app": "private-router"}
	if len(policy.Spec.PodSelector.MatchLabels) != len(expectedLabels) {
		t.Errorf("Expected %d pod selector labels, got %d", len(expectedLabels), len(policy.Spec.PodSelector.MatchLabels))
	}
	for key, expectedValue := range expectedLabels {
		if actualValue, exists := policy.Spec.PodSelector.MatchLabels[key]; !exists || actualValue != expectedValue {
			t.Errorf("Expected pod selector label %s=%s, got %s=%s", key, expectedValue, key, actualValue)
		}
	}

	// Verify ingress rules
	if len(policy.Spec.Ingress) == 0 {
		t.Error("Expected at least one ingress rule")
	} else {
		ingressRule := policy.Spec.Ingress[0]
		if len(ingressRule.Ports) != 2 {
			t.Errorf("Expected 2 ingress ports, got %d", len(ingressRule.Ports))
		} else {
			// Check for ports 8080 and 8443
			expectedPorts := []int32{8080, 8443}
			actualPorts := make([]int32, len(ingressRule.Ports))
			for i, port := range ingressRule.Ports {
				if port.Port != nil {
					actualPorts[i] = port.Port.IntVal
				}
			}
			for _, expectedPort := range expectedPorts {
				found := false
				for _, actualPort := range actualPorts {
					if actualPort == expectedPort {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected ingress port %d not found", expectedPort)
				}
			}
		}
	}

	// Verify egress rules exist (detailed verification would require more complex setup)
	if len(policy.Spec.Egress) == 0 {
		t.Error("Expected at least one egress rule")
	}
}

func TestReconcilePrivateRouterNetworkPolicy_IngressOnly(t *testing.T) {
	// Test the ingressOnly parameter functionality
	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
		},
	}

	kubernetesEndpoint := &corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	policy := networkpolicy.PrivateRouterNetworkPolicy("test-namespace")

	// Test with ingressOnly = true
	err := reconcilePrivateRouterNetworkPolicy(policy, hcluster, kubernetesEndpoint, false, nil, true)
	if err != nil {
		t.Fatalf("reconcilePrivateRouterNetworkPolicy with ingressOnly=true failed: %v", err)
	}

	// Verify only ingress policy type is set
	if len(policy.Spec.PolicyTypes) != 1 || policy.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Error("Expected only Ingress policy type when ingressOnly=true")
	}

	// Verify no egress rules
	if len(policy.Spec.Egress) != 0 {
		t.Error("Expected no egress rules when ingressOnly=true")
	}

	// Test with ingressOnly = false
	policy = networkpolicy.PrivateRouterNetworkPolicy("test-namespace")
	err = reconcilePrivateRouterNetworkPolicy(policy, hcluster, kubernetesEndpoint, false, nil, false)
	if err != nil {
		t.Fatalf("reconcilePrivateRouterNetworkPolicy with ingressOnly=false failed: %v", err)
	}

	// Verify both ingress and egress policy types are set
	expectedTypes := []networkingv1.PolicyType{networkingv1.PolicyTypeIngress, networkingv1.PolicyTypeEgress}
	if len(policy.Spec.PolicyTypes) != 2 {
		t.Errorf("Expected 2 policy types, got %d", len(policy.Spec.PolicyTypes))
	}
	for _, expectedType := range expectedTypes {
		found := false
		for _, actualType := range policy.Spec.PolicyTypes {
			if actualType == expectedType {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected policy type %s not found", expectedType)
		}
	}

	// Verify egress rules exist
	if len(policy.Spec.Egress) == 0 {
		t.Error("Expected egress rules when ingressOnly=false")
	}
}

func TestReconcilePrivateRouterNetworkPolicy_Ports(t *testing.T) {
	// Test that the correct ports are configured
	hcluster := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.GCPPlatform, // Test specifically with GCP
			},
		},
	}

	kubernetesEndpoint := &corev1.Endpoints{
		Subsets: []corev1.EndpointSubset{
			{
				Addresses: []corev1.EndpointAddress{
					{IP: "10.0.0.1"},
				},
			},
		},
	}

	policy := networkpolicy.PrivateRouterNetworkPolicy("test-namespace")
	err := reconcilePrivateRouterNetworkPolicy(policy, hcluster, kubernetesEndpoint, false, nil, false)
	if err != nil {
		t.Fatalf("reconcilePrivateRouterNetworkPolicy failed: %v", err)
	}

	// Verify ingress ports
	if len(policy.Spec.Ingress) == 0 {
		t.Fatal("Expected at least one ingress rule")
	}

	ingressRule := policy.Spec.Ingress[0]
	if len(ingressRule.Ports) != 2 {
		t.Fatalf("Expected 2 ingress ports, got %d", len(ingressRule.Ports))
	}

	// Verify specific ports: 8080 (HTTP) and 8443 (HTTPS)
	expectedPorts := map[int32]string{
		8080: "HTTP",
		8443: "HTTPS",
	}

	for _, port := range ingressRule.Ports {
		if port.Port == nil {
			t.Error("Port should not be nil")
			continue
		}

		portValue := port.Port.IntVal
		if _, expected := expectedPorts[portValue]; !expected {
			t.Errorf("Unexpected port %d in ingress rules", portValue)
		}

		// Verify protocol is TCP
		if port.Protocol == nil || *port.Protocol != corev1.ProtocolTCP {
			t.Errorf("Expected TCP protocol for port %d", portValue)
		}
	}

	// Verify pod selector targets private-router app
	if policy.Spec.PodSelector.MatchLabels["app"] != "private-router" {
		t.Error("Expected pod selector to target app=private-router")
	}
}
