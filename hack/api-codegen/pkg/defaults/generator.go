package defaults

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/template"
)

// FieldDefault represents a field with a default value
type FieldDefault struct {
	TypeName  string // e.g., "ClusterNetworking"
	FieldName string // e.g., "NetworkType"
	FieldType string // e.g., "hypershiftv1beta1.NetworkType"
	JSONTag   string // e.g., "networkType,omitempty"
	Default   string // e.g., "OVNKubernetes" or {{cidr: "10.132.0.0/14"}}
	IsSlice   bool   // true if field is a slice type
	IsPointer bool   // true if field is a pointer type
}

// Generator generates default value functions from +kubebuilder:default markers
type Generator struct {
	SourceDir   string
	PackageName string
	Verbose     bool
	Defaults    map[string][]FieldDefault // TypeName -> []FieldDefault
	imports     map[string]bool           // track required imports
}

// NewGenerator creates a new default generator
func NewGenerator(sourceDir, packageName string, verbose bool) *Generator {
	return &Generator{
		SourceDir:   sourceDir,
		PackageName: packageName,
		Verbose:     verbose,
		Defaults:    make(map[string][]FieldDefault),
		imports:     make(map[string]bool),
	}
}

// ScanDefaults scans source files for +kubebuilder:default markers
func (g *Generator) ScanDefaults() error {
	fset := token.NewFileSet()

	return filepath.Walk(g.SourceDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}

		// Skip generated files
		if strings.Contains(path, "zz_generated") {
			return nil
		}

		return g.scanFile(fset, path)
	})
}

// scanFile scans a single Go file for default markers
func (g *Generator) scanFile(fset *token.FileSet, path string) error {
	if g.Verbose {
		log.Printf("Scanning file: %s", path)
	}

	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		typeSpec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}

		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return true
		}

		typeName := typeSpec.Name.Name
		if g.Verbose {
			log.Printf("Found struct type: %s", typeName)
		}

		// Scan fields for default markers
		for _, field := range structType.Fields.List {
			if field.Doc == nil {
				continue
			}

			var defaultValue string
			for _, comment := range field.Doc.List {
				if strings.Contains(comment.Text, "+kubebuilder:default") {
					// Extract default value from marker
					text := comment.Text
					if idx := strings.Index(text, "+kubebuilder:default"); idx >= 0 {
						marker := text[idx:]
						// Parse: +kubebuilder:default="value" or +kubebuilder:default={{...}}
						if strings.Contains(marker, "=") {
							parts := strings.SplitN(marker, "=", 2)
							if len(parts) == 2 {
								defaultValue = strings.TrimSpace(parts[1])
							}
						}
					}
				}
			}

			if defaultValue == "" {
				continue
			}

			// Process each field name (fields can have multiple names)
			for _, fieldName := range field.Names {
				fieldType := g.typeToString(field.Type)
				jsonTag := g.extractJSONTag(field.Tag)

				fd := FieldDefault{
					TypeName:  typeName,
					FieldName: fieldName.Name,
					FieldType: fieldType,
					JSONTag:   jsonTag,
					Default:   defaultValue,
					IsSlice:   g.isSliceType(field.Type),
					IsPointer: g.isPointerType(field.Type),
				}

				if g.Verbose {
					log.Printf("  Found default: %s.%s = %s", typeName, fieldName.Name, defaultValue)
				}

				g.Defaults[typeName] = append(g.Defaults[typeName], fd)

				// Track imports
				g.trackImport(fieldType)
			}
		}

		return true
	})

	return nil
}

// typeToString converts an ast.Expr to a string representation
func (g *Generator) typeToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return fmt.Sprintf("%s.%s", g.typeToString(t.X), t.Sel.Name)
	case *ast.ArrayType:
		return "[]" + g.typeToString(t.Elt)
	case *ast.StarExpr:
		return "*" + g.typeToString(t.X)
	default:
		return "unknown"
	}
}

// isSliceType checks if a type is a slice
func (g *Generator) isSliceType(expr ast.Expr) bool {
	_, ok := expr.(*ast.ArrayType)
	return ok
}

