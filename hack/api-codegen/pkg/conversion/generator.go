package conversion

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/registry"
)

// Generator generates REST types and conversion functions from CRD types
type Generator struct {
	APIVersion    string   // e.g., "v1alpha1"
	CRDPackage    string   // Import path to CRD types
	OutputDir     string   // Output directory for generated code
	OutputPackage string   // Import path for the output package (derived from OutputDir if empty)
	InputDirs     []string // Directories containing CRD source files

	// Internal state
	knownTypes     map[string]bool      // Set of all type names
	typeInfos      map[string]*typeInfo // Type name -> type information
	emittedHelpers map[string]bool      // Tracks emitted project/unproject helpers to avoid duplicates
}

// typeInfo holds parsed information about a Go type
type typeInfo struct {
	Name       string
	StructType *ast.StructType
	Doc        *ast.CommentGroup
	Fields     []*fieldInfo
}

// fieldInfo holds information about a struct field
type fieldInfo struct {
	GoName    string     // Go field name (e.g., "DisplayName")
	JSONName  string     // JSON tag name (e.g., "displayName")
	GoType    string     // Go type as string (e.g., "string", "*bool")
	FieldPath string     // Registry path (e.g., "spec.displayName")
	Field     *ast.Field // Original AST field
	Doc       *ast.CommentGroup
	Hidden    bool // From registry
	WriteMode registry.WriteMode
}

// NewGenerator creates a new conversion generator
func NewGenerator(apiVersion, crdPackage string, inputDirs []string, outputDir string) *Generator {
	return &Generator{
		APIVersion:     apiVersion,
		CRDPackage:     crdPackage,
		InputDirs:      inputDirs,
		OutputDir:      outputDir,
		knownTypes:     make(map[string]bool),
		typeInfos:      make(map[string]*typeInfo),
		emittedHelpers: make(map[string]bool),
	}
}

// Generate runs all three generation phases
func (g *Generator) Generate() error {
	// Parse CRD types first
	if err := g.parseTypes(); err != nil {
		return fmt.Errorf("parsing types: %w", err)
	}

	// Phase 1: Generate REST types (filter hidden fields)
	if err := g.generateRESTTypes(); err != nil {
		return fmt.Errorf("generating REST types: %w", err)
	}

	// Phase 2: Generate ServiceSetFields
	if err := g.generateServiceSetFields(); err != nil {
		return fmt.Errorf("generating ServiceSetFields: %w", err)
	}

	// Phase 3: Generate conversion functions
	if err := g.generateConversionFunctions(); err != nil {
		return fmt.Errorf("generating conversion functions: %w", err)
	}

	return nil
}

// parseTypes scans input directories and parses all CRD types
func (g *Generator) parseTypes() error {
	for _, dir := range g.InputDirs {
		fset := token.NewFileSet()

		// Parse all Go files in directory
		//nolint:staticcheck // ParseDir is sufficient for our use case
		pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
			name := fi.Name()
			// Skip test files and generated files
			return !strings.HasSuffix(name, "_test.go") &&
				!strings.HasPrefix(name, "zz_generated")
		}, parser.ParseComments)

		if err != nil {
			return fmt.Errorf("parsing directory %s: %w", dir, err)
		}

		// Collect all types
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
						g.knownTypes[typeName] = true

						// Create type info
						ti := &typeInfo{
							Name:       typeName,
							StructType: structType,
							Doc:        genDecl.Doc,
							Fields:     []*fieldInfo{},
						}

						// Parse fields
						for _, field := range structType.Fields.List {
							// Skip embedded fields
							if len(field.Names) == 0 {
								continue
							}

							for _, name := range field.Names {
								// Skip unexported fields
								if !name.IsExported() {
									continue
								}

								fi := g.parseField(typeName, field, name)
								if fi != nil {
									ti.Fields = append(ti.Fields, fi)
								}
							}
						}

						g.typeInfos[typeName] = ti
					}
				}
			}
		}
	}

	return nil
}

// parseField parses a single struct field
func (g *Generator) parseField(typeName string, field *ast.Field, name *ast.Ident) *fieldInfo {
	goName := name.Name
	jsonName := g.extractJSONTag(field)
	if jsonName == "" || jsonName == "-" {
		return nil // Skip fields without JSON tags
	}

	// Build field path for registry lookup
	fieldPath := g.buildFieldPath(typeName, jsonName)

	// Lookup in registry
	meta, exists := registry.FieldRegistry[fieldPath]

	fi := &fieldInfo{
		GoName:   goName,
		JSONName: jsonName,
		GoType:   g.exprToString(field.Type),
		Field:    field,
		Doc:      field.Doc,
	}

	if exists {
		fi.FieldPath = meta.FieldPath
		fi.Hidden = meta.Hidden
		fi.WriteMode = meta.WriteMode
	}

	return fi
}

