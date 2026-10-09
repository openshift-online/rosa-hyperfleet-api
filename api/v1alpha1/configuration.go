package v1alpha1

import (
	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterConfiguration specifies configuration for individual OCP components in the cluster.
// This is a HyperFleet-owned mirror of hypershiftv1beta1.ClusterConfiguration that allows
// us to add granular markers to nested fields like kubelet config.
// +hyperfleet:upstream-reduced-object=hypershiftv1beta1.ClusterConfiguration
type ClusterConfiguration struct {
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
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
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
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	Scheduler *SchedulerConfiguration `json:"scheduler,omitempty"`

	// proxy contains the configuration for the cluster-wide proxy.
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=mutable
	Proxy *ProxyConfiguration `json:"proxy,omitempty"`

	// kubelet contains the configuration for kubelet on nodes.
	// +hyperfleet:write-mode=service-set
	Kubelet *KubeletConfig `json:"kubelet,omitempty"`

	// machineConfig contains the configuration for machine-level settings.
	// +hyperfleet:write-mode=service-set
	MachineConfig *MachineConfigSpec `json:"machineConfig,omitempty"`
}

// KubeletConfig specifies kubelet configuration with granular markers for customer control.
// +hyperfleet:upstream-reduced-object=hypershiftv1beta1.KubeletConfig
type KubeletConfig struct {
	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	MaxPods *int32 `json:"maxPods,omitempty"`

	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	PodPidsLimit *int64 `json:"podPidsLimit,omitempty"`

	// +hyperfleet:write-mode=immutable
	// +kubebuilder:validation:MaxProperties=32
	SystemReserved map[string]string `json:"systemReserved,omitempty"`

	// +hyperfleet:write-mode=immutable
	// +kubebuilder:validation:MaxProperties=32
	KubeReserved map[string]string `json:"kubeReserved,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:MaxProperties=32
	EvictionHard map[string]string `json:"evictionHard,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:MaxProperties=32
	EvictionSoft map[string]string `json:"evictionSoft,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:MaxProperties=32
	EvictionSoftGracePeriod map[string]string `json:"evictionSoftGracePeriod,omitempty"`

	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`

	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	ImageGCLowThresholdPercent *int32 `json:"imageGCLowThresholdPercent,omitempty"`

	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	ImageMinimumGCAge *metav1.Duration `json:"imageMinimumGCAge,omitempty"`

	// +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	SerializeImagePulls *bool `json:"serializeImagePulls,omitempty"`

	// +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	RegistryPullQPS *int32 `json:"registryPullQPS,omitempty"`

	// +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	RegistryBurst *int32 `json:"registryBurst,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	CPUManagerPolicy *string `json:"cpuManagerPolicy,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:MaxProperties=32
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
	// +kubebuilder:validation:MaxItems=256
	AllowedUnsafeSysctls []string `json:"allowedUnsafeSysctls,omitempty"`

	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	StreamingConnectionIdleTimeout *metav1.Duration `json:"streamingConnectionIdleTimeout,omitempty"`

	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	ContainerLogMaxSize *string `json:"containerLogMaxSize,omitempty"`

	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	ContainerLogMaxFiles *int32 `json:"containerLogMaxFiles,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	MemoryThrottlingFactor *float64 `json:"memoryThrottlingFactor,omitempty"`
}

// Placeholder types for configuration areas not yet exposed.

type ClusterAuthentication struct{}

type FeatureGateConfiguration struct{}

type ImageConfiguration struct{}

// IngressConfiguration specifies the cluster-wide ingress settings.
// +hyperfleet:upstream-reduced-object=configv1.IngressSpec
type IngressConfiguration struct {
	// componentRoutes configures hostnames and serving certificates for
	// the console and downloads OpenShift component routes.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:MaxItems=2
	// +listType=map
	// +listMapKey=namespace
	// +listMapKey=name
	// +optional
	ComponentRoutes []ComponentRouteConfiguration `json:"componentRoutes,omitempty"`
}

// ComponentRouteConfiguration specifies a custom hostname and serving
// certificate for an OpenShift component route.
// +hyperfleet:upstream-reduced-object=configv1.ComponentRouteSpec
type ComponentRouteConfiguration struct {
	// namespace identifies the namespace containing the component route.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:Enum=openshift-console
	Namespace string `json:"namespace"`

	// name identifies the component route within its namespace.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:Enum=console;downloads
	Name string `json:"name"`

	// hostname is the hostname that should be used by the route.
	// +hyperfleet:write-mode=mutable
	Hostname configv1.Hostname `json:"hostname"`

	// servingCertKeyPairSecret references a TLS secret in openshift-config.
	// +hyperfleet:write-mode=mutable
	// +optional
	ServingCertKeyPairSecret configv1.SecretNameReference `json:"servingCertKeyPairSecret"`
}

type NetworkConfiguration struct{}

type OAuthConfiguration struct{}

// SchedulerConfiguration specifies the cluster-wide scheduler settings.
// +hyperfleet:upstream-reduced-object=configv1.SchedulerSpec
type SchedulerConfiguration struct {
	// profile configures how the scheduler makes pod placement decisions.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:Enum=LowNodeUtilization;HighNodeUtilization;NoScoring
	// +optional
	Profile configv1.SchedulerProfile `json:"profile,omitempty"`
}

// ProxyConfiguration specifies the cluster-wide proxy settings.
// +hyperfleet:upstream-reduced-object=configv1.ProxySpec
type ProxyConfiguration struct {
	// httpProxy is the URL of the proxy for HTTP requests.
	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	HTTPProxy string `json:"httpProxy,omitempty"`

	// httpsProxy is the URL of the proxy for HTTPS requests.
	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	HTTPSProxy string `json:"httpsProxy,omitempty"`

	// noProxy is a comma-separated list of hostnames, domains, IP addresses, or CIDRs
	// to exclude from proxying.
	// +hyperfleet:write-mode=mutable
	// +hyperfleet:update-action=UpdateClusterConfig
	// +kubebuilder:validation:MaxLength=8192
	// +optional
	NoProxy string `json:"noProxy,omitempty"`

	// trustedCA is a reference to a ConfigMap containing a CA certificate bundle
	// used for proxy TLS verification.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	TrustedCA string `json:"trustedCA,omitempty"`

	// readinessEndpoints is a list of endpoints used to verify proxy readiness.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	ReadinessEndpoints []string `json:"readinessEndpoints,omitempty"`
}

// MachineConfigSpec specifies machine-level configuration.
// +hyperfleet:upstream-reduced-object=hypershiftv1beta1.MachineConfigSpec
type MachineConfigSpec struct {
	// +openshift:enable:FeatureGate=HyperFleetMachineConfig
	// +hyperfleet:write-mode=immutable
	// +kubebuilder:validation:MaxItems=128
	AllowedKernelArguments []string `json:"allowedKernelArguments,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:MaxItems=128
	KernelArguments []string `json:"kernelArguments,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:MaxItems=64
	SystemdUnits []SystemdUnit `json:"systemdUnits,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:MaxItems=256
	Files []FileSpec `json:"files,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	KernelType *string `json:"kernelType,omitempty"`

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +kubebuilder:validation:MaxItems=64
	Extensions []string `json:"extensions,omitempty"`
}

type SystemdUnit struct {
	Name string `json:"name"`
	Enabled  *bool `json:"enabled,omitempty"`
	// +kubebuilder:validation:MaxLength=65536
	Contents string `json:"contents,omitempty"`
	// +kubebuilder:validation:MaxItems=16
	Dropins []SystemdDropin `json:"dropins,omitempty"`
}

type SystemdDropin struct {
	Name string `json:"name"`
	// +kubebuilder:validation:MaxLength=32768
	Contents string `json:"contents,omitempty"`
}

type FileSpec struct {
	Path string `json:"path"`
	// +kubebuilder:validation:MaxLength=262144
	Contents  string  `json:"contents,omitempty"`
	Mode      *int32  `json:"mode,omitempty"`
	User      *string `json:"user,omitempty"`
	Group     *string `json:"group,omitempty"`
	Overwrite *bool   `json:"overwrite,omitempty"`
}
