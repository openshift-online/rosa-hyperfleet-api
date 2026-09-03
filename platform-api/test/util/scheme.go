package util

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

// NewScheme constructs a runtime.Scheme with corev1 and hyperfleetv1alpha1
// registered, failing the test immediately if either registration fails.
func NewScheme(t testing.TB) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add corev1 to scheme: %v", err)
	}
	if err := hyperfleetv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("failed to add hyperfleetv1alpha1 to scheme: %v", err)
	}
	return s
}