// buildFieldPath constructs the registry path for a field
func (g *Generator) buildFieldPath(typeName, jsonName string) string {
	// Map type names to registry prefixes
	switch {
	case strings.HasSuffix(typeName, "Spec"):
		return "spec." + jsonName
	case strings.HasSuffix(typeName, "Status"):
		return "status." + jsonName
	case strings.Contains(typeName, "Passthrough"):
		// For passthrough types, need to determine prefix
		// e.g., HostedClusterSpecPassthrough -> "spec.hostedCluster."
		if strings.HasPrefix(typeName, "HostedCluster") {
			return "spec.hostedCluster." + jsonName
		}
		if strings.HasPrefix(typeName, "NodePool") {
			return "spec.nodePool." + jsonName
		}
		return jsonName
	default:
		return jsonName
	}
}

// extractJSONTag extracts the JSON tag from a field
func (g *Generator) extractJSONTag(field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}

	tag := strings.Trim(field.Tag.Value, "`")
	for _, part := range strings.Fields(tag) {
		if strings.HasPrefix(part, "json:") {
			jsonTag := strings.Trim(strings.TrimPrefix(part, "json:"), "\"")
			// Strip options (e.g., "name,omitempty" -> "name")
			if idx := strings.Index(jsonTag, ","); idx >= 0 {
				return jsonTag[:idx]
			}
			return jsonTag
		}
	}

	return ""
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
	case *ast.InterfaceType, *ast.FuncType, *ast.Ellipsis, *ast.IndexExpr, *ast.IndexListExpr:
		var buf bytes.Buffer
		if err := printer.Fprint(&buf, token.NewFileSet(), expr); err != nil {
			return "interface{}"
		}
		return buf.String()
	default:
		return "interface{}"
	}
}

// typeQualifiers maps unqualified Go type names to the import alias they
// require when referenced outside their declaring package.
var typeQualifiers = map[string]string{
	"ClusterConfiguration":             "v1alpha1",
	"KubeletConfig":                    "v1alpha1",
	"MachineConfigSpec":                "v1alpha1",
	"APIServerNetworkConfiguration":    "v1alpha1",
	"ClusterAuthentication":            "v1alpha1",
	"FeatureGateConfiguration":         "v1alpha1",
	"ImageConfiguration":               "v1alpha1",
	"IngressConfiguration":             "v1alpha1",
	"NetworkConfiguration":             "v1alpha1",
	"OAuthConfiguration":               "v1alpha1",
	"SchedulerConfiguration":           "v1alpha1",
	"ProxyConfiguration":               "v1alpha1",
	"AutoNode":                         "hypershiftv1beta1",
	"Release":                          "hypershiftv1beta1",
	"PlatformSpec":                     "hypershiftv1beta1",
	"DNSSpec":                          "hypershiftv1beta1",
	"ClusterNetworking":                "hypershiftv1beta1",
	"ClusterAutoscaling":               "hypershiftv1beta1",
	"EtcdSpec":                         "hypershiftv1beta1",
	"ServicePublishingStrategyMapping": "hypershiftv1beta1",
	"ImageContentSource":               "hypershiftv1beta1",
	"SecretEncryptionSpec":             "hypershiftv1beta1",
	"OLMCatalogPlacement":              "hypershiftv1beta1",
	"Capabilities":                     "hypershiftv1beta1",
	"OperatorConfiguration":            "hypershiftv1beta1",
	"NodePoolPlatform":                 "hypershiftv1beta1",
	"NodePoolManagement":               "hypershiftv1beta1",
	"NodePoolAutoScaling":              "hypershiftv1beta1",
	"Taint":                            "hypershiftv1beta1",
	"AvailabilityPolicy":               "hypershiftv1beta1",
}

// detectTypeImport returns the import alias required for a Go type string,
// checking both already-qualified prefixes and unqualified names via typeQualifiers.
func detectTypeImport(goType string) string {
	base := strings.TrimPrefix(goType, "*")
	base = strings.TrimPrefix(base, "[]")

	for _, prefix := range []string{"corev1.", "configv1.", "metav1.", "hypershiftv1beta1.", "v1alpha1."} {
		if strings.Contains(base, prefix) {
			return strings.TrimSuffix(prefix, ".")
		}
	}

	if alias, ok := typeQualifiers[base]; ok {
		return alias
	}

	return ""
}

// qualifyType adds package qualifiers to unqualified types that need them
func (g *Generator) qualifyType(goType string) string {
	isPointer := strings.HasPrefix(goType, "*")
	baseType := strings.TrimPrefix(goType, "*")

	if idx := strings.LastIndex(baseType, "."); idx != -1 {
		baseType = baseType[idx+1:]
	}

	if alias, ok := typeQualifiers[baseType]; ok && alias == "v1alpha1" {
		if isPointer {
			return "*v1alpha1." + baseType
		}
		return "v1alpha1." + baseType
	}

	return goType
}

