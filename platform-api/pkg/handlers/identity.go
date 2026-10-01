package handlers

import (
	"fmt"
	"net/http"

	apivalidation "k8s.io/apimachinery/pkg/api/validation"
	metav1validation "k8s.io/apimachinery/pkg/apis/meta/v1/validation"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation/field"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

// resolveNamespace returns the namespace a create request writes to. The
// namespace is the caller's account: an empty one defaults to it, and any
// other value is rejected (the caller gets 403). accountID comes from the
// SigV4-verified identity, never from the request body.
func resolveNamespace(requested, accountID string) (string, error) {
	ns := hyperfleetv1alpha1.AccountNamespace(accountID)
	if requested != "" && requested != ns {
		return "", fmt.Errorf("metadata.namespace %q is not the caller's account namespace %q", requested, ns)
	}
	return ns, nil
}

// validateClientMetadata checks the key format and size of client-sent labels
// and annotations. Both are stored as-is once valid.
func validateClientMetadata(labels, annotations map[string]string) error {
	path := field.NewPath("metadata")
	errs := metav1validation.ValidateLabels(labels, path.Child("labels"))
	errs = append(errs, apivalidation.ValidateAnnotations(annotations, path.Child("annotations"))...)
	return errs.ToAggregate()
}

// labelSelector parses the optional labelSelector query parameter, in
// Kubernetes selector syntax. An absent parameter selects everything.
func labelSelector(r *http.Request) (labels.Selector, error) {
	return labels.Parse(r.URL.Query().Get("labelSelector"))
}
