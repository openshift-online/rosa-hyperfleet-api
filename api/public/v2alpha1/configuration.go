package v2alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterConfiguration specifies configuration for individual OCP components in the cluster.
// This is a HyperFleet-owned mirror of hypershiftv1beta1.ClusterConfiguration that allows
// us to add granular markers to nested fields like kubelet config.
type ClusterConfiguration struct {
	// apiServer contains advanced network settings for the API server.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	APIServer *APIServerNetworkConfiguration `json:"apiServer,omitempty"`

	// authentication contains configuration for the cluster authentication.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Authentication *ClusterAuthentication `json:"authentication,omitempty"`

	// featureGate contains the desired configuration for feature gates.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	FeatureGate *FeatureGateConfiguration `json:"featureGate,omitempty"`

	// image contains the configuration for internal registry.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Image *ImageConfiguration `json:"image,omitempty"`

	// ingress contains the configuration for ingress.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Ingress *IngressConfiguration `json:"ingress,omitempty"`

	// network contains the configuration for cluster networking.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Network *NetworkConfiguration `json:"network,omitempty"`

	// oauth contains the configuration for OAuth.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	OAuth *OAuthConfiguration `json:"oauth,omitempty"`

	// scheduler contains the configuration for scheduler.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Scheduler *SchedulerConfiguration `json:"scheduler,omitempty"`

	// proxy contains the configuration for the cluster-wide proxy.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Proxy *ProxyConfiguration `json:"proxy,omitempty"`

	// kubelet contains the configuration for kubelet on nodes.
	// +hyperfleet:write-mode=service-set
	Kubelet *KubeletConfig `json:"kubelet,omitempty"`

	// machineConfig contains the configuration for machine-level settings.
	// +hyperfleet:write-mode=service-set
	MachineConfig *MachineConfigSpec `json:"machineConfig,omitempty"`
}

// KubeletConfig specifies kubelet configuration with granular markers for customer control.
type KubeletConfig struct {
	// +hyperfleet:write-mode=mutable
	MaxPods *int32 `json:"maxPods,omitempty"`

	// +hyperfleet:write-mode=mutable
	PodPidsLimit *int64 `json:"podPidsLimit,omitempty"`

	// +hyperfleet:write-mode=immutable
	SystemReserved map[string]string `json:"systemReserved,omitempty"`

	// +hyperfleet:write-mode=immutable
	KubeReserved map[string]string `json:"kubeReserved,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	EvictionHard map[string]string `json:"evictionHard,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	EvictionSoft map[string]string `json:"evictionSoft,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	EvictionSoftGracePeriod map[string]string `json:"evictionSoftGracePeriod,omitempty"`

	// +hyperfleet:write-mode=mutable
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`

	// +hyperfleet:write-mode=mutable
	ImageGCLowThresholdPercent *int32 `json:"imageGCLowThresholdPercent,omitempty"`

	// +hyperfleet:write-mode=mutable
	ImageMinimumGCAge *metav1.Duration `json:"imageMinimumGCAge,omitempty"`

	// +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
	// +hyperfleet:write-mode=mutable
	SerializeImagePulls *bool `json:"serializeImagePulls,omitempty"`

	// +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
	// +hyperfleet:write-mode=mutable
	RegistryPullQPS *int32 `json:"registryPullQPS,omitempty"`

	// +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
	// +hyperfleet:write-mode=mutable
	RegistryBurst *int32 `json:"registryBurst,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	CPUManagerPolicy *string `json:"cpuManagerPolicy,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	CPUManagerPolicyOptions map[string]string `json:"cpuManagerPolicyOptions,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	CPUManagerReconcilePeriod *metav1.Duration `json:"cpuManagerReconcilePeriod,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	TopologyManagerPolicy *string `json:"topologyManagerPolicy,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	TopologyManagerScope *string `json:"topologyManagerScope,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	AllowedUnsafeSysctls []string `json:"allowedUnsafeSysctls,omitempty"`

	// +hyperfleet:write-mode=mutable
	StreamingConnectionIdleTimeout *metav1.Duration `json:"streamingConnectionIdleTimeout,omitempty"`

	// +hyperfleet:write-mode=mutable
	ContainerLogMaxSize *string `json:"containerLogMaxSize,omitempty"`

	// +hyperfleet:write-mode=mutable
	ContainerLogMaxFiles *int32 `json:"containerLogMaxFiles,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	MemoryThrottlingFactor *float64 `json:"memoryThrottlingFactor,omitempty"`
}

// Placeholder types for configuration areas not yet exposed.

type APIServerNetworkConfiguration struct{}

type ClusterAuthentication struct{}

type FeatureGateConfiguration struct{}

type ImageConfiguration struct{}

type IngressConfiguration struct{}

type NetworkConfiguration struct{}

type OAuthConfiguration struct{}

type SchedulerConfiguration struct{}

type ProxyConfiguration struct{}

// MachineConfigSpec specifies machine-level configuration.
type MachineConfigSpec struct {
	// +openshift:enable:FeatureGate=HyperFleetMachineConfig
	// +hyperfleet:write-mode=immutable
	AllowedKernelArguments []string `json:"allowedKernelArguments,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	KernelArguments []string `json:"kernelArguments,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	SystemdUnits []SystemdUnit `json:"systemdUnits,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Files []FileSpec `json:"files,omitempty"`

	// +hyperfleet:write-mode=immutable
	FIPS *bool `json:"fips,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	KernelType *string `json:"kernelType,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Extensions []string `json:"extensions,omitempty"`
}

type SystemdUnit struct {
	Name     string          `json:"name"`
	Enabled  *bool           `json:"enabled,omitempty"`
	Contents string          `json:"contents,omitempty"`
	Dropins  []SystemdDropin `json:"dropins,omitempty"`
}

type SystemdDropin struct {
	Name     string `json:"name"`
	Contents string `json:"contents,omitempty"`
}

type FileSpec struct {
	Path      string `json:"path"`
	Contents  string `json:"contents,omitempty"`
	Mode      *int32 `json:"mode,omitempty"`
	User      *string `json:"user,omitempty"`
	Group     *string `json:"group,omitempty"`
	Overwrite *bool  `json:"overwrite,omitempty"`
}
