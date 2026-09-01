package render

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RegionalConfig holds per-region values injected by the operator at startup.
type RegionalConfig struct {
	BaseDomain string
	AWSRegion  string
}

// Resource is a generated Kubernetes resource with its GVR for desire creation.
type Resource struct {
	Group     string
	Version   string
	Resource  string
	Name      string
	Namespace string
	Object    any
}

// ClusterIDFromNamespace extracts the cluster UUID from a namespace name
// with the "cluster-" prefix.
func ClusterIDFromNamespace(ns string) string {
	const prefix = "cluster-"
	if len(ns) > len(prefix) && ns[:len(prefix)] == prefix {
		return ns[len(prefix):]
	}
	return ns
}

// Minimal local types for CRDs that lack standalone API modules.
// These produce the correct JSON for kube-applier-aws without pulling
// in the full cert-manager or external-secrets dependency trees.

// Certificate is a minimal cert-manager.io/v1.Certificate for serialization.
type Certificate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              CertificateSpec `json:"spec"`
}

type CertificateSpec struct {
	SecretName string               `json:"secretName"`
	IssuerRef  CertificateIssuerRef `json:"issuerRef"`
	DNSNames   []string             `json:"dnsNames,omitempty"`
}

type CertificateIssuerRef struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// ExternalSecret is a minimal external-secrets.io/v1.ExternalSecret for serialization.
type ExternalSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ExternalSecretSpec `json:"spec"`
}

type ExternalSecretSpec struct {
	RefreshInterval string                    `json:"refreshInterval"`
	SecretStoreRef  SecretStoreRef            `json:"secretStoreRef"`
	Target          ExternalSecretTarget      `json:"target"`
	Data            []ExternalSecretDataEntry `json:"data"`
}

type SecretStoreRef struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type ExternalSecretTarget struct {
	Name           string                       `json:"name"`
	CreationPolicy string                       `json:"creationPolicy"`
	Template       ExternalSecretTargetTemplate `json:"template,omitempty"`
}

type ExternalSecretTargetTemplate struct {
	Type string `json:"type"`
}

type ExternalSecretDataEntry struct {
	SecretKey string            `json:"secretKey"`
	RemoteRef ExternalRemoteRef `json:"remoteRef"`
}

type ExternalRemoteRef struct {
	Key string `json:"key"`
}