// generateRESTTypes generates REST type definitions (Phase 1)
func (g *Generator) generateRESTTypes() error {
	restDir := filepath.Join(g.OutputDir, "rest")
	if err := g.ensureDir(restDir); err != nil {
		return err
	}

	// Generate REST types for main resource types
	resourceTypes := []string{
		"Cluster", "ClusterSpec", "ClusterStatus",
		"NodePool", "NodePoolSpec", "NodePoolStatus",
		"ClusterReference", // Referenced by NodePoolSpec
	}

	for _, typeName := range resourceTypes {
		ti, exists := g.typeInfos[typeName]
		if !exists {
			// Type might not exist (e.g., NodePool not fully implemented yet)
			continue
		}

		// Generate REST type
		code := g.generateRESTType(ti)

		// Write to rest/{typename}_types.go
		filename := strings.ToLower(typeName) + "_types.go"
		if err := g.writeFile(filepath.Join("rest", filename), code); err != nil {
			return fmt.Errorf("writing REST type %s: %w", typeName, err)
		}
	}

	// Generate passthrough types if they exist
	for typeName := range g.typeInfos {
		if strings.Contains(typeName, "Passthrough") {
			ti := g.typeInfos[typeName]
			code := g.generateRESTType(ti)
			filename := strings.ToLower(typeName) + "_types.go"
			if err := g.writeFile(filepath.Join("rest", filename), code); err != nil {
				return fmt.Errorf("writing REST type %s: %w", typeName, err)
			}
		}
	}

	return nil
}

// generateRESTType generates a REST type from a CRD type
func (g *Generator) generateRESTType(ti *typeInfo) string {
	var b strings.Builder

	// Header
	b.WriteString("// Code generated by conversion-gen. DO NOT EDIT.\n\n")
	b.WriteString("package rest\n\n")

	// Filter visible fields FIRST (before checking imports)
	visibleFields := []*fieldInfo{}
	for _, fi := range ti.Fields {
		if !fi.Hidden {
			visibleFields = append(visibleFields, fi)
		}
	}

	// Check which imports we need (based on VISIBLE fields only)
	restImports := map[string]bool{}
	for _, fi := range visibleFields {
		goType := g.qualifyType(fi.GoType)
		if alias := detectTypeImport(goType); alias != "" {
			restImports[alias] = true
		}
	}
	needsMetav1 := restImports["metav1"]
	needsHyperShift := restImports["hypershiftv1beta1"]
	needsV1alpha1 := restImports["v1alpha1"]

	// Write imports
	if needsMetav1 || needsHyperShift || needsV1alpha1 {
		b.WriteString("import (\n")
		if needsHyperShift {
			b.WriteString("\thypershiftv1beta1 \"github.com/openshift/hypershift/api/hypershift/v1beta1\"\n")
		}
		if needsMetav1 {
			b.WriteString("\tmetav1 \"k8s.io/apimachinery/pkg/apis/meta/v1\"\n")
		}
		if needsV1alpha1 {
			fmt.Fprintf(&b, "\tv1alpha1 \"%s\"\n", g.CRDPackage)
		}
		b.WriteString(")\n\n")
	}

	// Type comment
	if ti.Doc != nil {
		docText := ti.Doc.Text()
		lines := strings.Split(strings.TrimSpace(docText), "\n")
		for _, line := range lines {
			if !strings.HasPrefix(line, "//") {
				b.WriteString("// ")
			}
			b.WriteString(line + "\n")
		}
	} else {
		fmt.Fprintf(&b, "// %s is the REST representation of %s (visible fields only)\n", ti.Name, ti.Name)
	}

	// Struct header
	fmt.Fprintf(&b, "type %s struct {\n", ti.Name)

	// Generate fields
	for _, fi := range visibleFields {
		// Field comment
		if fi.Doc != nil {
			docText := fi.Doc.Text()
			lines := strings.Split(strings.TrimSpace(docText), "\n")
			for _, line := range lines {
				if line != "" {
					if !strings.HasPrefix(line, "//") {
						b.WriteString("\t// ")
					} else {
						b.WriteString("\t")
					}
					b.WriteString(line + "\n")
				}
			}
		}

		// Construct JSON tag
		jsonTag := fi.JSONName
		if fi.Field.Tag != nil {
			// Preserve omitempty and other options
			tag := strings.Trim(fi.Field.Tag.Value, "`")
			for _, part := range strings.Fields(tag) {
				if strings.HasPrefix(part, "json:") {
					jsonTag = strings.Trim(strings.TrimPrefix(part, "json:"), "\"")
					break
				}
			}
		}

		// Field definition - qualify type if needed
		goType := g.qualifyType(fi.GoType)
		fmt.Fprintf(&b, "\t%s %s `json:\"%s\"`\n", fi.GoName, goType, jsonTag)
	}

	b.WriteString("}\n")

	return b.String()
}

