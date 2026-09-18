package v1alpha1

import (
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// ClusterNetworking mirrors upstream ClusterNetworking with custom markers.
// +hyperfleet:upstream-reduced-object=hypershiftv1beta1.ClusterNetworking
type ClusterNetworking struct {
	// machineNetwork is the list of IP address pools for machines. This might be used among other things to generate appropriate networking security groups in some clouds providers. Currently only one entry or two for dual stack is supported. This field is immutable.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	MachineNetwork []hypershiftv1beta1.MachineNetworkEntry `json:"machineNetwork,omitempty"`
	// clusterNetwork is the list of IP address pools for pods. Defaults to cidr: "10.132.0.0/14". Currently only one entry is supported. This field is immutable.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	ClusterNetwork []hypershiftv1beta1.ClusterNetworkEntry `json:"clusterNetwork,omitempty"`
	// serviceNetwork is the list of IP address pools for services. Defaults to cidr: "172.31.0.0/16". Currently only one entry is supported. This field is immutable.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	ServiceNetwork []hypershiftv1beta1.ServiceNetworkEntry `json:"serviceNetwork,omitempty"`
	// networkType specifies the SDN provider used for cluster networking. Defaults to OVNKubernetes. This field is required and immutable. kubebuilder:validation:XValidation:rule="self == oldSelf", message="networkType is immutable"
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	NetworkType hypershiftv1beta1.NetworkType `json:"networkType,omitempty"`
	// apiServer contains advanced network settings for the API server that affect how the APIServer is exposed inside a hosted cluster node.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	APIServer *hypershiftv1beta1.APIServerNetworking `json:"apiServer,omitempty"`
	// allocateNodeCIDRs controls whether the kube-controller-manager manages node CIDR allocation. When using networkType=Other, it is recommended to set this field to "Enabled" if Flannel is used as the CNI, as it relies on this behavior. Default is "Disabled". This field can only be set to "Enabled" when NetworkType is "Other". Setting it to "Enabled" with any other NetworkType will result in a validation error during cluster creation.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	AllocateNodeCIDRs *hypershiftv1beta1.AllocateNodeCIDRsMode `json:"allocateNodeCIDRs,omitempty"`
}
