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

package v2alpha1

import (
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// ClusterPhase represents the lifecycle phase of a Cluster.
// +kubebuilder:validation:Enum=WaitingForPlacement;Provisioning;Ready;Deleting
type ClusterPhase string

const (
	ClusterPhaseWaitingForPlacement ClusterPhase = "WaitingForPlacement"
	ClusterPhaseProvisioning        ClusterPhase = "Provisioning"
	ClusterPhaseReady               ClusterPhase = "Ready"
	ClusterPhaseDeleting            ClusterPhase = "Deleting"
)

// ClusterSpec defines the desired state of a ROSA HCP cluster.
type ClusterSpec struct {
	// DisplayName is a human-readable name for the cluster.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:MaxLength=256
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// DeleteProtection prevents accidental deletion when enabled.
	// +hyperfleet:write-mode=mutable
	// +optional
	DeleteProtection *bool `json:"deleteProtection,omitempty"`

	// ExpirationTimestamp marks when this cluster should be automatically deleted.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	// +optional
	ExpirationTimestamp *metav1.Time `json:"expirationTimestamp,omitempty"`

	// Properties are arbitrary key-value pairs for customer metadata.
	// +hyperfleet:write-mode=mutable
	// +optional
	Properties map[string]string `json:"properties,omitempty"`

	// Tags are customer-defined labels for organizational purposes.
	// +hyperfleet:write-mode=mutable
	// +openshift:enable:FeatureGate=HyperFleetAutoScaling
	// +optional
	Tags map[string]string `json:"tags,omitempty"`

	// AccountID identifies the customer account (platform-managed, hidden from API).
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	AccountID string `json:"accountId,omitempty"`

	// CreatorARN is the IAM ARN of the user who created this cluster.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	// +kubebuilder:validation:Pattern=`^arn:aws:`
	CreatorARN string `json:"creatorARN,omitempty"`

	// InternalID is an internal platform identifier (platform-managed, hidden).
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	InternalID string `json:"internalId,omitempty"`

	// HostedCluster is the full HyperShift HostedClusterSpec.
	// +kubebuilder:validation:Required
	HostedCluster hypershiftv1beta1.HostedClusterSpec `json:"hostedCluster"`
}

// ClusterStatus defines the observed state of a Cluster.
type ClusterStatus struct {
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	Phase ClusterPhase `json:"phase,omitempty"`

	// +optional
	ControlPlaneEndpoint hypershiftv1beta1.APIEndpoint `json:"controlPlaneEndpoint,omitempty"`

	// +optional
	Version string `json:"version,omitempty"`

	// +optional
	PlacementRef *PlacementReference `json:"placementRef,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// PlacementReference identifies the management cluster assignment.
type PlacementReference struct {
	Name              string `json:"name"`
	ManagementCluster string `json:"managementCluster"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=hfc
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="MC",type=string,JSONPath=".status.placementRef.managementCluster"
// +kubebuilder:printcolumn:name="Endpoint",type=string,JSONPath=".status.controlPlaneEndpoint.host",priority=1
// +kubebuilder:printcolumn:name="Expires",type=date,JSONPath=".spec.expirationTimestamp",priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Cluster is the Schema for the clusters API.
type Cluster struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec ClusterSpec `json:"spec"`

	// +optional
	Status ClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ClusterList contains a list of Cluster.
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Cluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Cluster{}, &ClusterList{})
		return nil
	})
}
