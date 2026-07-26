package markers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"strings"
)

var (
	// Marker patterns
	openapiGenPattern                = regexp.MustCompile(`\+k8s:openapi-gen=false`)
	writeModePattern                 = regexp.MustCompile(`\+hyperfleet:write-mode=(mutable|immutable|service-set)`)
	featureGatePattern               = regexp.MustCompile(`\+openshift:enable:FeatureGate=(\w+)`)
	featureGateAwareWriteModePattern = regexp.MustCompile(`\+hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="([^"]*)",writeMode="(mutable|immutable|service-set)"`)
)

// NewScanner creates a new marker scanner
func NewScanner(inputDirs []string) *MarkerScanner {
	return &MarkerScanner{
		InputDirs: inputDirs,
		Registry:  make(FieldRegistry),
		typeCache: make(map[string]*ast.StructType),
	}
}

// Scan walks the input directories and extracts marker metadata
func (s *MarkerScanner) Scan() error {
	for _, dir := range s.InputDirs {
		if err := s.scanDir(dir); err != nil {
			return fmt.Errorf("scanning directory %s: %w", dir, err)
		}
	}
	return nil
}

// scanDir processes all Go files in a directory
func (s *MarkerScanner) scanDir(dir string) error {
	fset := token.NewFileSet()

	//nolint:staticcheck // ParseDir is sufficient for our use case of scanning single directories
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		// Skip test files and generated files
		name := fi.Name()
		return !strings.HasSuffix(name, "_test.go") &&
			!strings.HasPrefix(name, "zz_generated")
	}, parser.ParseComments)

	if err != nil {
		return fmt.Errorf("parsing directory: %w", err)
	}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			s.scanFile(file)
		}
	}

	return nil
}

// scanFile extracts markers from a single Go file
func (s *MarkerScanner) scanFile(file *ast.File) {
	// First pass: collect all type definitions
	ast.Inspect(file, func(n ast.Node) bool {
		typeSpec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}

		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return true
		}

		// Cache the type definition
		s.typeCache[typeSpec.Name.Name] = structType

		return true
	})

	// Second pass: process only root types (Cluster, NodePool, etc.)
	// Skip Spec/Status/Passthrough types as they'll be processed via recursion
	for typeName, structType := range s.typeCache {
		if isRootType(typeName) {
			s.processStruct(typeName, structType, "")
		}
	}
}

// isRootType returns true for top-level CRD types (not Spec/Status/Passthrough types)
func isRootType(typeName string) bool {
	// Root types don't have suffixes
	return !strings.HasSuffix(typeName, "Spec") &&
		!strings.HasSuffix(typeName, "Status") &&
		!strings.HasSuffix(typeName, "List") &&
		!strings.HasSuffix(typeName, "Passthrough") &&
		typeName != "ClusterReference"
}

// processStruct walks struct fields and extracts markers
func (s *MarkerScanner) processStruct(_ string, structType *ast.StructType, parentPath string) {
	for _, field := range structType.Fields.List {
		s.processField(field, parentPath)
	}
}

// processField extracts markers from a single field
func (s *MarkerScanner) processField(field *ast.Field, parentPath string) {
	// Get JSON tag to determine field path
	jsonName := getJSONName(field)
	if jsonName == "" || jsonName == "-" {
		return
	}

	// Build full field path
	var fieldPath string
	if parentPath == "" {
		fieldPath = jsonName
	} else {
		fieldPath = parentPath + "." + jsonName
	}

	// Extract markers from comments
	meta := s.extractMarkers(field, fieldPath)
	if meta != nil {
		s.Registry[fieldPath] = *meta
	}

	// Recursively process nested structs
	s.processNestedType(field.Type, fieldPath)
}

