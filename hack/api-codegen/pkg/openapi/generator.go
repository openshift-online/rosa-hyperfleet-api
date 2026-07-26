package openapi

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

// Generate creates an OpenAPI schema from the specified Go types
func (g *Generator) Generate() error {
	if len(g.InputDirs) == 0 {
		return g.generatePOC()
	}

	// Parse all Go files in input directories
	definitions, typeNames, err := g.scanTypes()
	if err != nil {
		return fmt.Errorf("scanning types: %w", err)
	}

	// Store type names for $ref generation
	g.knownTypes = typeNames

	// Create OpenAPI schema
	swagger := &spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Swagger: "2.0",
			Info: &spec.Info{
				InfoProps: spec.InfoProps{
					Title:       g.Title,
					Version:     g.Version,
					Description: "OpenAPI schema for " + g.Title + " generated from Go types with markers\n\nFields marked with +k8s:openapi-gen=false are excluded from this schema.",
				},
			},
			Paths:       &spec.Paths{Paths: make(map[string]spec.PathItem)},
			Definitions: definitions,
		},
	}

	// Serialize to JSON
	data, err := json.MarshalIndent(swagger, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling OpenAPI schema: %w", err)
	}

	// Ensure output directory exists
	outputDir := filepath.Dir(g.OutputFile)
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	// Write to file
	if err := os.WriteFile(g.OutputFile, data, 0644); err != nil {
		return fmt.Errorf("writing OpenAPI schema: %w", err)
	}

	return nil
}

// generatePOC generates a minimal POC schema (legacy behavior)
func (g *Generator) generatePOC() error {
	swagger := &spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Swagger: "2.0",
			Info: &spec.Info{
				InfoProps: spec.InfoProps{
					Title:       g.Title,
					Version:     g.Version,
					Description: "OpenAPI schema for HyperFleet API generated from Go types with markers\n\nFields marked with +k8s:openapi-gen=false are excluded from this schema.",
				},
			},
			Paths:       &spec.Paths{Paths: make(map[string]spec.PathItem)},
			Definitions: make(spec.Definitions),
		},
	}

	data, err := json.MarshalIndent(swagger, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling OpenAPI schema: %w", err)
	}

	if err := os.WriteFile(g.OutputFile, data, 0644); err != nil {
		return fmt.Errorf("writing OpenAPI schema: %w", err)
	}

	return nil
}

// scanTypes scans Go source files and generates OpenAPI definitions
func (g *Generator) scanTypes() (spec.Definitions, map[string]bool, error) {
	definitions := make(spec.Definitions)
	typeNames := make(map[string]bool)

	// First pass: collect all type names and AST nodes
	type typeInfo struct {
		name       string
		structType *ast.StructType
		doc        *ast.CommentGroup
	}
	var allTypes []typeInfo

	for _, dir := range g.InputDirs {
		fset := token.NewFileSet()

		// Parse all Go files in directory
		//nolint:staticcheck // ParseDir is sufficient for our use case
		pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
			name := fi.Name()
			// Skip test files
			return !strings.HasSuffix(name, "_test.go") &&
				!strings.HasPrefix(name, "zz_generated")
		}, parser.ParseComments)

		if err != nil {
			return nil, nil, fmt.Errorf("parsing directory %s: %w", dir, err)
		}

		// Collect all type names first
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				for _, decl := range file.Decls {
					genDecl, ok := decl.(*ast.GenDecl)
					if !ok || genDecl.Tok != token.TYPE {
						continue
					}

					for _, spec := range genDecl.Specs {
						typeSpec, ok := spec.(*ast.TypeSpec)
						if !ok || !typeSpec.Name.IsExported() {
							continue
						}

						structType, ok := typeSpec.Type.(*ast.StructType)
						if !ok {
							continue
						}

						typeName := typeSpec.Name.Name
						typeNames[typeName] = true
						allTypes = append(allTypes, typeInfo{
							name:       typeName,
							structType: structType,
							doc:        genDecl.Doc,
						})
					}
				}
			}
		}
	}

	// Store known types BEFORE generating schemas
	g.knownTypes = typeNames

	// Second pass: generate schemas with $ref support
	for _, ti := range allTypes {
		schema := g.generateSchema(ti.structType, ti.doc)
		if schema != nil {
			definitions[ti.name] = *schema
		}
	}

	return definitions, typeNames, nil
}

// generateSchema generates an OpenAPI schema for a struct type
func (g *Generator) generateSchema(structType *ast.StructType, doc *ast.CommentGroup) *spec.Schema {
	schema := &spec.Schema{
		SchemaProps: spec.SchemaProps{
			Type:       []string{"object"},
			Properties: make(map[string]spec.Schema),
		},
	}

	// Add type description from doc comment
	if doc != nil {
		schema.Description = strings.TrimSpace(doc.Text())
	}

	var required []string

	// Process each field
	for _, field := range structType.Fields.List {
		// Skip embedded fields (inline types)
		if len(field.Names) == 0 {
			// Handle embedded TypeMeta, ObjectMeta, etc.
			continue
		}

		for _, name := range field.Names {
			// Skip unexported fields
			if !name.IsExported() {
				continue
			}

			// Check for +k8s:openapi-gen=false marker
			if g.isHidden(field) {
				continue
			}

			// Extract JSON tag
			jsonName := g.extractJSONTag(field)
			if jsonName == "" || jsonName == "-" {
				continue
			}

			// Generate field schema
			fieldSchema := g.generateFieldSchema(field)

			// Add field description
			if field.Doc != nil {
				fieldSchema.Description = strings.TrimSpace(field.Doc.Text())
			} else if field.Comment != nil {
				fieldSchema.Description = strings.TrimSpace(field.Comment.Text())
			}

			// Check if field is required (no omitempty tag)
			if g.isRequired(field) {
				required = append(required, jsonName)
			}

			schema.Properties[jsonName] = fieldSchema
		}
	}

	// Sort required fields for consistent output
	sort.Strings(required)
	schema.Required = required

	return schema
}

