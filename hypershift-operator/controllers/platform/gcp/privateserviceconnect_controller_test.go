package gcp

import (
	"context"
	"errors"
	"testing"

	"github.com/go-logr/logr/testr"
	"google.golang.org/api/googleapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/upsert"
	supportutil "github.com/openshift/hypershift/support/util"
)

// Note: Mock GCP Compute Service implementations would go here.
// They are currently commented out to avoid lint warnings about unused code.
// In a full test suite, these would be used for comprehensive GCP API mocking.

func TestExtractGCPProjectFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		envValue    string
		expected    string
		expectError bool
	}{
		{
			name:     "project set in env",
			envValue: "my-gcp-project",
			expected: "my-gcp-project",
		},
		{
			name:        "empty env returns error",
			envValue:    "",
			expected:    "",
			expectError: true,
		},
		{
			name:     "custom project",
			envValue: "production-project-123",
			expected: "production-project-123",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.envValue != "" {
				t.Setenv("GCP_PROJECT", test.envValue)
			}

			r := &GCPPrivateServiceConnectReconciler{
				Log: testr.New(t),
			}
			result, err := r.extractGCPProjectFromEnv()

			if test.expectError && err == nil {
				t.Error("expected error but got none")
			}
			if !test.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != test.expected {
				t.Errorf("expected %q, got %q", test.expected, result)
			}
		})
	}
}

func TestExtractGCPRegionFromEnv(t *testing.T) {
	tests := []struct {
		name        string
		envValue    string
		expected    string
		expectError bool
	}{
		{
			name:     "region set in env",
			envValue: "us-west1",
			expected: "us-west1",
		},
		{
			name:     "empty env uses default",
			envValue: "",
			expected: "us-central1",
		},
		{
			name:     "custom region",
			envValue: "europe-west1",
			expected: "europe-west1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Set environment variable
			if test.envValue != "" {
				t.Setenv("GCP_REGION", test.envValue)
			}

			r := &GCPPrivateServiceConnectReconciler{
				Log: testr.New(t),
			}

			actual, err := r.extractGCPRegionFromEnv()

			if test.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if actual != test.expected {
				t.Errorf("expected %s, got %s", test.expected, actual)
			}
		})
	}
}

func TestExtractGCPProjectFromServiceAccount(t *testing.T) {
	tests := []struct {
		name        string
		sa          *corev1.ServiceAccount
		expected    string
		expectError bool
	}{
		{
			name: "valid service account annotation",
			sa: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "operator",
					Namespace: "hypershift",
					Annotations: map[string]string{
						"iam.gke.io/gcp-service-account": "test-sa@test-project.iam.gserviceaccount.com",
					},
				},
			},
			expected: "test-project",
		},
		{
			name: "missing annotation",
			sa: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "operator",
					Namespace:   "hypershift",
					Annotations: map[string]string{},
				},
			},
			expectError: true,
		},
		{
			name: "invalid annotation format",
			sa: &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "operator",
					Namespace: "hypershift",
					Annotations: map[string]string{
						"iam.gke.io/gcp-service-account": "invalid-format",
					},
				},
			},
			expectError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := fake.NewClientBuilder().
				WithScheme(hyperapi.Scheme).
				WithObjects(test.sa).
				Build()

			r := &GCPPrivateServiceConnectReconciler{
				Client: client,
				Log:    testr.New(t),
			}

			actual, err := r.extractGCPProjectFromServiceAccount(context.Background(), "hypershift", "operator")

			if test.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if actual != test.expected {
				t.Errorf("expected %s, got %s", test.expected, actual)
			}
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
			name: "GCP 404 error",
			err: &googleapi.Error{
				Code: 404,
			},
			expected: true,
		},
		{
			name: "GCP 400 error",
			err: &googleapi.Error{
				Code: 400,
			},
			expected: false,
		},
		{
			name:     "non-GCP error",
			err:      errors.New("some other error"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := isNotFoundError(test.err)
			if actual != test.expected {
				t.Errorf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

// Note: TestDelete is commented out because it requires a full GCP client mock
// which would need significant interface refactoring. For now, we test the helper
// functions and other testable components.
//
// func TestDelete(t *testing.T) {
//     // This test would require mocking the full GCP Compute Service client
//     // which is not easily mockable without interface refactoring
// }

// Note: TestReconcileGCPPrivateServiceConnectSpec verifies the structure setup.
// Full testing would require mocking the GCP Compute Service client.
func TestReconcileGCPPrivateServiceConnectSpec(t *testing.T) {
	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-psc",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.GCPPrivateServiceConnectSpec{
			LoadBalancerIP: "10.0.0.1",
			// Testing with pre-populated values to avoid GCP API calls
			ForwardingRuleName: "test-forwarding-rule",
			NATSubnet:          "test-nat-subnet",
		},
	}

	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(gcpPSC, hc).
		Build()

	r := &GCPPrivateServiceConnectReconciler{
		Client:                 client,
		CreateOrUpdateProvider: upsert.New(false),
		ProjectID:              "test-project",
		Region:                 "us-central1",
		Log:                    testr.New(t),
	}

	// Test with pre-populated spec fields to avoid GCP API calls
	err := r.reconcileGCPPrivateServiceConnectSpec(context.Background(), gcpPSC, hc)

	// Since ForwardingRuleName and NATSubnet are already set, this should succeed
	if err != nil {
		t.Errorf("unexpected error with pre-populated spec: %v", err)
	}

	// Verify the fields remain set
	if gcpPSC.Spec.ForwardingRuleName != "test-forwarding-rule" {
		t.Error("ForwardingRuleName should remain set")
	}
	if gcpPSC.Spec.NATSubnet != "test-nat-subnet" {
		t.Error("NATSubnet should remain set")
	}
}

func TestReconcile_NotFound(t *testing.T) {
	client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).Build()

	r := &GCPPrivateServiceConnectReconciler{
		Client: client,
		Log:    testr.New(t),
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "nonexistent",
			Namespace: "test",
		},
	}

	result, err := r.Reconcile(context.Background(), req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	expectedResult := ctrl.Result{}
	if result != expectedResult {
		t.Errorf("expected %+v, got %+v", expectedResult, result)
	}
}

func TestReconcile_PausedUntil(t *testing.T) {
	pausedUntil := "2026-01-01T00:00:00Z"

	// Create a hosted cluster with pause settings
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedClusterSpec{
			PausedUntil: &pausedUntil,
		},
	}

	// Create a hosted control plane
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-namespace",
			Annotations: map[string]string{
				supportutil.HostedClusterAnnotation: "test-namespace/test-cluster",
			},
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ClusterID: "test-cluster",
		},
	}

	gcpPSC := &hyperv1.GCPPrivateServiceConnect{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-psc",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.GCPPrivateServiceConnectSpec{
			LoadBalancerIP: "10.0.0.1",
		},
	}

	client := fake.NewClientBuilder().
		WithScheme(hyperapi.Scheme).
		WithObjects(gcpPSC, hcp, hc).
		Build()

	r := &GCPPrivateServiceConnectReconciler{
		Client:                 client,
		CreateOrUpdateProvider: upsert.New(false),
		ProjectID:              "test-project",
		Region:                 "us-central1",
		Log:                    testr.New(t),
	}

	req := reconcile.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-psc",
			Namespace: "test-namespace",
		},
	}

	result, err := r.Reconcile(context.Background(), req)

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should requeue with a future time since we're paused until 2026
	if result.RequeueAfter <= 0 {
		t.Error("expected positive RequeueAfter duration for paused reconciliation")
	}
}

// Note: Helper functions for creating test objects would go here
// if needed for more comprehensive testing.