// generateServiceSetFields generates the ServiceSetFields struct (Phase 2)
func (g *Generator) generateServiceSetFields() error {
	// Collect all service-set fields from registry
	type serviceSetField struct {
		GoName    string
		GoType    string
		JSONTag   string
		FieldPath string
	}

	fieldsMap := make(map[string]serviceSetField)

	for path, meta := range registry.FieldRegistry {
		if meta.WriteMode == registry.ServiceSet {
			fieldsMap[path] = serviceSetField{
				GoName:    g.pathToGoName(path),
				GoType:    g.inferTypeFromPath(path),
				JSONTag:   g.pathToJSONTag(path),
				FieldPath: path,
			}
		}
	}

	// Convert map to slice for sorting
	var fields []serviceSetField
	for _, f := range fieldsMap {
		fields = append(fields, f)
	}

	// Sort for consistent output
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].GoName < fields[j].GoName
	})

	// Qualify types and detect which imports are actually needed
	type qualifiedField struct {
		serviceSetField
		QualifiedType string
	}
	var qFields []qualifiedField
	ssfImports := map[string]bool{}

	for _, f := range fields {
		qt := g.qualifyType(f.GoType)
		if alias := detectTypeImport(qt); alias != "" {
			ssfImports[alias] = true
		}
		qFields = append(qFields, qualifiedField{serviceSetField: f, QualifiedType: qt})
	}
	needsCorev1 := ssfImports["corev1"]
	needsConfigv1 := ssfImports["configv1"]
	needsMetav1 := ssfImports["metav1"]
	needsHyperShift := ssfImports["hypershiftv1beta1"]
	needsV1alpha1 := ssfImports["v1alpha1"]

	// Derive package name and output path from OutputDir.
	// The parent of OutputDir is where types.go lives (alongside the version subdirectory).
	// e.g., OutputDir = ".../conversion/v1alpha1" → typesDir = ".../conversion", pkg = "conversion"
	typesDir := filepath.Dir(g.OutputDir)
	pkgName := filepath.Base(typesDir)

	// Generate code
	var b strings.Builder

	b.WriteString("// Code generated by conversion-gen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", pkgName)

	if needsCorev1 || needsConfigv1 || needsMetav1 || needsHyperShift || needsV1alpha1 {
		b.WriteString("import (\n")
		if needsConfigv1 {
			b.WriteString("\tconfigv1 \"github.com/openshift/api/config/v1\"\n")
		}
		if needsHyperShift {
			b.WriteString("\thypershiftv1beta1 \"github.com/openshift/hypershift/api/hypershift/v1beta1\"\n")
		}
		if needsCorev1 {
			b.WriteString("\tcorev1 \"k8s.io/api/core/v1\"\n")
		}
		if needsMetav1 {
			b.WriteString("\tmetav1 \"k8s.io/apimachinery/pkg/apis/meta/v1\"\n")
		}
		if needsV1alpha1 {
			fmt.Fprintf(&b, "\tv1alpha1 \"%s\"\n", g.CRDPackage)
		}
		b.WriteString(")\n\n")
	}

	b.WriteString("// ServiceSetFields contains platform-managed fields injected during UnprojectX conversions\n")
	b.WriteString("type ServiceSetFields struct {\n")

	for _, f := range qFields {
		fmt.Fprintf(&b, "\t// %s is service-set (platform-managed, hidden from API)\n", f.GoName)
		fmt.Fprintf(&b, "\t%s %s `json:\"%s\"`\n", f.GoName, f.QualifiedType, f.JSONTag)
	}

	b.WriteString("}\n")

	// Write types.go into the parent of OutputDir
	typesPath := filepath.Join(typesDir, "types.go")
	if err := g.ensureDir(typesDir); err != nil {
		return err
	}
	return os.WriteFile(typesPath, []byte(b.String()), 0644)
}

// pathToGoName converts a field path to a Go field name using all segments
// after stripping the "spec." prefix so that distinct paths produce distinct names.
// e.g., "spec.accountId" -> "AccountID", "spec.hostedCluster.release" -> "HostedClusterRelease"
func (g *Generator) pathToGoName(path string) string {
	path = strings.TrimPrefix(path, "spec.")
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return ""
	}

	var name string
	for _, part := range parts {
		part = strings.ReplaceAll(part, "Id", "ID")
		part = strings.ReplaceAll(part, "Arn", "ARN")
		if len(part) > 0 && part[0] >= 'a' && part[0] <= 'z' {
			part = strings.ToUpper(string(part[0])) + part[1:]
		}
		name += part
	}
	return name
}

