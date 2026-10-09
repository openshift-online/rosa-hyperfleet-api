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
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// NodePoolPhase represents the lifecycle phase of a NodePool.
// +kubebuilder:validation:Enum=WaitingForCluster;Provisioning;Ready;Deleting
type NodePoolPhase string

const (
	NodePoolPhaseWaitingForCluster NodePoolPhase = "WaitingForCluster"
	NodePoolPhaseProvisioning      NodePoolPhase = "Provisioning"
	NodePoolPhaseReady             NodePoolPhase = "Ready"
	NodePoolPhaseDeleting          NodePoolPhase = "Deleting"
)

// NodePoolSpec defines the desired state of a NodePool.
type NodePoolSpec struct {
	// AutoRepair controls whether unhealthy nodes are automatically replaced.
	// +hyperfleet:write-mode=mutable
	// +optional
	AutoRepair *bool `json:"autoRepair,omitempty"`

	// Labels are customer-defined labels applied to nodes in this pool.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:MaxProperties=100
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// AccountID is the AWS account that owns this node pool.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	AccountID string `json:"accountId,omitempty"`

	// InternalPoolID is a platform-assigned unique identifier.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	InternalPoolID string `json:"internalPoolId,omitempty"`

	// NodePool contains the upstream HyperShift fields, mirrored as
	// passthrough types with per-field visibility and write-mode markers.
	// +kubebuilder:validation:Required
	NodePool NodePoolSpecPassthrough `json:"nodePool"`
}

// NodePoolStatus defines the observed state of a NodePool.
type NodePoolStatus struct {
	// Replicas is the latest observed number of nodes in the pool.
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Conditions represent the latest observations of the node pool's state.
	// Known condition types: Synced, Ready.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase summarizes the node pool's lifecycle state.
	// +optional
	Phase NodePoolPhase `json:"phase,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +bridge:watch=disabled
// +bridge:wait
// +bridge:parent=Cluster,label=hyperfleet.io/cluster-uid
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=hfnp
// +kubebuilder:validation:XValidation:rule=`self.metadata.name.matches("^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?[.][a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$")`,message="metadata.name must be <cluster>.<child> with DNS labels of at most 63 characters each"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// NodePool is the Schema for the nodepools API.
// It represents a set of worker nodes for a Cluster.
// metadata.Namespace is account-<accountID>. metadata.Name is the client-selected
// <cluster>.<child> name.
// The parent Cluster name is the prefix of metadata.name; the server sets its
// ownerReference and cluster-uid label after resolving that Cluster by account namespace/name.
type NodePool struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec NodePoolSpec `json:"spec"`

	// +optional
	Status NodePoolStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// NodePoolList contains a list of NodePool.
type NodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []NodePool `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &NodePool{}, &NodePoolList{})
		return nil
	})
}

// NodePoolPlatform mirrors NodePoolPlatform from upstream HyperShift, exposing only
// the AWS platform. Other platforms are intentionally omitted.
// +hyperfleet:upstream-reduced-object=hypershiftv1beta1.NodePoolPlatform
// +k8s:openapi-gen=true
type NodePoolPlatform struct {
	// type specifies the platform type for this NodePool.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=immutable
	// +required
	Type hypershiftv1beta1.PlatformType `json:"type"`

	// aws specifies the platform-specific AWS configuration for this NodePool.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	// +optional
	AWS *hypershiftv1beta1.AWSNodePoolPlatform `json:"aws,omitempty"`
}
