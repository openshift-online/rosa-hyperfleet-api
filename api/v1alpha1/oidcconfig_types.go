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

// OidcConfigPhase represents the lifecycle phase of an OidcConfig.
// +kubebuilder:validation:Enum=Pending;Ready;Error
type OidcConfigPhase string

const (
	OidcConfigPhasePending OidcConfigPhase = "Pending"
	OidcConfigPhaseReady   OidcConfigPhase = "Ready"
	OidcConfigPhaseError   OidcConfigPhase = "Error"
)

// OidcConfigSpec.Type values.
const (
	OidcConfigTypeManaged   = "managed"
	OidcConfigTypeUnmanaged = "unmanaged"
)

// OidcConfigSpec defines the desired state of an OidcConfig.
// +kubebuilder:validation:XValidation:rule="self.type != 'managed' || (self.secretArn == '' && self.installerRoleArn == '')",message="managed type must not set secretArn or installerRoleArn"
// +kubebuilder:validation:XValidation:rule="self.type != 'unmanaged' || (self.secretArn != '' && self.installerRoleArn != '')",message="unmanaged type requires secretArn and installerRoleArn"
// +kubebuilder:validation:XValidation:rule="self.type == oldSelf.type",message="spec.type is immutable"
// +kubebuilder:validation:XValidation:rule="self.secretArn == oldSelf.secretArn",message="spec.secretArn is immutable"
// +kubebuilder:validation:XValidation:rule="self.installerRoleArn == oldSelf.installerRoleArn",message="spec.installerRoleArn is immutable"
// +kubebuilder:validation:XValidation:rule="self.issuerUrl == oldSelf.issuerUrl",message="spec.issuerUrl is immutable"
type OidcConfigSpec struct {
	// Type is the OIDC configuration mode.
	// +kubebuilder:validation:Enum=managed;unmanaged
	// +hyperfleet:write-mode=immutable
	Type string `json:"type"`

	// IssuerUrl is the OIDC issuer URL.
	// Required for both types; computed by platform-api for managed configs,
	// customer-supplied for unmanaged configs.
	// +hyperfleet:write-mode=immutable
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	IssuerUrl string `json:"issuerUrl"`

	// SecretArn is the ARN of the customer's Secrets Manager secret containing the RSA private key.
	// Required for unmanaged configs; must be empty for managed configs.
	// +hyperfleet:write-mode=immutable
	// +optional
	// +kubebuilder:validation:Pattern=`^(arn:aws:secretsmanager:.*)?$`
	SecretArn string `json:"secretArn"`

	// InstallerRoleArn is the ARN of the cross-account IAM role used to read the customer's secret.
	// Required for unmanaged configs; must be empty for managed configs.
	// +hyperfleet:write-mode=immutable
	// +optional
	// +kubebuilder:validation:Pattern=`^(arn:aws:iam::.*)?$`
	InstallerRoleArn string `json:"installerRoleArn"`

	// AccountID is the AWS account that owns this OIDC config.
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	// +optional
	AccountID string `json:"accountId,omitempty"`
}

// OidcConfigStatus defines the observed state of an OidcConfig.
type OidcConfigStatus struct {
	// Conditions represent the latest observations of the OIDC config's state.
	// Known condition types: Ready.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Phase summarizes the OIDC config's lifecycle state.
	// +optional
	Phase OidcConfigPhase `json:"phase,omitempty"`

	// Thumbprint is the SHA-1 fingerprint of the OIDC issuer's TLS certificate.
	// Computed by the controller and refreshed periodically.
	// +optional
	Thumbprint string `json:"thumbprint,omitempty"`

	// LastUsedTimestamp records when a cluster last referenced this config.
	// +optional
	LastUsedTimestamp *metav1.Time `json:"lastUsedTimestamp,omitempty"`

	// ObservedGeneration is the most recent generation observed by the controller.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +genclient
// +genclient:nonNamespaced
// +resourceName=oidc_configs
// +bridge:field=id,meta=name
// +bridge:field=resource_version,meta=resourceVersion
// +bridge:field=generation,meta=generation
// +bridge:watch=disabled
// +bridge:wait
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=hfoc
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=".spec.type"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// OidcConfig is the Schema for the oidcconfigs API.
// It represents a reusable OIDC configuration for cluster identity.
// metadata.Name is the config ID; metadata.Namespace is account-<accountID>.
// Not exposed to REST clients — the platform API is flat (/oidc_configs) and
// derives the account from the caller's identity, not a URL parameter.
type OidcConfig struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec OidcConfigSpec `json:"spec"`

	// +optional
	Status OidcConfigStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// OidcConfigList contains a list of OidcConfig.
type OidcConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []OidcConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &OidcConfig{}, &OidcConfigList{})
		return nil
	})
}
