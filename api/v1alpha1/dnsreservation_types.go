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
// domain is reported in status. metadata.name is customer-selected.
// Labels:
//   - hyperfleet.io/account-id: AWS account that owns this reservation (always set).
//   - hyperfleet.io/claimed-by-cluster-uid: set when a Cluster claims the reservation.
type DNSReservation struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// Spec is empty; customers provide their reservation name in metadata.
	// +optional
	Spec DNSReservationSpec `json:"spec,omitempty"`

	// Status describes whether the reservation has been allocated and its base domain.
	// +optional
	Status DNSReservationStatus `json:"status,omitzero"`
}

// DNSReservationSpec defines the desired state of a DNS reservation.
type DNSReservationSpec struct {
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
