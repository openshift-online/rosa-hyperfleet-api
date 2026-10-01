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

// IndexRef is a reference to an Index resource by namespace and name.
type IndexRef struct {
	// Namespace of the Index (the uniqueness domain, e.g. "dns-shard-0-reservations").
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// Name of the Index (the unique value, e.g. "f7a3").
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// IndexSpec is intentionally empty. All uniqueness semantics are encoded in
// the resource's namespace (the uniqueness domain) and name (the unique value).
type IndexSpec struct{}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=hfidx
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// Index reserves a unique name within a namespace that acts as a uniqueness
// domain. The database primary key (gvk, namespace, name) guarantees that no
// two Index resources can share the same name inside the same namespace,
// providing an atomic global-uniqueness guard for higher-level resources.
// The spec is intentionally empty: the namespace (the uniqueness domain) and
// the name (the unique value) carry all the meaning.
//
// Index is a generic primitive. Each caller picks a namespace naming scheme
// for its uniqueness domain and attaches whatever labels it needs for
// ownership and cleanup; none of that is intrinsic to Index. For example:
//
//	dns-shard-<id>-reservations   — one domain per DNS shard, keyed by prefix
//	oidc-issuer-reservations      — a single domain keyed by issuer URL
//
// Creating the Index is the claim. Every Index carries its holder's uid in the
// hyperfleet.io/owner-uid label: on AlreadyExists, a caller whose uid matches
// already holds the value (so retries are safe); otherwise the value is taken.
// The holder's finalizer deletes the Indexes carrying its uid, and never ones
// carrying someone else's. Data belongs on the holder (e.g. a cluster's
// status.baseDomain), not on the Index.
type Index struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	Spec IndexSpec `json:"spec"`
}

// +kubebuilder:object:root=true

// IndexList contains a list of Index.
type IndexList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Index `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Index{}, &IndexList{})
		return nil
	})
}