// pathToJSONTag derives a unique JSON tag from a field path
// e.g., "spec.accountId" -> "accountId", "spec.hostedCluster.release" -> "hostedCluster.release"
func (g *Generator) pathToJSONTag(path string) string {
	return strings.TrimPrefix(path, "spec.")
}

// inferTypeFromPath infers the Go type for a field path
func (g *Generator) inferTypeFromPath(path string) string {
	// Try to find the field in parsed types
	for _, ti := range g.typeInfos {
		for _, fi := range ti.Fields {
			if fi.FieldPath == path {
				return fi.GoType
			}
		}
	}

	// Default to string if not found
	return "string"
}

// generateConversionFunctions generates conversion functions (Phase 3)
func (g *Generator) generateConversionFunctions() error {
	// Generate for Cluster and NodePool
	resources := []string{"Cluster", "NodePool"}

	for _, resource := range resources {
		// Check if types exist
		specType := resource + "Spec"
		_, hasSpec := g.typeInfos[specType]
		if !hasSpec {
			continue // Skip if resource not fully defined
		}

		// Generate conversion functions
		code := g.generateResourceConversions(resource)

		// Write to {resource}.go
		filename := strings.ToLower(resource) + ".go"
		if err := g.writeFile(filename, code); err != nil {
			return fmt.Errorf("writing conversions for %s: %w", resource, err)
		}
	}

	return nil
}

// generateResourceConversions generates Project and Unproject functions for a resource
func (g *Generator) generateResourceConversions(resource string) string {
	var b strings.Builder

	// Derive package name from OutputDir (e.g., ".../conversion/v1alpha1" → "v1alpha1")
	convPkgName := filepath.Base(g.OutputDir)

	// Derive import paths for the parent (types) package and the rest sub-package
	parentPkg := g.outputImportPath()
	restPkg := parentPkg + "/" + convPkgName + "/rest"

	// Header
	b.WriteString("// Code generated by conversion-gen. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\n", convPkgName)

	// Imports
	b.WriteString("import (\n")
	fmt.Fprintf(&b, "\tv1alpha1 \"%s\"\n", g.CRDPackage)
	fmt.Fprintf(&b, "\t\"%s\"\n", parentPkg)
	fmt.Fprintf(&b, "\t\"%s\"\n", restPkg)
	b.WriteString(")\n\n")

	// Project function (CRD → REST)
	b.WriteString(g.generateProjectFunction(resource))
	b.WriteString("\n")

	// Unproject function (REST → CRD)
	b.WriteString(g.generateUnprojectFunction(resource))

	return b.String()
}

// generateProjectFunction generates the ProjectX function (CRD → REST)
func (g *Generator) generateProjectFunction(resource string) string {
	var b strings.Builder

	specType := resource + "Spec"
	statusType := resource + "Status"

	fmt.Fprintf(&b, "// Project%s converts CRD %s to REST (visible fields only)\n", resource, resource)
	fmt.Fprintf(&b, "func Project%s(crd *v1alpha1.%s) *rest.%s {\n", resource, resource, resource)
	b.WriteString("\tif crd == nil {\n")
	b.WriteString("\t\treturn nil\n")
	b.WriteString("\t}\n\n")

	fmt.Fprintf(&b, "\treturn &rest.%s{\n", resource)
	fmt.Fprintf(&b, "\t\tSpec:   project%s(crd.Spec),\n", specType)
	fmt.Fprintf(&b, "\t\tStatus: project%s(crd.Status),\n", statusType)
	b.WriteString("\t}\n")
	b.WriteString("}\n\n")

	// Helper for Spec
	b.WriteString(g.generateProjectSpecFunction(resource, specType))
	b.WriteString("\n")

	// Helper for Status
	b.WriteString(g.generateProjectStatusFunction(resource, statusType))

	return b.String()
}

