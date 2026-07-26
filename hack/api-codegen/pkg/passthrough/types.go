package passthrough

import (
	"go/ast"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
)

// Generator generates passthrough types from upstream types
type Generator struct {
	// SourceDir is the directory containing source Go files
	SourceDir string

	// SourceTypes are the type names to generate passthroughs for (e.g., ["HostedClusterSpec", "NodePoolSpec"])
	SourceTypes []string

	// OutputPackage is the package name for generated code
	OutputPackage string

	// Registry contains existing field markers to preserve
	Registry markers.FieldRegistry

	// SourcePackage is the import path of the source package (e.g., "github.com/openshift/hypershift/api/hypershift/v1beta1")
	SourcePackage string

	// SourcePackageAlias is the alias to use for the source package import (e.g., "hypershiftv1")
	SourcePackageAlias string

	// FieldPrefix is the dotted path prefix for registry lookups (e.g., "spec.hostedCluster")
	FieldPrefix string

	// parsedFiles holds parsed AST of source files
	parsedFiles map[string]*ast.File
}

// ParsedFiles returns the parsed files (for CLI tool)
func (g *Generator) ParsedFiles() map[string]*ast.File {
	return g.parsedFiles
}

// TypeDef represents a generated passthrough type definition
type TypeDef struct {
	// Name is the generated type name (e.g., "HostedClusterPassthrough")
	Name string

	// SourceName is the original type name (e.g., "HostedCluster")
	SourceName string

	// Fields are the struct fields
	Fields []FieldDef

	// Doc is the type documentation
	Doc string
}

// FieldDef represents a single field in a passthrough type
type FieldDef struct {
	// Name is the Go field name
	Name string

	// Type is the Go type (as a string)
	Type string

	// JSONTag is the json struct tag
	JSONTag string

	// Doc is the field documentation
	Doc string

	// Markers are the Go markers to include
	Markers []string

	// IsNested indicates if this is a nested struct type that needs its own passthrough
	IsNested bool
}

// NewGenerator creates a new passthrough generator
func NewGenerator(sourceDir string, sourceTypes []string, registry markers.FieldRegistry) *Generator {
	return &Generator{
		SourceDir:     sourceDir,
		SourceTypes:   sourceTypes,
		OutputPackage: "v1alpha1",
		Registry:      registry,
		parsedFiles:   make(map[string]*ast.File),
	}
}
