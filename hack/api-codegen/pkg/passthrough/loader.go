package passthrough

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"unicode"
)

// LoadSourceFiles loads and parses Go source files from a directory
func (g *Generator) LoadSourceFiles(sourceDir string) error {
	fset := token.NewFileSet()

	// Parse all Go files in the directory
	//nolint:staticcheck // ParseDir is sufficient for our use case of parsing single directories
	pkgs, err := parser.ParseDir(fset, sourceDir, func(fi os.FileInfo) bool {
		// Skip test files and generated files
		name := fi.Name()
		return !strings.HasSuffix(name, "_test.go") &&
			!strings.HasPrefix(name, "zz_generated")
	}, parser.ParseComments)

	if err != nil {
		return fmt.Errorf("parsing directory %s: %w", sourceDir, err)
	}

	if len(pkgs) == 0 {
		return fmt.Errorf("no packages found in %s", sourceDir)
	}

	// Store parsed files
	g.parsedFiles = make(map[string]*ast.File)
	for _, pkg := range pkgs {
		for filename, file := range pkg.Files {
			g.parsedFiles[filename] = file
		}
	}

	return nil
}

// GenerateTypeDef creates a passthrough type definition for a source type
func (g *Generator) GenerateTypeDef(typeName string) (*TypeDef, error) {
	// Find the type definition across all parsed files
	var typeSpec *ast.TypeSpec
	for _, file := range g.parsedFiles {
		ast.Inspect(file, func(n ast.Node) bool {
			if ts, ok := n.(*ast.TypeSpec); ok && ts.Name.Name == typeName {
				typeSpec = ts
				return false
			}
			return true
		})
		if typeSpec != nil {
			break
		}
	}

	if typeSpec == nil {
		return nil, fmt.Errorf("type %s not found in parsed files", typeName)
	}

	// Ensure it's a struct type
	structType, ok := typeSpec.Type.(*ast.StructType)
	if !ok {
		return nil, fmt.Errorf("type %s is not a struct", typeName)
	}

	// Auto-derive FieldPrefix from type name if not explicitly set
	// e.g., "HostedClusterSpec" → "spec.hostedCluster", "NodePoolSpec" → "spec.nodePool"
	if g.FieldPrefix == "" {
		g.FieldPrefix = deriveFieldPrefix(typeName)
	}

	typeDef := &TypeDef{
		Name:       typeName + "Passthrough",
		SourceName: typeName,
		Doc:        fmt.Sprintf("%s mirrors %s from upstream", typeName+"Passthrough", typeName),
		Fields:     make([]FieldDef, 0),
	}

	// Process each field
	for _, field := range structType.Fields.List {
		// Skip fields without names (embedded types)
		if len(field.Names) == 0 {
			continue
		}

		for _, name := range field.Names {
			// Skip unexported fields
			if !name.IsExported() {
				continue
			}

			fieldDef := g.createFieldDef(name.Name, field)
			typeDef.Fields = append(typeDef.Fields, fieldDef)
		}
	}

	return typeDef, nil
}

// createFieldDef creates a field definition with appropriate markers
func (g *Generator) createFieldDef(fieldName string, field *ast.Field) FieldDef {
	fieldDef := FieldDef{
		Name: fieldName,
		Type: g.typeToString(field.Type),
	}

	// Extract JSON tag
	if field.Tag != nil {
		tag := strings.Trim(field.Tag.Value, "`")
		if jsonTag := parseStructTag(tag, "json"); jsonTag != "" {
			fieldDef.JSONTag = jsonTag
		}
	}

	// Extract documentation (first line only, collapsed to single line)
	if field.Doc != nil {
		doc := strings.TrimSpace(field.Doc.Text())
		// Take only first line and collapse to single line
		lines := strings.Split(doc, "\n")
		if len(lines) > 0 {
			fieldDef.Doc = strings.TrimSpace(lines[0])
		}
	}

	// Get markers for this field (use JSON tag name for registry lookup,
	// stripping options like ",omitempty" or ",omitzero")
	lookupName := fieldDef.JSONTag
	if i := strings.Index(lookupName, ","); i != -1 {
		lookupName = lookupName[:i]
	}
	if lookupName == "" {
		lookupName = fieldName
	}
	fieldDef.Markers = g.getMarkersForField(lookupName)

	return fieldDef
}

// typeToString converts an AST type expression to a string
func (g *Generator) typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		// Check if this is a type from the source package that needs to be qualified
		typeName := t.Name
		if g.SourcePackageAlias != "" && g.isSourcePackageType(typeName) {
			return g.SourcePackageAlias + "." + typeName
		}
		return typeName
	case *ast.StarExpr:
		return "*" + g.typeToString(t.X)
	case *ast.ArrayType:
		return "[]" + g.typeToString(t.Elt)
	case *ast.MapType:
		return "map[" + g.typeToString(t.Key) + "]" + g.typeToString(t.Value)
	case *ast.SelectorExpr:
		return g.typeToString(t.X) + "." + t.Sel.Name
	default:
		return "interface{}"
	}
}

// isSourcePackageType checks if a type name is defined in the source package
// (not a built-in like string, int, bool)
func (g *Generator) isSourcePackageType(typeName string) bool {
	// Built-in types don't need qualification
	builtins := map[string]bool{
		"bool": true, "byte": true, "complex64": true, "complex128": true,
		"error": true, "float32": true, "float64": true, "int": true,
		"int8": true, "int16": true, "int32": true, "int64": true,
		"rune": true, "string": true, "uint": true, "uint8": true,
		"uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	}

	if builtins[typeName] {
		return false
	}

	// Check if the type is defined in the parsed source files
	for _, file := range g.parsedFiles {
		for _, decl := range file.Decls {
			if genDecl, ok := decl.(*ast.GenDecl); ok {
				for _, spec := range genDecl.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok {
						if typeSpec.Name.Name == typeName {
							return true
						}
					}
				}
			}
		}
	}

	return false
}

// getMarkersForField returns markers for a field, from registry or defaults.
// jsonTagName is the JSON struct tag name (e.g., "autoNode"), which is combined
// with g.FieldPrefix to build the registry lookup key (e.g., "spec.hostedCluster.autoNode").
func (g *Generator) getMarkersForField(jsonTagName string) []string {
	lookupKey := jsonTagName
	if g.FieldPrefix != "" {
		lookupKey = g.FieldPrefix + "." + jsonTagName
	}

	if meta, found := g.Registry[lookupKey]; found {
		var markers []string

		if meta.Hidden {
			markers = append(markers, "+k8s:openapi-gen=false")
		} else {
			markers = append(markers, "+k8s:openapi-gen=true")
		}

		if meta.WriteMode != "" {
			markers = append(markers, fmt.Sprintf("+hyperfleet:write-mode=%s", meta.WriteMode))
		}

		if meta.FeatureGate != "" {
			markers = append(markers, fmt.Sprintf("+openshift:enable:FeatureGate=%s", meta.FeatureGate))
		}

		return markers
	}

	// Apply safe defaults for new fields not in the registry
	return []string{
		"+k8s:openapi-gen=false",
		"+hyperfleet:write-mode=service-set",
	}
}

// deriveFieldPrefix derives a registry field prefix from a Go type name.
// e.g., "HostedClusterSpec" → "spec.hostedCluster", "NodePoolSpec" → "spec.nodePool"
func deriveFieldPrefix(typeName string) string {
	base := strings.TrimSuffix(typeName, "Spec")
	if base == typeName {
		return ""
	}
	runes := []rune(base)
	runes[0] = unicode.ToLower(runes[0])
	return "spec." + string(runes)
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
