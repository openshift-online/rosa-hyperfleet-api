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

	// WriteMode controls customer mutability
	WriteMode WriteMode

	// FeatureGate is the gate required to use this field (empty if no gate required)
	FeatureGate string

	// Hidden indicates if the field is excluded from OpenAPI (+k8s:openapi-gen=false)
	Hidden bool

	// FeatureGateAwareWriteModes allows write-mode to vary based on enabled feature gates
	// Empty FeatureGate in an entry means "default" (when no gates are enabled)
	FeatureGateAwareWriteModes []FeatureGateWriteMode `json:"featureGateAwareWriteModes,omitempty"`
}

// FieldRegistry is a map from field path to its metadata
type FieldRegistry map[string]FieldMeta

// MarkerScanner extracts markers from Go source files
type MarkerScanner struct {
	// InputDirs are the directories to scan for Go files
	InputDirs []string

	// Registry is the collected field metadata
	Registry FieldRegistry

	// typeCache maps type names to their struct definitions
	typeCache map[string]*ast.StructType
}