// isPointerType checks if a type is a pointer
func (g *Generator) isPointerType(expr ast.Expr) bool {
	_, ok := expr.(*ast.StarExpr)
	return ok
}

// extractJSONTag extracts the json tag value
func (g *Generator) extractJSONTag(tag *ast.BasicLit) string {
	if tag == nil {
		return ""
	}

	tagStr := tag.Value
	tagStr = strings.Trim(tagStr, "`")

	// Parse json:"fieldName,omitempty"
	for _, part := range strings.Fields(tagStr) {
		if strings.HasPrefix(part, "json:") {
			jsonPart := strings.TrimPrefix(part, "json:")
			jsonPart = strings.Trim(jsonPart, "\"")
			return jsonPart
		}
	}

	return ""
}

// trackImport tracks imports needed for the generated code
func (g *Generator) trackImport(typeStr string) {
	if strings.Contains(typeStr, "hypershiftv1beta1") {
		g.imports["github.com/openshift/hypershift/api/hypershift/v1beta1"] = true
	}
	if strings.Contains(typeStr, "ipnet") {
		g.imports["github.com/openshift/hypershift/api/util/ipnet"] = true
	}
}

// Generate generates the defaults file
func (g *Generator) Generate(outputPath string) error {
	funcMap := template.FuncMap{
		"contains":    strings.Contains,
		"extractCIDR": extractCIDR,
	}

	tmpl := template.Must(template.New("defaults").Funcs(funcMap).Parse(defaultsTemplate))

	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}
	defer f.Close()

	data := struct {
		Package  string
		Imports  []string
		Defaults map[string][]FieldDefault
	}{
		Package:  g.PackageName,
		Imports:  g.getImportList(),
		Defaults: g.Defaults,
	}

	if err := tmpl.Execute(f, data); err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	return nil
}

// extractCIDR extracts the CIDR value from a kubebuilder default marker
// Input: {{cidr: "10.132.0.0/14"}}
// Output: "10.132.0.0/14"
func extractCIDR(defaultStr string) string {
	// Remove {{ and }}
	s := strings.TrimSpace(defaultStr)
	s = strings.TrimPrefix(s, "{{")
	s = strings.TrimSuffix(s, "}}")
	s = strings.TrimSpace(s)

	// Parse cidr: "value"
	if strings.HasPrefix(s, "cidr:") {
		s = strings.TrimPrefix(s, "cidr:")
		s = strings.TrimSpace(s)
		s = strings.Trim(s, "\"")
		return fmt.Sprintf("%q", s)
	}

	return defaultStr
}

// getImportList returns sorted list of imports
func (g *Generator) getImportList() []string {
	var imports []string
	for imp := range g.imports {
		imports = append(imports, imp)
	}
	return imports
}

const defaultsTemplate = `// Code generated by default-gen. DO NOT EDIT.
//
// This file contains constant default values extracted from +kubebuilder:default markers.
// The SetDefaults functions are defined in defaults.go (manually maintained).

package {{ .Package }}

// Default values extracted from +kubebuilder:default markers
const (
{{- range $typeName, $fields := .Defaults }}
	{{- range $field := $fields }}
	{{- if and (not $field.IsSlice) (not (contains $field.Default "{{")) }}
	// Default{{ $typeName }}{{ $field.FieldName }} is the default value for {{ $typeName }}.{{ $field.FieldName }}
	Default{{ $typeName }}{{ $field.FieldName }} = {{ $field.Default }}
	{{- end }}
	{{- end }}
{{- end }}
)

// Default CIDR strings extracted from +kubebuilder:default markers
const (
{{- range $typeName, $fields := .Defaults }}
	{{- range $field := $fields }}
	{{- if contains $field.Default "cidr:" }}
	// Default{{ $typeName }}{{ $field.FieldName }}CIDR is the default CIDR for {{ $typeName }}.{{ $field.FieldName }}
	Default{{ $typeName }}{{ $field.FieldName }}CIDR = {{ extractCIDR $field.Default }}
	{{- end }}
	{{- end }}
{{- end }}
)
`
