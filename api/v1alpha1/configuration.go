package v1alpha1

import (
	configv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterConfiguration specifies configuration for individual OCP components in the cluster.
// This is a Seaplane-owned mirror of hypershiftv1beta1.ClusterConfiguration that allows
// us to add granular markers to nested fields like kubelet config.
// +seaplane:upstream-reduced-object=hypershiftv1beta1.ClusterConfiguration
type ClusterConfiguration struct {
	// authentication contains configuration for the cluster authentication.
	// +k8s:openapi-gen=true
	// +seaplane:write-mode=mutable
	Authentication *ClusterAuthentication `json:"authentication,omitempty"`

	// featureGate contains the desired configuration for feature gates.
	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	FeatureGate *FeatureGateConfiguration `json:"featureGate,omitempty"`

	// image contains the configuration for internal registry.
	// +k8s:openapi-gen=true
	// +seaplane:write-mode=mutable
	Image *ImageConfiguration `json:"image,omitempty"`

	// ingress contains the configuration for ingress.
	// +k8s:openapi-gen=true
	// +seaplane:write-mode=mutable
	Ingress *IngressConfiguration `json:"ingress,omitempty"`

	// network contains the configuration for cluster networking.
	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	Network *NetworkConfiguration `json:"network,omitempty"`

	// oauth contains the configuration for OAuth.
	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	OAuth *OAuthConfiguration `json:"oauth,omitempty"`

	// scheduler contains the configuration for scheduler.
	// +k8s:openapi-gen=true
	// +seaplane:write-mode=mutable
	Scheduler *SchedulerConfiguration `json:"scheduler,omitempty"`

	// proxy contains the configuration for the cluster-wide proxy.
	// +k8s:openapi-gen=true
	// +seaplane:write-mode=mutable
	Proxy *ProxyConfiguration `json:"proxy,omitempty"`

	// kubelet contains the configuration for kubelet on nodes.
	// +seaplane:write-mode=service-set
	Kubelet *KubeletConfig `json:"kubelet,omitempty"`

	// machineConfig contains the configuration for machine-level settings.
	// +seaplane:write-mode=service-set
	MachineConfig *MachineConfigSpec `json:"machineConfig,omitempty"`
}

// KubeletConfig specifies kubelet configuration with granular markers for customer control.
// +seaplane:upstream-reduced-object=hypershiftv1beta1.KubeletConfig
type KubeletConfig struct {
	// +seaplane:write-mode=mutable
	MaxPods *int32 `json:"maxPods,omitempty"`

	// +seaplane:write-mode=mutable
	PodPidsLimit *int64 `json:"podPidsLimit,omitempty"`

	// +seaplane:write-mode=immutable
	// +kubebuilder:validation:MaxProperties=32
	SystemReserved map[string]string `json:"systemReserved,omitempty"`

	// +seaplane:write-mode=immutable
	// +kubebuilder:validation:MaxProperties=32
	KubeReserved map[string]string `json:"kubeReserved,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +kubebuilder:validation:MaxProperties=32
	EvictionHard map[string]string `json:"evictionHard,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +kubebuilder:validation:MaxProperties=32
	EvictionSoft map[string]string `json:"evictionSoft,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +kubebuilder:validation:MaxProperties=32
	EvictionSoftGracePeriod map[string]string `json:"evictionSoftGracePeriod,omitempty"`

	// +seaplane:write-mode=mutable
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`

	// +seaplane:write-mode=mutable
	ImageGCLowThresholdPercent *int32 `json:"imageGCLowThresholdPercent,omitempty"`

	// +seaplane:write-mode=mutable
	ImageMinimumGCAge *metav1.Duration `json:"imageMinimumGCAge,omitempty"`

	// +openshift:enable:FeatureGate=SeaplaneKubeletAdvanced
	// +seaplane:write-mode=mutable
	SerializeImagePulls *bool `json:"serializeImagePulls,omitempty"`

	// +openshift:enable:FeatureGate=SeaplaneKubeletAdvanced
	// +seaplane:write-mode=mutable
	RegistryPullQPS *int32 `json:"registryPullQPS,omitempty"`

	// +openshift:enable:FeatureGate=SeaplaneKubeletAdvanced
	// +seaplane:write-mode=mutable
	RegistryBurst *int32 `json:"registryBurst,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	CPUManagerPolicy *string `json:"cpuManagerPolicy,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +kubebuilder:validation:MaxProperties=32
	CPUManagerPolicyOptions map[string]string `json:"cpuManagerPolicyOptions,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	CPUManagerReconcilePeriod *metav1.Duration `json:"cpuManagerReconcilePeriod,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	TopologyManagerPolicy *string `json:"topologyManagerPolicy,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	TopologyManagerScope *string `json:"topologyManagerScope,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +kubebuilder:validation:MaxItems=256
	AllowedUnsafeSysctls []string `json:"allowedUnsafeSysctls,omitempty"`

	// +seaplane:write-mode=mutable
	StreamingConnectionIdleTimeout *metav1.Duration `json:"streamingConnectionIdleTimeout,omitempty"`

	// +seaplane:write-mode=mutable
	ContainerLogMaxSize *string `json:"containerLogMaxSize,omitempty"`

	// +seaplane:write-mode=mutable
	ContainerLogMaxFiles *int32 `json:"containerLogMaxFiles,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	MemoryThrottlingFactor *float64 `json:"memoryThrottlingFactor,omitempty"`
}

// ClusterAuthentication contains the customer-configurable authentication settings.
// +kubebuilder:validation:XValidation:rule="!has(self.oidcProviders) || size(self.oidcProviders) == 0 || self.type == 'OIDC'",message="type must be OIDC when oidcProviders are configured"
type ClusterAuthentication struct {
	// type identifies the authentication mode. Set it to OIDC when configuring oidcProviders.
	// +k8s:openapi-gen=true
	// +seaplane:write-mode=mutable
	// +optional
	Type string `json:"type,omitempty"`

	// oidcProviders are OIDC identity providers that can issue tokens for this cluster.
	// At most one provider can be configured. Set type to OIDC to enable these providers.
	// +k8s:openapi-gen=true
	// +seaplane:write-mode=mutable
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=1
	// +optional
	OIDCProviders []configv1.OIDCProvider `json:"oidcProviders,omitempty"`
}

// Placeholder types for configuration areas not yet exposed.

type FeatureGateConfiguration struct{}

// ImageConfiguration configures how the cluster pulls container images.
// +hyperfleet:upstream-reduced-object=configv1.ImageSpec
type ImageConfiguration struct {
	// registrySources contains configuration for external registries.
	// +hyperfleet:write-mode=mutable
	// +optional
	RegistrySources *RegistrySources `json:"registrySources,omitempty"`

	// allowedRegistriesForImport limits the container image registries from which
	// normal users may import images.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:MaxItems=256
	// +listType=atomic
	// +optional
	AllowedRegistriesForImport []RegistryLocation `json:"allowedRegistriesForImport,omitempty"`
}

// RegistrySources holds cluster-wide information about how to handle container image registries.
// +hyperfleet:upstream-reduced-object=configv1.RegistrySources
type RegistrySources struct {
	// allowedRegistries: when set, only these registries are permitted to pull images.
	// All other registries are blocked.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:MaxItems=256
	// +listType=set
	// +optional
	AllowedRegistries []string `json:"allowedRegistries,omitempty"`

	// blockedRegistries: registries for which image pull should be blocked.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:MaxItems=256
	// +listType=set
	// +optional
	BlockedRegistries []string `json:"blockedRegistries,omitempty"`

	// insecureRegistries: registries for which image pull should allow HTTP or
	// skip TLS certificate verification.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:MaxItems=256
	// +listType=set
	// +optional
	InsecureRegistries []string `json:"insecureRegistries,omitempty"`
}

// RegistryLocation contains configuration for a registry that may be used for image imports.
// +hyperfleet:upstream-reduced-object=configv1.RegistryLocation
type RegistryLocation struct {
	// domainName is the registry hostname.
	// +hyperfleet:write-mode=mutable
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	DomainName string `json:"domainName"`

	// insecure indicates whether the registry is accessed over HTTP or with
	// unverified TLS certificates.
	// +hyperfleet:write-mode=mutable
	// +optional
	Insecure bool `json:"insecure,omitempty"`
}

// IngressConfiguration specifies the cluster-wide ingress settings.
// +seaplane:upstream-reduced-object=configv1.IngressSpec
type IngressConfiguration struct {
	// componentRoutes configures hostnames and serving certificates for
	// the console and downloads OpenShift component routes.
	// +seaplane:write-mode=mutable
	// +kubebuilder:validation:MaxItems=2
	// +listType=map
	// +listMapKey=namespace
	// +listMapKey=name
	// +optional
	ComponentRoutes []ComponentRouteConfiguration `json:"componentRoutes,omitempty"`
}

// ComponentRouteConfiguration specifies a custom hostname and serving
// certificate for an OpenShift component route.
// +seaplane:upstream-reduced-object=configv1.ComponentRouteSpec
type ComponentRouteConfiguration struct {
	// namespace identifies the namespace containing the component route.
	// +seaplane:write-mode=mutable
	// +kubebuilder:validation:Enum=openshift-console
	Namespace string `json:"namespace"`

	// name identifies the component route within its namespace.
	// +seaplane:write-mode=mutable
	// +kubebuilder:validation:Enum=console;downloads
	Name string `json:"name"`

	// hostname is the hostname that should be used by the route.
	// +seaplane:write-mode=mutable
	Hostname configv1.Hostname `json:"hostname"`

	// servingCertKeyPairSecret references a TLS secret in openshift-config.
	// +seaplane:write-mode=mutable
	// +optional
	ServingCertKeyPairSecret configv1.SecretNameReference `json:"servingCertKeyPairSecret"`
}

type NetworkConfiguration struct{}

type OAuthConfiguration struct{}

// SchedulerConfiguration specifies the cluster-wide scheduler settings.
// +seaplane:upstream-reduced-object=configv1.SchedulerSpec
type SchedulerConfiguration struct {
	// profile configures how the scheduler makes pod placement decisions.
	// +seaplane:write-mode=mutable
	// +kubebuilder:validation:Enum=LowNodeUtilization;HighNodeUtilization;NoScoring
	// +optional
	Profile configv1.SchedulerProfile `json:"profile,omitempty"`
}

// ProxyConfiguration specifies the cluster-wide proxy settings.
// +seaplane:upstream-reduced-object=configv1.ProxySpec
type ProxyConfiguration struct {
	// httpProxy is the URL of the proxy for HTTP requests.
	// +seaplane:write-mode=mutable
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	HTTPProxy string `json:"httpProxy,omitempty"`

	// httpsProxy is the URL of the proxy for HTTPS requests.
	// +seaplane:write-mode=mutable
	// +kubebuilder:validation:MaxLength=2048
	// +optional
	HTTPSProxy string `json:"httpsProxy,omitempty"`

	// noProxy is a comma-separated list of hostnames, domains, IP addresses, or CIDRs
	// to exclude from proxying.
	// +seaplane:write-mode=mutable
	// +kubebuilder:validation:MaxLength=8192
	// +optional
	NoProxy string `json:"noProxy,omitempty"`

	// trustedCA is a reference to a ConfigMap containing a CA certificate bundle
	// used for proxy TLS verification.
	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +optional
	TrustedCA string `json:"trustedCA,omitempty"`

	// readinessEndpoints is a list of endpoints used to verify proxy readiness.
	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +optional
	ReadinessEndpoints []string `json:"readinessEndpoints,omitempty"`
}

// MachineConfigSpec specifies machine-level configuration.
// +seaplane:upstream-reduced-object=hypershiftv1beta1.MachineConfigSpec
type MachineConfigSpec struct {
	// +openshift:enable:FeatureGate=SeaplaneMachineConfig
	// +seaplane:write-mode=immutable
	// +kubebuilder:validation:MaxItems=128
	AllowedKernelArguments []string `json:"allowedKernelArguments,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +kubebuilder:validation:MaxItems=128
	KernelArguments []string `json:"kernelArguments,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +kubebuilder:validation:MaxItems=64
	SystemdUnits []SystemdUnit `json:"systemdUnits,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +kubebuilder:validation:MaxItems=256
	Files []FileSpec `json:"files,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	KernelType *string `json:"kernelType,omitempty"`

	// +k8s:openapi-gen=false
	// +seaplane:write-mode=service-set
	// +kubebuilder:validation:MaxItems=64
	Extensions []string `json:"extensions,omitempty"`
}

type SystemdUnit struct {
	Name    string `json:"name"`
	Enabled *bool  `json:"enabled,omitempty"`
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
