package v1alpha1

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
	// This is where we can add granular control over kubelet fields.
	// +hyperfleet:write-mode=service-set
	Kubelet *KubeletConfig `json:"kubelet,omitempty"`

	// machineConfig contains the configuration for machine-level settings (kernel params, systemd, files).
	// Granular markers allow safe subset exposure while hiding dangerous operations.
	// +hyperfleet:write-mode=service-set
	MachineConfig *MachineConfigSpec `json:"machineConfig,omitempty"`
}

// KubeletConfig specifies kubelet configuration.
// This is a HyperFleet-owned type that mirrors hypershiftv1beta1.KubeletConfig
// with granular markers for customer control.
type KubeletConfig struct {
	// maxPods is the maximum number of pods per node.
	// Customers can set this to optimize for high-density workloads.
	// +hyperfleet:write-mode=mutable
	MaxPods *int32 `json:"maxPods,omitempty"`

	// podPidsLimit is the maximum number of PIDs allowed per pod.
	// Customers can increase this for applications that spawn many processes.
	// +hyperfleet:write-mode=mutable
	PodPidsLimit *int64 `json:"podPidsLimit,omitempty"`

	// systemReserved specifies resources reserved for system daemons.
	// Customers can set this on cluster creation but cannot change it later.
	// +hyperfleet:write-mode=immutable
	SystemReserved map[string]string `json:"systemReserved,omitempty"`

	// kubeReserved specifies resources reserved for Kubernetes system components.
	// +hyperfleet:write-mode=immutable
	KubeReserved map[string]string `json:"kubeReserved,omitempty"`

	// evictionHard specifies hard eviction thresholds.
	// Platform manages this for cluster stability and safety.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	EvictionHard map[string]string `json:"evictionHard,omitempty"`

	// evictionSoft specifies soft eviction thresholds.
	// Platform manages this for cluster stability.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	EvictionSoft map[string]string `json:"evictionSoft,omitempty"`

	// evictionSoftGracePeriod specifies grace periods for soft evictions.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	EvictionSoftGracePeriod map[string]string `json:"evictionSoftGracePeriod,omitempty"`

	// imageGCHighThresholdPercent is the disk usage percent triggering image GC.
	// +hyperfleet:write-mode=mutable
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`

	// imageGCLowThresholdPercent is the disk usage percent to gc to.
	// +hyperfleet:write-mode=mutable
	ImageGCLowThresholdPercent *int32 `json:"imageGCLowThresholdPercent,omitempty"`

	// imageMinimumGCAge is the minimum age for an unused image before it is garbage collected.
	// +hyperfleet:write-mode=mutable
	ImageMinimumGCAge *metav1.Duration `json:"imageMinimumGCAge,omitempty"`

	// serializeImagePulls when enabled, tells kubelet to pull images one at a time.
	// Tech preview feature for optimizing image pull performance.
	// +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
	// +hyperfleet:write-mode=mutable
	SerializeImagePulls *bool `json:"serializeImagePulls,omitempty"`

	// registryPullQPS is the limit of registry pulls per second.
	// +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
	// +hyperfleet:write-mode=mutable
	RegistryPullQPS *int32 `json:"registryPullQPS,omitempty"`

	// registryBurst is the maximum size of bursty pulls, temporarily allows pulls to burst.
	// +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
	// +hyperfleet:write-mode=mutable
	RegistryBurst *int32 `json:"registryBurst,omitempty"`

	// cpuManagerPolicy is the CPU management policy.
	// Platform controls this to ensure consistent behavior.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	CPUManagerPolicy *string `json:"cpuManagerPolicy,omitempty"`

	// cpuManagerPolicyOptions is a set of key=value CPU manager policy options.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	CPUManagerPolicyOptions map[string]string `json:"cpuManagerPolicyOptions,omitempty"`

	// cpuManagerReconcilePeriod is the reconciliation period for the CPU manager.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	CPUManagerReconcilePeriod *metav1.Duration `json:"cpuManagerReconcilePeriod,omitempty"`

	// topologyManagerPolicy is the topology management policy.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	TopologyManagerPolicy *string `json:"topologyManagerPolicy,omitempty"`

	// topologyManagerScope represents the scope of topology hint generation.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	TopologyManagerScope *string `json:"topologyManagerScope,omitempty"`

	// allowedUnsafeSysctls are passed to the kubelet config to explicitly allow certain unsafe sysctls.
	// Platform controls the allowlist for security.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	AllowedUnsafeSysctls []string `json:"allowedUnsafeSysctls,omitempty"`

	// streamingConnectionIdleTimeout is the maximum time a streaming connection can be idle.
	// +hyperfleet:write-mode=mutable
	StreamingConnectionIdleTimeout *metav1.Duration `json:"streamingConnectionIdleTimeout,omitempty"`

	// containerLogMaxSize is the maximum size of container log file before it is rotated.
	// +hyperfleet:write-mode=mutable
	ContainerLogMaxSize *string `json:"containerLogMaxSize,omitempty"`

	// containerLogMaxFiles is the maximum number of container log files.
	// +hyperfleet:write-mode=mutable
	ContainerLogMaxFiles *int32 `json:"containerLogMaxFiles,omitempty"`

	// memoryThrottlingFactor specifies the factor multiplied by the memory limit.
	// Platform manages this for performance and stability.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	MemoryThrottlingFactor *float64 `json:"memoryThrottlingFactor,omitempty"`
}

// Placeholder types for other configuration areas
// These would be fully defined similarly to KubeletConfig

type APIServerNetworkConfiguration struct {
	// TODO: Define fields with markers
}

type ClusterAuthentication struct {
	// TODO: Define fields with markers
}

type FeatureGateConfiguration struct {
	// TODO: Define fields with markers
}

type ImageConfiguration struct {
	// TODO: Define fields with markers
}

type IngressConfiguration struct {
	// TODO: Define fields with markers
}

type NetworkConfiguration struct {
	// TODO: Define fields with markers
}

type OAuthConfiguration struct {
	// TODO: Define fields with markers
}

type SchedulerConfiguration struct {
	// TODO: Define fields with markers
}

type ProxyConfiguration struct {
	// TODO: Define fields with markers
}

// MachineConfigSpec specifies machine-level configuration.
// This controls kernel parameters, systemd units, and file writes.
// Most fields are platform-managed for security and stability.
type MachineConfigSpec struct {
	// allowedKernelArguments specifies kernel parameters customers can request.
	// This is a WHITELIST approach - customers can only request known-safe parameters.
	// Platform validates against an allowlist and applies approved parameters.
	// Tech Preview feature requiring explicit enablement.
	// +openshift:enable:FeatureGate=HyperFleetMachineConfig
	// +hyperfleet:write-mode=immutable
	AllowedKernelArguments []string `json:"allowedKernelArguments,omitempty"`

	// kernelArguments are the actual kernel parameters applied to nodes.
	// Platform manages the final list based on AllowedKernelArguments and platform defaults.
	// Hidden from customers - they request via AllowedKernelArguments, platform sets this.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	KernelArguments []string `json:"kernelArguments,omitempty"`

	// systemdUnits are systemd units to configure on nodes.
	// Platform-only for security - arbitrary systemd units are dangerous.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	SystemdUnits []SystemdUnit `json:"systemdUnits,omitempty"`

	// files are file writes to perform on nodes.
	// Platform-only for security - arbitrary file writes are dangerous.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Files []FileSpec `json:"files,omitempty"`

	// fips enables FIPS mode on nodes.
	// Immutable - must be set at cluster creation, cannot be changed.
	// +hyperfleet:write-mode=immutable
	FIPS *bool `json:"fips,omitempty"`

	// kernelType specifies the kernel variant (default, realtime).
	// Platform manages this for consistency and support.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	KernelType *string `json:"kernelType,omitempty"`

	// extensions are additional software to install on nodes (e.g., usbguard, sandboxed-containers).
	// Platform manages the allowed extension list.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Extensions []string `json:"extensions,omitempty"`
}

// SystemdUnit represents a systemd unit configuration.
type SystemdUnit struct {
	// name is the name of the systemd unit (e.g., "custom.service")
	Name string `json:"name"`

	// enabled specifies whether the unit is enabled
	Enabled *bool `json:"enabled,omitempty"`

	// contents is the full systemd unit file contents
	Contents string `json:"contents,omitempty"`

	// dropins are drop-in configurations for the unit
	Dropins []SystemdDropin `json:"dropins,omitempty"`
}

// SystemdDropin represents a systemd drop-in configuration.
type SystemdDropin struct {
	// name is the name of the drop-in file
	Name string `json:"name"`

	// contents is the drop-in file contents
	Contents string `json:"contents,omitempty"`
}

// FileSpec represents a file to write to nodes.
type FileSpec struct {
	// path is the absolute path where the file should be written
	Path string `json:"path"`

	// contents is the file contents
	Contents string `json:"contents,omitempty"`

	// mode is the file permissions (e.g., 0644)
	Mode *int32 `json:"mode,omitempty"`

	// user is the file owner user
	User *string `json:"user,omitempty"`

	// group is the file owner group
	Group *string `json:"group,omitempty"`

	// overwrite specifies whether to overwrite existing files
	Overwrite *bool `json:"overwrite,omitempty"`
}
