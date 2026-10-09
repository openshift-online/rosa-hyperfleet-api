package validation

import (
	"fmt"
	"strings"

	k8svalidation "k8s.io/apimachinery/pkg/util/validation"
)

// ValidateClusterName validates the customer-facing Cluster metadata.name.
func ValidateClusterName(name string) error {
	if problems := k8svalidation.IsDNS1123Label(name); len(problems) > 0 {
		return fmt.Errorf("must be a DNS label: %s", strings.Join(problems, ", "))
	}
	return nil
}

// ValidateResourceName validates a namespaced Kubernetes object name.
func ValidateResourceName(name string) error {
	if problems := k8svalidation.IsDNS1123Subdomain(name); len(problems) > 0 {
		return fmt.Errorf("must be a DNS subdomain: %s", strings.Join(problems, ", "))
	}
	return nil
}

// ParseNodePoolName validates and separates the account-scoped child name
// format <cluster>.<child>. Each component is a DNS label.
func ParseNodePoolName(name string) (clusterName, childName string, err error) {
	parts := strings.Split(name, ".")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("must be <cluster>.<child>")
	}
	for _, part := range parts {
		if problems := k8svalidation.IsDNS1123Label(part); len(problems) > 0 {
			return "", "", fmt.Errorf("each name component must be a DNS label: %s", strings.Join(problems, ", "))
		}
	}
	return parts[0], parts[1], nil
}

// ValidateAccountNamespace accepts an omitted namespace or the authenticated
// account's canonical namespace. Callers set the canonical namespace on write.
func ValidateAccountNamespace(namespace, accountID string) error {
	want := "account-" + accountID
	if namespace != "" && namespace != want {
		return fmt.Errorf("must be %q", want)
	}
	return nil
}
