/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	"fmt"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/types"
)

// Identity model (see hyperfleet-operator/docs/hyperfleet-db-guidelines.md):
//   - namespace is the account: "account-<id>";
//   - name is chosen by the client and never changes; a child of a cluster is
//     named "<cluster>.<child>";
//   - uid is minted by the database, and every stored reference uses it.

const (
	// AccountNamespacePrefix prefixes the namespace that holds an account's objects.
	AccountNamespacePrefix = "account-"

	// ManagementClusterNamespacePrefix prefixes a cluster's namespace on its
	// management cluster: "cluster-<uid>".
	ManagementClusterNamespacePrefix = "cluster-"

	// ChildNameSeparator joins a cluster name and a child name. Neither part
	// may contain it, so "<cluster>.<child>" never collides.
	ChildNameSeparator = "."

	// MaxClusterNameLength bounds a cluster name. HyperShift builds the
	// control-plane namespace "cluster-<uid>-<cluster>", which must fit in 63.
	MaxClusterNameLength = 63 - len(ManagementClusterNamespacePrefix) - 36 - len("-")

	// MaxChildNameLength bounds the child part of "<cluster>.<child>".
	MaxChildNameLength = 63
)

// Labels written by platform-api and the operator. Client-sent values for any
// hyperfleet.io/ label are ignored.
const (
	// ReservedLabelPrefix is the label prefix only the service may set.
	ReservedLabelPrefix = "hyperfleet.io/"

	// ClusterUIDLabel carries the owning cluster's uid on every object that
	// belongs to a cluster. It is set once at create, from the same parent
	// as the ownerReference, and selects a cluster's objects.
	ClusterUIDLabel = "hyperfleet.io/cluster-uid"

	// OwnerUIDLabel carries the holder's uid on an Index claim.
	OwnerUIDLabel = "hyperfleet.io/owner-uid"

	// ClaimedByClusterUIDLabel marks an OidcConfig as claimed by the cluster
	// with this uid. Unlike ClusterUIDLabel it is mutable: the claim is
	// released when the cluster goes away and can be taken again.
	ClaimedByClusterUIDLabel = "hyperfleet.io/claimed-by-cluster-uid"
)

// dnsLabelRE is a DNS label without dots: lowercase alphanumerics and '-',
// starting and ending with an alphanumeric.
var dnsLabelRE = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// AccountNamespace returns the namespace holding accountID's objects.
func AccountNamespace(accountID string) string {
	return AccountNamespacePrefix + accountID
}

// ManagementClusterNamespace returns the namespace a cluster's objects live in
// on its management cluster.
func ManagementClusterNamespace(clusterUID types.UID) string {
	return ManagementClusterNamespacePrefix + string(clusterUID)
}

// ValidateClusterName checks a cluster name: a DNS label of at most
// MaxClusterNameLength characters.
func ValidateClusterName(name string) error {
	return validateNamePart("cluster name", name, MaxClusterNameLength)
}

// ChildName builds the name of a cluster's child: "<cluster>.<child>". It is
// the only name ever built from other names.
func ChildName(clusterName, child string) string {
	return clusterName + ChildNameSeparator + child
}

// SplitChildName validates a "<cluster>.<child>" name and returns its parts.
func SplitChildName(name string) (clusterName, child string, err error) {
	clusterName, child, ok := strings.Cut(name, ChildNameSeparator)
	if !ok {
		return "", "", fmt.Errorf("name %q must have the form <cluster>%s<child>", name, ChildNameSeparator)
	}
	if err := ValidateClusterName(clusterName); err != nil {
		return "", "", err
	}
	if err := validateNamePart("child name", child, MaxChildNameLength); err != nil {
		return "", "", err
	}
	return clusterName, child, nil
}

func validateNamePart(what, s string, maxLen int) error {
	if s == "" {
		return fmt.Errorf("%s must not be empty", what)
	}
	if len(s) > maxLen {
		return fmt.Errorf("%s %q must be at most %d characters", what, s, maxLen)
	}
	if !dnsLabelRE.MatchString(s) {
		return fmt.Errorf("%s %q must consist of lowercase letters, digits, and '-', "+
			"start and end with a letter or digit, and contain no dots", what, s)
	}
	return nil
}
