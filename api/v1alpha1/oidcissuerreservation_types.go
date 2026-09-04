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
	"crypto/sha256"
	"encoding/hex"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// OidcIssuerReservationName returns the deterministic CRD name for a given
// issuer URL: the full hex-encoded SHA-256 hash (64 lowercase hex chars).
//
// OIDC issuer URLs contain characters invalid in K8s names (://) so we hash
// instead of using the URL directly. SHA-256 has a collision probability of
// ~N²/2^256 (birthday bound): with 10 billion reservations the probability
// of any collision is ~10^-57. In the astronomically unlikely event of a
// collision, the second (different) URL would receive a spurious 409
// "issuer URL already exists" and the customer would need to use a
// different issuer URL.
func OidcIssuerReservationName(issuerUrl string) string {
	h := sha256.Sum256([]byte(issuerUrl))
	return hex.EncodeToString(h[:])
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=hfoir
// +kubebuilder:printcolumn:name="IssuerUrl",type=string,JSONPath=".spec.issuerUrl",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// OidcIssuerReservation reserves a globally unique OIDC issuer URL.
// Name = hex(SHA256(issuerUrl)), which maps arbitrary URLs to valid K8s names.
// The K8s API server enforces name uniqueness, preventing two configs
// from claiming the same issuer URL across all accounts.
// See OidcIssuerReservationName for collision probability analysis.
// Labels:
//   - hyperfleet.io/account-id: AWS account that owns this reservation.
//   - hyperfleet.io/oidcconfig-namespace: namespace of the owning OidcConfig.
//   - hyperfleet.io/oidcconfig-name: name of the owning OidcConfig.
type OidcIssuerReservation struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	Spec OidcIssuerReservationSpec `json:"spec"`
}

// OidcIssuerReservationSpec defines the desired state of an OIDC issuer reservation.
type OidcIssuerReservationSpec struct {
	// IssuerUrl is the OIDC issuer URL being reserved.
	// Stored for display/debugging since the resource name is a hash.
	IssuerUrl string `json:"issuerUrl"`
}

// +kubebuilder:object:root=true

// OidcIssuerReservationList contains a list of OidcIssuerReservation.
type OidcIssuerReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OidcIssuerReservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &OidcIssuerReservation{}, &OidcIssuerReservationList{})
		return nil
	})
}