// generateProjectSpecFunction generates projectXSpec helper
func (g *Generator) generateProjectSpecFunction(_, specType string) string {
	var b strings.Builder

	ti, exists := g.typeInfos[specType]
	if !exists {
		return ""
	}

	fmt.Fprintf(&b, "// project%s converts CRD %s to REST\n", specType, specType)
	fmt.Fprintf(&b, "func project%s(crd v1alpha1.%s) rest.%s {\n", specType, specType, specType)
	fmt.Fprintf(&b, "\treturn rest.%s{\n", specType)

	// Copy visible fields only
	for _, fi := range ti.Fields {
		if fi.Hidden {
			continue
		}

		// Check if this field is a mirror type that needs conversion
		if IsMirrorType(fi.GoName) {
			// Use auto-generated conversion helper
			mapping := GetMirrorMapping(fi.GoName)
			if mapping != nil {
				// Extract base type name (e.g., "ClusterConfiguration" from "*hypershiftv1beta1.ClusterConfiguration")
				baseType := strings.TrimPrefix(fi.GoType, "*")
				baseType = strings.TrimPrefix(baseType, "[]")
				// Remove package qualifier (e.g., "hypershiftv1beta1." or "v1alpha1.")
				if idx := strings.LastIndex(baseType, "."); idx != -1 {
					baseType = baseType[idx+1:]
				}

				// Generate conversion helper call
				// ProjectCluster converts CRD (v1beta1) to REST (v1alpha1), so we need v1beta1 → v1alpha1
				// E.g., ConvertClusterConfiguration_v1beta1_to_v1alpha1(crd.Configuration)
				fmt.Fprintf(&b, "\t\t%s: Convert%s_v1beta1_to_v1alpha1(crd.%s),\n",
					fi.GoName, baseType, fi.GoName)
				continue
			}
		}

		// Check if this is a custom type that needs conversion
		needsHelper := false
		baseType := strings.TrimPrefix(fi.GoType, "*") // Remove pointer
		baseType = strings.TrimPrefix(baseType, "[]")  // Remove slice
		if _, isCustom := g.typeInfos[baseType]; isCustom {
			needsHelper = true
		}

		if needsHelper {
			isPointer := strings.HasPrefix(fi.GoType, "*")
			isSlice := strings.HasPrefix(fi.GoType, "[]")
			if isPointer {
				fmt.Fprintf(&b, "\t\t%s: project%sPtr(crd.%s),\n", fi.GoName, baseType, fi.GoName)
			} else if isSlice {
				fmt.Fprintf(&b, "\t\t%s: project%sSlice(crd.%s),\n", fi.GoName, baseType, fi.GoName)
			} else {
				fmt.Fprintf(&b, "\t\t%s: project%s(crd.%s),\n", fi.GoName, baseType, fi.GoName)
			}
		} else {
			fmt.Fprintf(&b, "\t\t%s: crd.%s,\n", fi.GoName, fi.GoName)
		}
	}

	b.WriteString("\t}\n")
	b.WriteString("}\n")

	return b.String()
}

// generateProjectStatusFunction generates projectXStatus helper
func (g *Generator) generateProjectStatusFunction(_, statusType string) string {
	var b strings.Builder

	ti, exists := g.typeInfos[statusType]
	if !exists {
		return ""
	}

	fmt.Fprintf(&b, "// project%s converts CRD %s to REST\n", statusType, statusType)
	fmt.Fprintf(&b, "func project%s(crd v1alpha1.%s) rest.%s {\n", statusType, statusType, statusType)
	fmt.Fprintf(&b, "\treturn rest.%s{\n", statusType)

	// Copy all fields (status fields are typically all visible)
	for _, fi := range ti.Fields {
		if fi.Hidden {
			continue
		}
		fmt.Fprintf(&b, "\t\t%s: crd.%s,\n", fi.GoName, fi.GoName)
	}

	b.WriteString("\t}\n")
	b.WriteString("}\n")

	return b.String()
}

