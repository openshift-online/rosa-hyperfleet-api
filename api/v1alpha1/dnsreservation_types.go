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
// +kubebuilder:resource:scope=Cluster,shortName=hfdns
// +kubebuilder:printcolumn:name="Shard",type=string,JSONPath=".spec.shard"
// +kubebuilder:printcolumn:name="Prefix",type=string,JSONPath=".spec.prefix"
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=".metadata.labels.hyperfleet\\.io/cluster-namespace"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// DNSReservation reserves a DNS prefix within a specific zone shard.
// Name format: "{shard}-{prefix}" (e.g. "0-f7a3").
// The API server rejects duplicates, ensuring each prefix is unique per shard.
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

// DNSReservationSpec defines the desired state of a DNS reservation.
type DNSReservationSpec struct {
	// Shard is the DNS zone shard this reservation belongs to (e.g. "0").
	// +kubebuilder:validation:Pattern=`^[a-z0-9]+$`
	Shard string `json:"shard"`

	// Prefix is the randomly generated DNS disambiguation token (e.g. "f7a3").
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{4}$`
	Prefix string `json:"prefix"`

	// BaseDomain is the fully assembled base domain for this reservation
	// (e.g. "f7a3.0.openshiftapps.com"). Used directly as the HostedCluster
	// BaseDomain. Computed at creation time from {prefix}.{shard}.{regionalBaseDomain}.
	BaseDomain string `json:"baseDomain"`
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
