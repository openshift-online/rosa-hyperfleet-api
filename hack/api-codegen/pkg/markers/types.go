package markers

import "go/ast"

// WriteMode defines how a field can be mutated by customers
type WriteMode string

const (
	// Mutable fields can be set on create and changed on update
	Mutable WriteMode = "mutable"

	// Immutable fields can be set on create but cannot be changed on update
	Immutable WriteMode = "immutable"

	// ServiceSet fields are set by the platform and cannot be set by customers
	ServiceSet WriteMode = "service-set"
)

// FeatureGateWriteMode represents a write-mode override for a specific feature gate
type FeatureGateWriteMode struct {
	// FeatureGate is the gate that enables this write-mode (empty string = default/no gates enabled)
	FeatureGate string `json:"featureGate"`

	// WriteMode is the effective write-mode when this gate condition matches
	WriteMode WriteMode `json:"writeMode"`
}

// FieldMeta contains metadata extracted from Go markers for a single field
type FieldMeta struct {
	// FieldPath is the JSON path to the field (e.g., "spec.name", "spec.hostedCluster.release")
	FieldPath string

	// UpdateAction is required when a submitted update effectively changes this field.
	UpdateAction string `json:"updateAction,omitempty"`

	// WriteMode controls customer mutability
	WriteMode WriteMode

	// FeatureGate is the gate required to use this field (empty if no gate required)
	FeatureGate string

	// Hidden indicates if the field is excluded from generated Kubernetes OpenAPI
	// (+k8s:openapi-gen=false). Mutable hidden fields can still be exposed by
	// HyperFleet's custom public API schema.
	Hidden bool

	// IsReducedContainer keeps ancestor restrictions separate from leaf projection/enrichment.
	IsReducedContainer bool `json:"reducedContainer,omitempty"`

	// FeatureGateAwareWriteModes allows write-mode to vary based on enabled feature gates
	// Empty FeatureGate in an entry means "default" (when no gates are enabled)
	FeatureGateAwareWriteModes []FeatureGateWriteMode `json:"featureGateAwareWriteModes,omitempty"`

	// OwnerType is the Kind of the CRD that owns this field (e.g., "Cluster", "NodePool")
	// For shared config types, this is the specific CRD context where the field appears
	OwnerType string `json:"ownerType"`

	// OwnerGVK is the GroupVersionKind of the CRD that owns this field
	// (e.g., "hyperfleet.io/v1alpha1.Cluster")
	OwnerGVK string `json:"ownerGVK"`
}

// HiddenFromPublicAPI reports whether the field should be omitted from
// HyperFleet-managed public API types and schemas. A mutable field explicitly
// opts into that API even when standard Kubernetes OpenAPI generation is off.
func (m FieldMeta) HiddenFromPublicAPI() bool {
	return m.Hidden && m.WriteMode != Mutable && !m.IsReducedContainer
}

// TypedFieldRegistry maps CRD type kinds to their field metadata
// Maps: Kind (e.g., "Cluster") → {FieldPath → FieldMeta}
// This allows per-CRD-type field metadata while supporting different rules for the same field in different contexts
type TypedFieldRegistry map[string]map[string]FieldMeta

// UpstreamReducedMapping tracks local types that mirror upstream types
type UpstreamReducedMapping struct {
	// LocalType is the name of the local reduced type (e.g., "ClusterConfiguration")
	LocalType string
	// UpstreamType is the upstream type reference (e.g., "hypershiftv1beta1.ClusterConfiguration")
	UpstreamType string
}

// EmbeddedUpstreamType tracks where upstream-reduced types are embedded in CRDs
type EmbeddedUpstreamType struct {
	ContainerFieldPath string // e.g., "spec.hostedCluster.configuration"
	LocalType          string // e.g., "ClusterConfiguration"
	UpstreamType       string // e.g., "hypershiftv1beta1.ClusterConfiguration"
	CRDOwner           string // e.g., "Cluster"
	CRDOwnerGVK        string // e.g., "hyperfleet.io/v1alpha1.Cluster"
}

// MarkerScanner extracts markers from Go source files
type MarkerScanner struct {
	// InputDirs are the directories to scan for Go files
	InputDirs []string

	// TypedRegistry is the per-CRD-type field metadata
	TypedRegistry TypedFieldRegistry

	// crdTypes maps Kind names to their GroupVersionKind (populated from scheme)
	crdTypes map[string]string

	// typeCache maps type names to their struct definitions
	typeCache map[string]*ast.StructType

	// upstreamReducedTypes maps local type names to their upstream equivalents
	// Used to identify types that need special handling for nested path generation
	upstreamReducedTypes map[string]UpstreamReducedMapping

	// embeddedUpstreamTypes tracks where upstream-reduced types are embedded in CRDs
	// Key: "CRDOwner.ContainerFieldPath.LocalType" to deduplicate
	embeddedUpstreamTypes map[string]EmbeddedUpstreamType

	// UpdateActions supplies authoritative build-time declarations for passthrough fields.
	UpdateActions []UpdateActionDeclaration

	declarationError error

	// verbose enables detailed logging to stderr
	verbose bool
}