// generateUnprojectFunction generates the UnprojectX function (REST → CRD)
func (g *Generator) generateUnprojectFunction(resource string) string {
	var b strings.Builder

	specType := resource + "Spec"

	fmt.Fprintf(&b, "// Unproject%s converts REST %sSpec to CRD with service-set enrichment\n", resource, resource)
	fmt.Fprintf(&b, "func Unproject%s(spec *rest.%s, enrichment *conversion.ServiceSetFields) *v1alpha1.%s {\n", resource, specType, specType)
	b.WriteString("\tif spec == nil {\n")
	b.WriteString("\t\treturn nil\n")
	b.WriteString("\t}\n\n")

	ti, exists := g.typeInfos[specType]
	if !exists {
		fmt.Fprintf(&b, "\treturn &v1alpha1.%s{}\n", specType)
		b.WriteString("}\n")
		return b.String()
	}

	fmt.Fprintf(&b, "\tcrdSpec := &v1alpha1.%s{\n", specType)

	// Copy visible fields from REST
	b.WriteString("\t\t// Visible fields from REST request\n")
	for _, fi := range ti.Fields {
		if fi.Hidden {
			continue
		}

		// Check if this field is a mirror type that needs conversion
		if IsMirrorType(fi.GoName) {
			// Use auto-generated conversion helper (reverse direction)
			mapping := GetMirrorMapping(fi.GoName)
			if mapping != nil {
				// Extract base type name
				baseType := strings.TrimPrefix(fi.GoType, "*")
				baseType = strings.TrimPrefix(baseType, "[]")
				// Remove package qualifier
				if idx := strings.LastIndex(baseType, "."); idx != -1 {
					baseType = baseType[idx+1:]
				}

				// Generate conversion helper call
				// UnprojectCluster converts REST (v1alpha1) to CRD (v1beta1), so we need v1alpha1 → v1beta1
				// E.g., ConvertClusterConfiguration_v1alpha1_to_v1beta1(spec.Configuration)
				fmt.Fprintf(&b, "\t\t%s: Convert%s_v1alpha1_to_v1beta1(spec.%s),\n",
					fi.GoName, baseType, fi.GoName)
				continue
			}
		}

		// Check if this is a custom type that needs conversion
		needsHelper := false
		baseType := strings.TrimPrefix(fi.GoType, "*")
		baseType = strings.TrimPrefix(baseType, "[]")
		if _, isCustom := g.typeInfos[baseType]; isCustom {
			needsHelper = true
		}

		if needsHelper {
			isPointer := strings.HasPrefix(fi.GoType, "*")
			isSlice := strings.HasPrefix(fi.GoType, "[]")
			if isPointer {
				fmt.Fprintf(&b, "\t\t%s: unproject%sPtr(spec.%s),\n", fi.GoName, baseType, fi.GoName)
			} else if isSlice {
				fmt.Fprintf(&b, "\t\t%s: unproject%sSlice(spec.%s),\n", fi.GoName, baseType, fi.GoName)
			} else {
				fmt.Fprintf(&b, "\t\t%s: unproject%s(spec.%s),\n", fi.GoName, baseType, fi.GoName)
			}
		} else {
			fmt.Fprintf(&b, "\t\t%s: spec.%s,\n", fi.GoName, fi.GoName)
		}
	}

	b.WriteString("\t}\n\n")

	// Add service-set fields from enrichment
	b.WriteString("\t// Service-set fields from platform enrichment\n")
	b.WriteString("\tif enrichment != nil {\n")

	for _, fi := range ti.Fields {
		if fi.WriteMode == registry.ServiceSet {
			enrichField := g.pathToGoName(fi.FieldPath)
			fmt.Fprintf(&b, "\t\tcrdSpec.%s = enrichment.%s\n", fi.GoName, enrichField)
		}
	}

	b.WriteString("\t}\n\n")
	b.WriteString("\treturn crdSpec\n")
	b.WriteString("}\n\n")

	// Add helper functions for passthrough types
	b.WriteString(g.generatePassthroughHelpers(specType))

	return b.String()
}

