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

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=hfdns
// +genclient
// +genclient:nonNamespaced
// +resourceName=dns_reservations
// +bridge:watch=disabled
// +bridge:wait
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="BaseDomain",type=string,JSONPath=".status.baseDomain"
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".metadata.labels.hyperfleet\\.io/claimed-by-cluster-uid"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// DNSReservation reserves a DNS prefix within a specific zone shard.
// Customers create it in their account namespace (account-<accountID>). The
// regional DNS zone and allocated prefix are operator-managed; the assigned base
// domain and internal HostedCluster name are reported in status. metadata.name
// is customer-selected.
// Labels:
//   - hyperfleet.io/account-id: AWS account that owns this reservation (always set).
//   - hyperfleet.io/claimed-by-cluster-uid: set when a Cluster claims the reservation.
type DNSReservation struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Spec configures the reservation's behavior when a Cluster releases its claim.
	// +optional
	Spec DNSReservationSpec `json:"spec,omitempty"`

	// Status describes whether the reservation has been allocated and its base domain.
	// +optional
	Status DNSReservationStatus `json:"status,omitzero"`
}

// DNSReservationClusterDeletionPolicy controls reservation cleanup when its
// claiming Cluster is deleted.
// +kubebuilder:validation:Enum=Retain;Delete
type DNSReservationClusterDeletionPolicy string

const (
	// DNSReservationClusterDeletionPolicyRetain keeps the reservation available for reuse.
	DNSReservationClusterDeletionPolicyRetain DNSReservationClusterDeletionPolicy = "Retain"
	// DNSReservationClusterDeletionPolicyDelete deletes the reservation and releases its allocation.
	DNSReservationClusterDeletionPolicyDelete DNSReservationClusterDeletionPolicy = "Delete"
)

// DNSReservationSpec defines the desired state of a DNS reservation.
type DNSReservationSpec struct {
	// ClusterDeletionPolicy determines what happens when the claiming Cluster is deleted.
	// It is platform-managed: customer-created reservations default to Retain and
	// operator-created automatic reservations use Delete.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	ClusterDeletionPolicy DNSReservationClusterDeletionPolicy `json:"clusterDeletionPolicy,omitempty"`

	// BaseDomain is the domain reserved for an HCP DNS-domain reservation.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	BaseDomain string `json:"baseDomain,omitempty"`

	// ClusterArch identifies the cluster architecture that can use this domain.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	ClusterArch string `json:"clusterArch,omitempty"`

	// UserDefined is true when the reservation was created by a customer.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	UserDefined bool `json:"userDefined,omitempty"`

	// ReservedAt is the time when the domain was reserved.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	ReservedAt metav1.Time `json:"reservedAt,omitempty"`
}

// DNSReservationPhase represents the allocation lifecycle of a DNS reservation.
// Pending means allocation or recovery is in progress; Ready means the Index is
// reserved by this resource UID and BaseDomain is assigned. Retryable failures
// are reported with a Ready=False condition while the phase remains Pending.
// +kubebuilder:validation:Enum=Pending;Ready
type DNSReservationPhase string

const (
	// DNSReservationPhasePending means allocation or recovery is in progress.
	DNSReservationPhasePending DNSReservationPhase = "Pending"
	// DNSReservationPhaseReady means an owned Index and base domain are assigned.
	DNSReservationPhaseReady DNSReservationPhase = "Ready"
)

// DNSReservationStatus describes the operator-allocated DNS name.
type DNSReservationStatus struct {
	// Conditions represent the latest observations of the reservation's state.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase summarizes allocation state. BaseDomain is empty unless Phase is Ready.
	// +optional
	Phase DNSReservationPhase `json:"phase,omitempty"`

	// BaseDomain is the assigned domain, e.g. "f7a3.0.openshiftapps.com".
	// It is populated when Phase is Ready.
	// +optional
	BaseDomain string `json:"baseDomain,omitempty"`

	// InternalHostedClusterName is the canonical management-cluster name to use
	// with this reservation. Customer-created reservations use a UID-derived name
	// that remains stable after claim; automatic reservations use their owning
	// Cluster's persisted name. It is exposed so networking and Cluster creation
	// use the same value.
	// +optional
	InternalHostedClusterName string `json:"internalHostedClusterName,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
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
