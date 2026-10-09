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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	// DNSReservationClusterNamespaceLabel records the cluster that claimed a
	// customer-created DNS reservation.
	DNSReservationClusterNamespaceLabel = "hyperfleet.io/cluster-namespace"
	// DNSReservationDeletingLabel fences new claims while DNS reservation
	// cleanup is in progress.
	DNSReservationDeletingLabel = "hyperfleet.io/dns-domain-deleting"
)

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=hfdns
// +kubebuilder:printcolumn:name="BaseDomain",type=string,JSONPath=".spec.baseDomain"
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".metadata.labels.hyperfleet\\.io/cluster-namespace"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// DNSReservation reserves a DNS prefix within a specific zone shard.
// Lives in the account namespace (account-<accountID>) for account isolation.
// Global uniqueness of the DNS prefix is guaranteed by the referenced Index
// resource in the shard's uniqueness namespace (dns-shard-<id>-reservations).
// Labels:
//   - hyperfleet.io/account-id: AWS account that owns this reservation (always set).
//   - hyperfleet.io/cluster-namespace: set when a cluster claims the reservation;
//     absent for pre-reservations (e.g. shared-VPC flows).
type DNSReservation struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	Spec DNSReservationSpec `json:"spec"`
}

// IndexRef is a reference to an Index resource by namespace and name.
type IndexRef struct {
	// Namespace of the Index (the uniqueness domain, e.g. "dns-shard-0-reservations").
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// Name of the Index (the unique value, e.g. "f7a3").
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// DNSReservationSpec defines the desired state of a DNS reservation.
type DNSReservationSpec struct {
	// IndexRef references the Index resource in the shard's uniqueness
	// namespace that guarantees global uniqueness for this reservation's
	// prefix within the shard.
	IndexRef IndexRef `json:"indexRef"`

	// BaseDomain is the fully assembled base domain for this reservation
	// (e.g. "f7a3.0.openshiftapps.com"). Used directly as the HostedCluster
	// BaseDomain. Computed at creation time as {prefix}.{shard}.{baseDomain},
	// where baseDomain is the operator's configured --base-domain.
	BaseDomain string `json:"baseDomain"`

	// ClusterArch identifies the cluster architecture that can use this domain.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	ClusterArch string `json:"clusterArch,omitempty"`

	// UserDefined is true when the reservation was created by a customer.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	UserDefined bool `json:"userDefined,omitempty"`

	// ReservedAt is the time when the domain was reserved.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	ReservedAt metav1.Time `json:"reservedAt,omitempty"`
}

// +kubebuilder:object:root=true

// DNSReservationList contains a list of DNSReservation.
type DNSReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []DNSReservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &DNSReservation{}, &DNSReservationList{})
		return nil
	})
}