// generatePassthroughHelpers generates project/unproject helpers for passthrough types
func (g *Generator) generatePassthroughHelpers(specType string) string {
	var b strings.Builder

	ti, exists := g.typeInfos[specType]
	if !exists {
		return ""
	}

	// Find custom type fields (passthrough and others like ClusterReference)
	for _, fi := range ti.Fields {
		// Check if this is a custom type (exists in our type registry)
		baseType := strings.TrimPrefix(fi.GoType, "*")
		baseType = strings.TrimPrefix(baseType, "[]")

		pti, exists := g.typeInfos[baseType]
		if !exists {
			continue
		}

		customType := baseType
		isPointer := strings.HasPrefix(fi.GoType, "*")
		isSlice := strings.HasPrefix(fi.GoType, "[]")

		// Generate value helpers if not already emitted
		if !g.emittedHelpers[customType] {
			g.emittedHelpers[customType] = true

			// Generate project helper
			fmt.Fprintf(&b, "// project%s converts CRD type to REST\n", customType)
			fmt.Fprintf(&b, "func project%s(crd v1alpha1.%s) rest.%s {\n", customType, customType, customType)
			fmt.Fprintf(&b, "\treturn rest.%s{\n", customType)

			for _, pfi := range pti.Fields {
				if !pfi.Hidden {
					// Check if this is a mirror type field
					if IsMirrorType(pfi.GoName) {
						mapping := GetMirrorMapping(pfi.GoName)
						if mapping != nil {
							// Extract base type
							fieldBaseType := strings.TrimPrefix(pfi.GoType, "*")
							fieldBaseType = strings.TrimPrefix(fieldBaseType, "[]")
							// Remove package qualifier
							if idx := strings.LastIndex(fieldBaseType, "."); idx != -1 {
								fieldBaseType = fieldBaseType[idx+1:]
							}

							// Use conversion helper (CRD v1beta1 → REST v1alpha1)
							fmt.Fprintf(&b, "\t\t%s: Convert%s_v1beta1_to_v1alpha1(crd.%s),\n",
								pfi.GoName, fieldBaseType, pfi.GoName)
							continue
						}
					}
					fmt.Fprintf(&b, "\t\t%s: crd.%s,\n", pfi.GoName, pfi.GoName)
				}
			}

			b.WriteString("\t}\n")
			b.WriteString("}\n\n")

			// Generate unproject helper
			fmt.Fprintf(&b, "// unproject%s converts REST type to CRD\n", customType)
			fmt.Fprintf(&b, "func unproject%s(rest rest.%s) v1alpha1.%s {\n", customType, customType, customType)
			fmt.Fprintf(&b, "\treturn v1alpha1.%s{\n", customType)

			for _, pfi := range pti.Fields {
				if !pfi.Hidden {
					// Check if this is a mirror type field
					if IsMirrorType(pfi.GoName) {
						mapping := GetMirrorMapping(pfi.GoName)
						if mapping != nil {
							// Extract base type
							fieldBaseType := strings.TrimPrefix(pfi.GoType, "*")
							fieldBaseType = strings.TrimPrefix(fieldBaseType, "[]")
							// Remove package qualifier
							if idx := strings.LastIndex(fieldBaseType, "."); idx != -1 {
								fieldBaseType = fieldBaseType[idx+1:]
							}

							// Use conversion helper (REST v1alpha1 → CRD v1beta1)
							fmt.Fprintf(&b, "\t\t%s: Convert%s_v1alpha1_to_v1beta1(rest.%s),\n",
								pfi.GoName, fieldBaseType, pfi.GoName)
							continue
						}
					}
					fmt.Fprintf(&b, "\t\t%s: rest.%s,\n", pfi.GoName, pfi.GoName)
				}
			}

			b.WriteString("\t}\n")
			b.WriteString("}\n\n")
		}

		// Generate pointer wrappers if needed
		if isPointer && !g.emittedHelpers[customType+"Ptr"] {
			g.emittedHelpers[customType+"Ptr"] = true

			fmt.Fprintf(&b, "func project%sPtr(crd *v1alpha1.%s) *rest.%s {\n", customType, customType, customType)
			b.WriteString("\tif crd == nil {\n\t\treturn nil\n\t}\n")
			fmt.Fprintf(&b, "\tv := project%s(*crd)\n", customType)
			b.WriteString("\treturn &v\n")
			b.WriteString("}\n\n")

			fmt.Fprintf(&b, "func unproject%sPtr(r *rest.%s) *v1alpha1.%s {\n", customType, customType, customType)
			b.WriteString("\tif r == nil {\n\t\treturn nil\n\t}\n")
			fmt.Fprintf(&b, "\tv := unproject%s(*r)\n", customType)
			b.WriteString("\treturn &v\n")
			b.WriteString("}\n\n")
		}

		// Generate slice wrappers if needed
		if isSlice && !g.emittedHelpers[customType+"Slice"] {
			g.emittedHelpers[customType+"Slice"] = true

			fmt.Fprintf(&b, "func project%sSlice(crd []v1alpha1.%s) []rest.%s {\n", customType, customType, customType)
			b.WriteString("\tif crd == nil {\n\t\treturn nil\n\t}\n")
			fmt.Fprintf(&b, "\tout := make([]rest.%s, len(crd))\n", customType)
			b.WriteString("\tfor i := range crd {\n")
			fmt.Fprintf(&b, "\t\tout[i] = project%s(crd[i])\n", customType)
			b.WriteString("\t}\n")
			b.WriteString("\treturn out\n")
			b.WriteString("}\n\n")

			fmt.Fprintf(&b, "func unproject%sSlice(r []rest.%s) []v1alpha1.%s {\n", customType, customType, customType)
			b.WriteString("\tif r == nil {\n\t\treturn nil\n\t}\n")
			fmt.Fprintf(&b, "\tout := make([]v1alpha1.%s, len(r))\n", customType)
			b.WriteString("\tfor i := range r {\n")
			fmt.Fprintf(&b, "\t\tout[i] = unproject%s(r[i])\n", customType)
			b.WriteString("\t}\n")
			b.WriteString("\treturn out\n")
			b.WriteString("}\n\n")
		}
	}

	return b.String()
}

// ensureDir creates a directory if it doesn't exist
func (g *Generator) ensureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

// outputImportPath returns the Go import path for the parent output package.
// If OutputPackage is set explicitly, it is used directly. Otherwise, the path
// is derived from OutputDir using a convention-based default.
func (g *Generator) outputImportPath() string {
	if g.OutputPackage != "" {
		return g.OutputPackage
	}
	return "github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/conversion"
}

// writeFile writes content to a file, creating parent directories as needed
func (g *Generator) writeFile(relativePath, content string) error {
	fullPath := filepath.Join(g.OutputDir, relativePath)

	if err := g.ensureDir(filepath.Dir(fullPath)); err != nil {
		return err
	}

	return os.WriteFile(fullPath, []byte(content), 0644)
}