// generateFieldSchema generates a schema for a single field
func (g *Generator) generateFieldSchema(field *ast.Field) spec.Schema {
	schema := spec.Schema{}

	typeStr := g.exprToString(field.Type)

	// Handle pointers
	typeStr = strings.TrimPrefix(typeStr, "*")

	// Handle basic types
	switch typeStr {
	case "string":
		schema.Type = []string{"string"}
	case "bool":
		schema.Type = []string{"boolean"}
	case "int", "int32", "int64":
		schema.Type = []string{"integer"}
		if typeStr == "int64" {
			schema.Format = "int64"
		} else {
			schema.Format = "int32"
		}
	case "float32", "float64":
		schema.Type = []string{"number"}
		if typeStr == "float64" {
			schema.Format = "double"
		} else {
			schema.Format = "float"
		}
	default:
		// Handle arrays
		if strings.HasPrefix(typeStr, "[]") {
			schema.Type = []string{"array"}
			elemType := strings.TrimPrefix(typeStr, "[]")
			elemType = strings.TrimPrefix(elemType, "*")
			schema.Items = &spec.SchemaOrArray{
				Schema: g.resolveTypeSchema(elemType),
			}
		} else if strings.HasPrefix(typeStr, "map[") {
			// Handle maps — extract value type after the closing ]
			schema.Type = []string{"object"}
			valType := typeStr
			if idx := strings.Index(typeStr, "]"); idx != -1 {
				valType = typeStr[idx+1:]
			}
			valType = strings.TrimPrefix(valType, "*")
			schema.AdditionalProperties = &spec.SchemaOrBool{
				Allows: true,
				Schema: g.resolveTypeSchema(valType),
			}
		} else {
			schema = *g.resolveTypeSchema(typeStr)
		}
	}

	return schema
}

// isHidden checks if a field has +k8s:openapi-gen=false marker
func (g *Generator) isHidden(field *ast.Field) bool {
	if field.Doc != nil {
		text := field.Doc.Text()
		if strings.Contains(text, "+k8s:openapi-gen=false") {
			return true
		}
	}
	if field.Comment != nil {
		text := field.Comment.Text()
		if strings.Contains(text, "+k8s:openapi-gen=false") {
			return true
		}
	}
	return false
}

// extractJSONTag extracts the JSON field name from struct tags
func (g *Generator) extractJSONTag(field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}

	tag := strings.Trim(field.Tag.Value, "`")
	parts := strings.Fields(tag)

	for _, part := range parts {
		if strings.HasPrefix(part, "json:") {
			jsonTag := strings.TrimPrefix(part, "json:")
			jsonTag = strings.Trim(jsonTag, "\"")
			// Split on comma to handle omitempty
			parts := strings.Split(jsonTag, ",")
			return parts[0]
		}
	}

	return ""
}

// isRequired checks if a field is required (no omitempty tag)
func (g *Generator) isRequired(field *ast.Field) bool {
	if field.Tag == nil {
		return false
	}

	tag := strings.Trim(field.Tag.Value, "`")
	parts := strings.Fields(tag)

	for _, part := range parts {
		if strings.HasPrefix(part, "json:") {
			jsonTag := strings.TrimPrefix(part, "json:")
			jsonTag = strings.Trim(jsonTag, "\"")
			// Check if omitempty is present
			return !strings.Contains(jsonTag, "omitempty")
		}
	}

	return false
}

// exprToString converts an AST expression to a string
func (g *Generator) exprToString(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + g.exprToString(t.X)
	case *ast.ArrayType:
		return "[]" + g.exprToString(t.Elt)
	case *ast.MapType:
		return "map[" + g.exprToString(t.Key) + "]" + g.exprToString(t.Value)
	case *ast.SelectorExpr:
		return g.exprToString(t.X) + "." + t.Sel.Name
	default:
		return "interface{}"
	}
}

// resolveTypeSchema returns a schema for a Go type name, using $ref for known
// definitions and primitive schemas for basic types.
func (g *Generator) resolveTypeSchema(goType string) *spec.Schema {
	switch goType {
	case "string":
		return &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"string"}}}
	case "bool":
		return &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"boolean"}}}
	case "int", "int32", "int64":
		return &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"integer"}}}
	case "float32", "float64":
		return &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"number"}}}
	default:
		typeName := goType
		if idx := strings.LastIndex(goType, "."); idx != -1 {
			typeName = goType[idx+1:]
		}
		if g.knownTypes != nil && g.knownTypes[typeName] {
			return &spec.Schema{SchemaProps: spec.SchemaProps{Ref: spec.MustCreateRef("#/definitions/" + typeName)}}
		}
		return &spec.Schema{SchemaProps: spec.SchemaProps{Type: []string{"object"}}}
	}
}
