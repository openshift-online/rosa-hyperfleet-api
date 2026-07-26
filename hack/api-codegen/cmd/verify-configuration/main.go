package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
)

// verify-configuration ensures all fields in configuration.go have required markers
func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: verify-configuration <file.go>")
		os.Exit(1)
	}

	filePath := os.Args[1]
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing file: %v\n", err)
		os.Exit(1)
	}

	var errors []string

	// Visit all struct type declarations
	ast.Inspect(node, func(n ast.Node) bool {
		typeSpec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}

		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return true
		}

		// Only check config-related structs
		typeName := typeSpec.Name.Name
		if !isConfigType(typeName) {
			return true
		}

		// Check each field in the struct
		for _, field := range structType.Fields.List {
			if field.Names == nil {
				// Embedded field, skip
				continue
			}

			fieldName := field.Names[0].Name
			if !field.Names[0].IsExported() {
				// Unexported field, skip
				continue
			}

			// Check if field has markers (in Doc or Comment)
			hasWriteMode := false
			hasVisibility := false

			// Check Doc comments (above the field)
			if field.Doc != nil {
				for _, comment := range field.Doc.List {
					text := comment.Text
					if strings.Contains(text, "+hyperfleet:write-mode=") {
						hasWriteMode = true
					}
					if strings.Contains(text, "+k8s:openapi-gen=") {
						hasVisibility = true
					}
				}
			}

			// Check inline comments (after the field)
			if field.Comment != nil {
				for _, comment := range field.Comment.List {
					text := comment.Text
					if strings.Contains(text, "+hyperfleet:write-mode=") {
						hasWriteMode = true
					}
					if strings.Contains(text, "+k8s:openapi-gen=") {
						hasVisibility = true
					}
				}
			}

			// Fields should have write-mode marker
			if !hasWriteMode {
				errors = append(errors, fmt.Sprintf("%s.%s: missing +hyperfleet:write-mode marker", typeName, fieldName))
			}

			// Note: Visibility markers are optional
			// - No marker = visible (default, standard Kubernetes convention)
			// - +k8s:openapi-gen=false = hidden (explicit)
			// We only enforce write-mode markers, not visibility markers
			_ = hasVisibility // Acknowledged - used for future enforcement if needed
		}

		return true
	})

	if len(errors) > 0 {
		fmt.Println("Configuration verification failed:")
		for _, err := range errors {
			fmt.Printf("  ❌ %s\n", err)
		}
		fmt.Println("\nAll fields in configuration types must have +hyperfleet:write-mode markers.")
		fmt.Println("Add one of: mutable, immutable, service-set")
		os.Exit(1)
	}

	fmt.Println("✅ Configuration verification passed")
}

func isConfigType(name string) bool {
	// Skip support types that are never exposed directly
	supportTypes := []string{
		"SystemdUnit",
		"SystemdDropin",
		"FileSpec",
	}
	for _, t := range supportTypes {
		if name == t {
			return false
		}
	}

	// Check if this is a configuration-related type that needs markers
	suffixes := []string{
		"Configuration",
		"Config",
		"Spec",
	}

	for _, suffix := range suffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return false
}