// processNestedType recursively handles nested struct types
func (s *MarkerScanner) processNestedType(expr ast.Expr, fieldPath string) {
	switch t := expr.(type) {
	case *ast.StructType:
		// Inline struct
		s.processStruct("", t, fieldPath)
	case *ast.StarExpr:
		// Pointer to type
		s.processNestedType(t.X, fieldPath)
	case *ast.Ident:
		// Named type - look it up in the cache
		if structType, ok := s.typeCache[t.Name]; ok {
			s.processStruct(t.Name, structType, fieldPath)
		}
	case *ast.SelectorExpr:
		// External type (e.g., metav1.Time) - skip
	case *ast.ArrayType:
		// Array/slice - process element type
		s.processNestedType(t.Elt, fieldPath)
	case *ast.MapType:
		// Map - process value type
		s.processNestedType(t.Value, fieldPath)
	}
}

// extractMarkers parses comment markers and creates FieldMeta
func (s *MarkerScanner) extractMarkers(field *ast.Field, fieldPath string) *FieldMeta {
	if field.Doc == nil {
		return nil
	}

	comments := field.Doc.Text()

	meta := &FieldMeta{
		FieldPath: fieldPath,
	}

	// Check for openapi-gen=false (field is hidden)
	if openapiGenPattern.MatchString(comments) {
		meta.Hidden = true
	}

	// Extract write mode
	if matches := writeModePattern.FindStringSubmatch(comments); len(matches) > 1 {
		meta.WriteMode = WriteMode(matches[1])
	}

	// Extract feature gate
	if matches := featureGatePattern.FindStringSubmatch(comments); len(matches) > 1 {
		meta.FeatureGate = matches[1]
	}

	// Extract feature-gate-aware write-modes
	var gatedModes []FeatureGateWriteMode
	for _, match := range featureGateAwareWriteModePattern.FindAllStringSubmatch(comments, -1) {
		featureGate := match[1] // Empty string or gate name
		mode := WriteMode(match[2])
		gatedModes = append(gatedModes, FeatureGateWriteMode{
			FeatureGate: featureGate,
			WriteMode:   mode,
		})
	}

	if len(gatedModes) > 0 {
		meta.FeatureGateAwareWriteModes = gatedModes
	}

	// Only include in registry if at least one marker was found
	if meta.Hidden || meta.WriteMode != "" || meta.FeatureGate != "" || len(meta.FeatureGateAwareWriteModes) > 0 {
		return meta
	}

	return nil
}

// getJSONName extracts the JSON field name from struct tags
func getJSONName(field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}

	tag := field.Tag.Value
	// Remove backticks
	tag = strings.Trim(tag, "`")

	// Parse json tag
	jsonTag := parseStructTag(tag, "json")
	if jsonTag == "" {
		return ""
	}

	// Handle "name,omitempty" or "name" format
	parts := strings.Split(jsonTag, ",")
	return parts[0]
}

// parseStructTag extracts a specific tag value from struct tag string
func parseStructTag(tag, key string) string {
	// Simple tag parser - handles: `json:"name,omitempty" yaml:"name"`
	parts := strings.Fields(tag)
	prefix := key + `:"`

	for _, part := range parts {
		if strings.HasPrefix(part, prefix) {
			value := strings.TrimPrefix(part, prefix)
			value = strings.TrimSuffix(value, `"`)
			return value
		}
	}

	return ""
}

// Validate checks that all fields in the registry have required markers
func (r FieldRegistry) Validate() error {
	var errors []string

	for path, meta := range r {
		// All visible fields must have a write mode
		if !meta.Hidden && meta.WriteMode == "" {
			errors = append(errors, fmt.Sprintf("field %s is missing +hyperfleet:write-mode marker", path))
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("validation failed:\n  %s", strings.Join(errors, "\n  "))
	}

	return nil
}

// ValidateAllFields checks that ALL struct fields (not just those with markers) meet requirements
// This is more strict and should be used in CI
func (s *MarkerScanner) ValidateAllFields() error {
	// This would require re-scanning and checking all fields, not just those with markers
	// For now, just validate what's in the registry
	return s.Registry.Validate()
}
