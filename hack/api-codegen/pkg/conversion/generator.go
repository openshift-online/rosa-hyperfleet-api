package conversion

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/template"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/registry"
)

var clientgenMarkerRE = regexp.MustCompile(`\+genclient\b|\+bridge:|\+resourceName=`)

// Generator generates REST types and conversion functions from CRD types.
type Generator struct {
	APIVersion    string
	CRDPackage    string
	OutputDir     string
	OutputPackage string
	InputDirs     []string

	// RESTOutputDir overrides where REST type files are written.
	// Defaults to OutputDir + "/rest" when empty.
	RESTOutputDir string
	// RESTImportPath is the Go import path for the REST types package.
	// Defaults to OutputPackage + "/<output-dir-base>/rest" when empty.
	RESTImportPath string

	knownTypes     map[string]bool
	typeInfos      map[string]*typeInfo
	namedTypes     map[string]*namedTypeInfo
	constsByType   map[string][]*constEntry
	emittedHelpers map[string]bool
}

type typeInfo struct {
	Name       string
	StructType *ast.StructType
	Doc        *ast.CommentGroup
	Fields     []*fieldInfo
	Markers    []string
	Embeds     []string
}

type namedTypeInfo struct {
	Name       string
	Underlying string
	Doc        *ast.CommentGroup
}

type constEntry struct {
	Name  string
	Value string
}

type fieldInfo struct {
	GoName    string
	JSONName  string
	GoType    string
	FieldPath string
	Field     *ast.Field
	Doc       *ast.CommentGroup
	Hidden    bool
	WriteMode registry.WriteMode
}

// NewGenerator creates a new conversion generator.
func NewGenerator(apiVersion, crdPackage string, inputDirs []string, outputDir string) *Generator {
	return &Generator{
		APIVersion:     apiVersion,
		CRDPackage:     crdPackage,
		InputDirs:      inputDirs,
		OutputDir:      outputDir,
		knownTypes:     make(map[string]bool),
		typeInfos:      make(map[string]*typeInfo),
		namedTypes:     make(map[string]*namedTypeInfo),
		constsByType:   make(map[string][]*constEntry),
		emittedHelpers: make(map[string]bool),
	}
}

func (g *Generator) restOutputDir() string {
	if g.RESTOutputDir != "" {
		return g.RESTOutputDir
	}
	return filepath.Join(g.OutputDir, "rest")
}

func (g *Generator) restImportPath() string {
	if g.RESTImportPath != "" {
		return g.RESTImportPath
	}
	convPkgName := filepath.Base(g.OutputDir)
	return g.outputImportPath() + "/" + convPkgName + "/rest"
}

func (g *Generator) restPackageName() string {
	return filepath.Base(g.restOutputDir())
}

// Generate runs all three generation phases.
func (g *Generator) Generate() error {
	if err := g.parseTypes(); err != nil {
		return fmt.Errorf("parsing types: %w", err)
	}

	if err := g.generateRESTTypes(); err != nil {
		return fmt.Errorf("generating REST types: %w", err)
	}

	if err := g.generateServiceSetFields(); err != nil {
		return fmt.Errorf("generating ServiceSetFields: %w", err)
	}

	if err := g.generateConversionFunctions(); err != nil {
		return fmt.Errorf("generating conversion functions: %w", err)
	}

	return nil
}

// --- Parsing ---

func (g *Generator) parseTypes() error {
	for _, dir := range g.InputDirs {
		fset := token.NewFileSet()

		//nolint:staticcheck // ParseDir is sufficient for our use case
		pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
			name := fi.Name()
			return !strings.HasSuffix(name, "_test.go") &&
				(!strings.HasPrefix(name, "zz_generated") || name == "zz_generated.passthrough.go")
		}, parser.ParseComments)
		if err != nil {
			return fmt.Errorf("parsing directory %s: %w", dir, err)
		}

		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				for _, decl := range file.Decls {
					genDecl, ok := decl.(*ast.GenDecl)
					if !ok {
						continue
					}

					switch genDecl.Tok {
					case token.TYPE:
						for _, spec := range genDecl.Specs {
							typeSpec, ok := spec.(*ast.TypeSpec)
							if !ok || !typeSpec.Name.IsExported() {
								continue
							}

							typeName := typeSpec.Name.Name
							g.knownTypes[typeName] = true

							structType, ok := typeSpec.Type.(*ast.StructType)
							if ok {
								g.typeInfos[typeName] = g.parseStructType(typeName, structType, genDecl.Doc)
								continue
							}

							if ident, ok := typeSpec.Type.(*ast.Ident); ok {
								g.namedTypes[typeName] = &namedTypeInfo{
									Name:       typeName,
									Underlying: ident.Name,
									Doc:        genDecl.Doc,
								}
							}
						}

					case token.CONST:
						for _, spec := range genDecl.Specs {
							valueSpec, ok := spec.(*ast.ValueSpec)
							if !ok || len(valueSpec.Names) == 0 {
								continue
							}
							constTypeName := ""
							if valueSpec.Type != nil {
								if ident, ok := valueSpec.Type.(*ast.Ident); ok {
									constTypeName = ident.Name
								}
							}
							if constTypeName == "" {
								continue
							}
							for i, name := range valueSpec.Names {
								if !name.IsExported() || i >= len(valueSpec.Values) {
									continue
								}
								lit, ok := valueSpec.Values[i].(*ast.BasicLit)
								if !ok {
									continue
								}
								g.constsByType[constTypeName] = append(g.constsByType[constTypeName], &constEntry{
									Name:  name.Name,
									Value: lit.Value,
								})
							}
						}
					}
				}

				g.extractClientMarkers(file)
			}
		}
	}
	return nil
}

// extractClientMarkers scans all comment groups in a file for +genclient and
// +bridge:* markers that appear in floating comment blocks (separated by a blank
// line from the type's doc comment). It associates each marker set with the
// nearest following type declaration, mirroring the convention used by
// client-gen and bridge-gen.
func (g *Generator) extractClientMarkers(file *ast.File) {
	type typePos struct {
		name string
		pos  token.Pos
	}
	var typeDecls []typePos
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			typeDecls = append(typeDecls, typePos{name: ts.Name.Name, pos: gd.Pos()})
		}
	}
	sort.Slice(typeDecls, func(i, j int) bool { return typeDecls[i].pos < typeDecls[j].pos })

	for _, cg := range file.Comments {
		var markers []string
		for _, c := range cg.List {
			if clientgenMarkerRE.MatchString(c.Text) {
				markers = append(markers, strings.TrimSpace(strings.TrimPrefix(c.Text, "//")))
			}
		}
		if len(markers) == 0 {
			continue
		}
		cgEnd := cg.End()
		for _, td := range typeDecls {
			if td.pos <= cgEnd {
				continue
			}
			if ti, ok := g.typeInfos[td.name]; ok {
				ti.Markers = append(ti.Markers, markers...)
			}
			break
		}
	}
}

func (g *Generator) parseStructType(typeName string, structType *ast.StructType, doc *ast.CommentGroup) *typeInfo {
	ti := &typeInfo{Name: typeName, StructType: structType, Doc: doc}
	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 {
			ti.Embeds = append(ti.Embeds, g.exprToString(field.Type))
			continue
		}
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			fi := g.parseField(typeName, field, name)
			if fi != nil {
				ti.Fields = append(ti.Fields, fi)
			}
		}
	}
	return ti
}

func (g *Generator) parseField(typeName string, field *ast.Field, name *ast.Ident) *fieldInfo {
	goName := name.Name
	jsonName := g.extractJSONTag(field)
	if jsonName == "" || jsonName == "-" {
		return nil
	}

	fieldPath := g.buildFieldPath(typeName, jsonName)

	// Look up field metadata from all types in the registry
	// (we don't know the CRD owner type during conversion code generation)
	var meta registry.FieldMeta
	exists := false
	for _, typeFields := range registry.FieldRegistry {
		if m, ok := typeFields[fieldPath]; ok {
			meta = m
			exists = true
			break
		}
	}

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

func (g *Generator) buildFieldPath(typeName, jsonName string) string {
	switch typeName {
	case "KubeletConfig":
		return "kubelet." + jsonName
	case "MachineConfigSpec":
		return "machineConfig." + jsonName
	case "ClusterConfiguration":
		return jsonName
	}
	switch {
	case strings.HasSuffix(typeName, "Spec"):
		return "spec." + jsonName
	case strings.HasSuffix(typeName, "Status"):
		return "status." + jsonName
	case strings.Contains(typeName, "Passthrough"):
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

func (g *Generator) extractJSONTag(field *ast.Field) string {
	if field.Tag == nil {
		return ""
	}
	tag := strings.Trim(field.Tag.Value, "`")
	for _, part := range strings.Fields(tag) {
		if strings.HasPrefix(part, "json:") {
			jsonTag := strings.Trim(strings.TrimPrefix(part, "json:"), "\"")
			if idx := strings.Index(jsonTag, ","); idx >= 0 {
				return jsonTag[:idx]
			}
			return jsonTag
		}
	}
	return ""
}

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

// --- Import detection (replaces hard-coded type lists) ---

// importInfo tracks which external packages a set of fields references.
type importInfo struct {
	NeedsMetav1     bool
	NeedsHyperShift bool
	NeedsV1alpha1   bool
	NeedsCorev1     bool
	NeedsConfigv1   bool
	NeedsRuntime    bool
}

// detectImports scans a list of Go type strings and determines which
// import aliases are required. This replaces the hard-coded type lists
// (hypershiftTypes, v1alpha1Types) by deriving imports from the AST.
func (g *Generator) detectImports(goTypes []string) importInfo {
	var info importInfo
	for _, goType := range goTypes {
		base := strings.TrimPrefix(goType, "*")
		base = strings.TrimPrefix(base, "[]")

		if strings.Contains(base, "metav1.") {
			info.NeedsMetav1 = true
		}
		if strings.Contains(base, "hypershiftv1beta1.") {
			info.NeedsHyperShift = true
		}
		if strings.Contains(base, "v1alpha1.") {
			info.NeedsV1alpha1 = true
		}
		if strings.Contains(base, "corev1.") {
			info.NeedsCorev1 = true
		}
		if strings.Contains(base, "configv1.") {
			info.NeedsConfigv1 = true
		}
		if strings.Contains(base, "runtime.") {
			info.NeedsRuntime = true
		}

		// Check if the unqualified type is from the CRD package
		if !strings.Contains(base, ".") && !g.isBuiltinType(base) {
			if g.knownTypes[base] {
				info.NeedsV1alpha1 = true
			}
		}
	}
	return info
}

// qualifyType adds package qualifiers for types that are defined in other
// packages. Instead of hard-coded lists, this derives the qualification from
// what the AST parser found.
func (g *Generator) qualifyType(goType string) string {
	isPointer := strings.HasPrefix(goType, "*")
	isSlice := strings.HasPrefix(goType, "[]")
	base := goType
	base = strings.TrimPrefix(base, "*")
	base = strings.TrimPrefix(base, "[]")

	if strings.Contains(base, ".") {
		return goType // Already qualified
	}
	if g.isBuiltinType(base) {
		return goType
	}

	// If the type exists in the CRD package, qualify with v1alpha1
	if g.knownTypes[base] {
		qualified := "v1alpha1." + base
		if isPointer {
			return "*" + qualified
		}
		if isSlice {
			return "[]" + qualified
		}
		return qualified
	}

	return goType
}

// qualifyTypeForREST is like qualifyType but skips types that are also
// generated in the REST package (they're in the same package, no qualifier needed).
func (g *Generator) qualifyTypeForREST(goType string, restTypeSet map[string]bool) string {
	base := goType
	base = strings.TrimPrefix(base, "*")
	base = strings.TrimPrefix(base, "[]")

	if strings.Contains(base, ".") {
		return goType
	}
	if g.isBuiltinType(base) {
		return goType
	}

	if restTypeSet[base] {
		return goType // Same REST package, no qualifier
	}

	return g.qualifyType(goType)
}

func (g *Generator) isBuiltinType(t string) bool {
	switch t {
	case "string", "bool", "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "byte", "rune", "error", "interface{}":
		return true
	}
	return false
}

// --- Phase 1: REST type generation ---

// restTypeData feeds the REST type template.
type restTypeData struct {
	PackageName string
	TypeName    string
	DocComment  string
	Fields      []restFieldData
	Embeds      []restEmbedData
	IsRoot      bool
	Imports     importInfo
	CRDPackage  string
}

type restFieldData struct {
	GoName  string
	GoType  string
	JSONTag string
	Comment string
}

type restEmbedData struct {
	GoType  string
	JSONTag string
}

var restTypeTmpl = template.Must(template.New("restType").Parse(`// Code generated by conversion-gen. DO NOT EDIT.

package {{ .PackageName }}

{{ if or .Imports.NeedsMetav1 .Imports.NeedsHyperShift .Imports.NeedsV1alpha1 .Imports.NeedsCorev1 .Imports.NeedsConfigv1 .Imports.NeedsRuntime -}}
import (
{{- if .Imports.NeedsConfigv1 }}
	configv1 "github.com/openshift/api/config/v1"
{{- end }}
{{- if .Imports.NeedsHyperShift }}
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
{{- end }}
{{- if .Imports.NeedsCorev1 }}
	corev1 "k8s.io/api/core/v1"
{{- end }}
{{- if .Imports.NeedsMetav1 }}
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
{{- end }}
{{- if .Imports.NeedsRuntime }}
	"k8s.io/apimachinery/pkg/runtime"
{{- end }}
{{- if .Imports.NeedsV1alpha1 }}
	v1alpha1 "{{ .CRDPackage }}"
{{- end }}
)
{{ end }}
{{ .DocComment }}
type {{ .TypeName }} struct {
{{- range .Embeds }}
	{{ .GoType }} ` + "`" + `json:"{{ .JSONTag }}"` + "`" + `
{{- end }}
{{- range .Fields }}
{{- if .Comment }}
	{{ .Comment }}
{{- end }}
	{{ .GoName }} {{ .GoType }} ` + "`" + `json:"{{ .JSONTag }}"` + "`" + `
{{- end }}
}
{{ if .IsRoot }}
// +kubebuilder:object:root=true
type {{ .TypeName }}List struct {
	metav1.TypeMeta ` + "`" + `json:",inline"` + "`" + `
	metav1.ListMeta ` + "`" + `json:"metadata,omitempty"` + "`" + `
	Items []{{ .TypeName }} ` + "`" + `json:"items"` + "`" + `
}
{{ end }}
`))

// restConstData feeds the constants template.
type restConstData struct {
	PackageName string
	Types       []restConstTypeData
}

type restConstTypeData struct {
	TypeName   string
	Underlying string
	DocComment string
	Consts     []restConstEntryData
}

type restConstEntryData struct {
	Name  string
	Value string
}

func (g *Generator) generateRESTTypes() error {
	restDir := g.restOutputDir()
	if err := g.ensureDir(restDir); err != nil {
		return err
	}

	// Discover resource types dynamically: types with both Spec and Status subtypes
	resourceTypes := g.discoverResourceTypes()

	// Identify root resource types (CRDs with TypeMeta/ObjectMeta wrapper)
	rootTypes := make(map[string]bool)
	for typeName := range g.typeInfos {
		if g.isCRDResource(typeName) {
			rootTypes[typeName] = true
		}
	}

	// Build the full set of types being generated as REST types (including passthrough)
	restTypeSet := make(map[string]bool)
	for _, t := range resourceTypes {
		restTypeSet[t] = true
	}
	for typeName := range g.typeInfos {
		if strings.Contains(typeName, "Passthrough") {
			restTypeSet[typeName] = true
		}
	}

	// Find named types referenced by REST type fields and add them to restTypeSet
	referencedNamedTypes := g.findReferencedNamedTypes(restTypeSet)
	for _, nt := range referencedNamedTypes {
		restTypeSet[nt.Name] = true
	}

	for _, typeName := range resourceTypes {
		ti, exists := g.typeInfos[typeName]
		if !exists {
			continue
		}

		code, err := g.renderRESTType(ti, restTypeSet, rootTypes[typeName])
		if err != nil {
			return fmt.Errorf("rendering REST type %s: %w", typeName, err)
		}

		filename := strings.ToLower(typeName) + "_types.go"
		if err := g.writeRESTFile(filename, code); err != nil {
			return fmt.Errorf("writing REST type %s: %w", typeName, err)
		}
	}

	// Also generate passthrough types
	for typeName := range g.typeInfos {
		if strings.Contains(typeName, "Passthrough") {
			ti := g.typeInfos[typeName]
			code, err := g.renderRESTType(ti, restTypeSet, false)
			if err != nil {
				return fmt.Errorf("rendering REST type %s: %w", typeName, err)
			}
			filename := strings.ToLower(typeName) + "_types.go"
			if err := g.writeRESTFile(filename, code); err != nil {
				return fmt.Errorf("writing REST type %s: %w", typeName, err)
			}
		}
	}

	// Generate constants file for referenced named types
	if len(referencedNamedTypes) > 0 {
		if err := g.generateRESTConstants(referencedNamedTypes); err != nil {
			return fmt.Errorf("generating REST constants: %w", err)
		}
	}

	return nil
}

// findReferencedNamedTypes returns named types (with const blocks) that are
// referenced by fields in the REST type set.
func (g *Generator) findReferencedNamedTypes(restTypeSet map[string]bool) []*namedTypeInfo {
	needed := make(map[string]bool)
	for typeName := range restTypeSet {
		ti, ok := g.typeInfos[typeName]
		if !ok {
			continue
		}
		for _, fi := range ti.Fields {
			if fi.Hidden {
				continue
			}
			base := strings.TrimPrefix(fi.GoType, "*")
			base = strings.TrimPrefix(base, "[]")
			if _, isNamed := g.namedTypes[base]; isNamed {
				if _, hasConsts := g.constsByType[base]; hasConsts {
					needed[base] = true
				}
			}
		}
	}

	result := make([]*namedTypeInfo, 0, len(needed))
	for name := range needed {
		result = append(result, g.namedTypes[name])
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

func (g *Generator) generateRESTConstants(namedTypes []*namedTypeInfo) error {
	var types []restConstTypeData
	for _, nt := range namedTypes {
		docComment := ""
		if nt.Doc != nil {
			lines := strings.Split(strings.TrimSpace(nt.Doc.Text()), "\n")
			var docLines []string
			for _, line := range lines {
				if !strings.HasPrefix(line, "//") {
					docLines = append(docLines, "// "+line)
				} else {
					docLines = append(docLines, line)
				}
			}
			docComment = strings.Join(docLines, "\n")
		}

		var consts []restConstEntryData
		for _, ce := range g.constsByType[nt.Name] {
			consts = append(consts, restConstEntryData{
				Name:  ce.Name,
				Value: ce.Value,
			})
		}

		types = append(types, restConstTypeData{
			TypeName:   nt.Name,
			Underlying: nt.Underlying,
			DocComment: docComment,
			Consts:     consts,
		})
	}

	data := restConstData{
		PackageName: g.restPackageName(),
		Types:       types,
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "// Code generated by conversion-gen. DO NOT EDIT.\n\npackage %s\n", data.PackageName)
	for _, td := range data.Types {
		if td.DocComment != "" {
			fmt.Fprintf(&buf, "\n%s\n", td.DocComment)
		} else {
			fmt.Fprintln(&buf)
		}
		fmt.Fprintf(&buf, "type %s %s\n\nconst (\n", td.TypeName, td.Underlying)
		for _, c := range td.Consts {
			fmt.Fprintf(&buf, "\t%s %s = %s\n", c.Name, td.TypeName, c.Value)
		}
		fmt.Fprintln(&buf, ")")
	}

	return g.writeRESTFile("constants.go", buf.String())
}

// isCRDResource returns true if typeName is a top-level CRD resource. It
// requires the wrapper struct to embed metav1.TypeMeta and metav1.ObjectMeta,
// have sibling FooSpec/FooStatus types, AND declare Spec and Status fields
// whose GoType values match those sibling types. Types that only have
// Spec+Status sub-types but no wrapper (e.g. ControlPlaneUpgradePolicy) are
// nested sub-types, not top-level CRDs.
func (g *Generator) isCRDResource(typeName string) bool {
	ti, exists := g.typeInfos[typeName]
	if !exists {
		return false
	}
	specType := typeName + "Spec"
	statusType := typeName + "Status"
	if _, ok := g.typeInfos[specType]; !ok {
		return false
	}
	if _, ok := g.typeInfos[statusType]; !ok {
		return false
	}
	hasTypeMeta := false
	hasObjectMeta := false
	for _, embed := range ti.Embeds {
		if embed == "metav1.TypeMeta" {
			hasTypeMeta = true
		}
		if embed == "metav1.ObjectMeta" {
			hasObjectMeta = true
		}
	}
	if !hasTypeMeta || !hasObjectMeta {
		return false
	}
	hasSpecField := false
	hasStatusField := false
	for _, fi := range ti.Fields {
		if fi.GoName == "Spec" && fi.GoType == specType {
			hasSpecField = true
		}
		if fi.GoName == "Status" && fi.GoType == statusType {
			hasStatusField = true
		}
	}
	return hasSpecField && hasStatusField
}

// discoverResourceTypes finds types that form CRD resources (have a wrapper
// struct with TypeMeta/ObjectMeta embeds and matching Spec and Status subtypes)
// plus any types referenced by their visible fields.
func (g *Generator) discoverResourceTypes() []string {
	typeSet := make(map[string]bool)

	for typeName := range g.typeInfos {
		if g.isCRDResource(typeName) {
			typeSet[typeName] = true
			typeSet[typeName+"Spec"] = true
			typeSet[typeName+"Status"] = true
		}
	}

	// Walk visible fields and add referenced types that are in our type set
	g.addReferencedTypes(typeSet)

	result := make([]string, 0, len(typeSet))
	for t := range typeSet {
		result = append(result, t)
	}
	sort.Strings(result)
	return result
}

func (g *Generator) addReferencedTypes(typeSet map[string]bool) {
	changed := true
	for changed {
		changed = false
		for typeName := range typeSet {
			ti, ok := g.typeInfos[typeName]
			if !ok {
				continue
			}
			for _, fi := range ti.Fields {
				if fi.Hidden {
					continue
				}
				base := strings.TrimPrefix(fi.GoType, "*")
				base = strings.TrimPrefix(base, "[]")
				if strings.Contains(base, ".") {
					continue // Already qualified, external type
				}
				if _, ok := g.typeInfos[base]; ok && !typeSet[base] {
					typeSet[base] = true
					changed = true
				}
			}
		}
	}
}

func (g *Generator) renderRESTType(ti *typeInfo, restTypeSet map[string]bool, isRoot bool) (string, error) {
	var visibleFields []restFieldData
	var goTypes []string

	for _, fi := range ti.Fields {
		if fi.Hidden {
			continue
		}
		qualifiedType := g.qualifyTypeForREST(fi.GoType, restTypeSet)
		// Only track types for import detection that aren't REST-local
		base := strings.TrimPrefix(fi.GoType, "*")
		base = strings.TrimPrefix(base, "[]")
		if !restTypeSet[base] {
			goTypes = append(goTypes, qualifiedType)
		}

		jsonTag := fi.JSONName
		if fi.Field.Tag != nil {
			tag := strings.Trim(fi.Field.Tag.Value, "`")
			for _, part := range strings.Fields(tag) {
				if strings.HasPrefix(part, "json:") {
					jsonTag = strings.Trim(strings.TrimPrefix(part, "json:"), "\"")
					break
				}
			}
		}

		comment := ""
		if fi.Doc != nil {
			lines := strings.Split(strings.TrimSpace(fi.Doc.Text()), "\n")
			var commentLines []string
			for _, line := range lines {
				if line != "" {
					if !strings.HasPrefix(line, "//") {
						commentLines = append(commentLines, "// "+line)
					} else {
						commentLines = append(commentLines, line)
					}
				}
			}
			comment = strings.Join(commentLines, "\n\t")
		}

		visibleFields = append(visibleFields, restFieldData{
			GoName:  fi.GoName,
			GoType:  qualifiedType,
			JSONTag: jsonTag,
			Comment: comment,
		})
	}

	docComment := ""
	if ti.Doc != nil {
		lines := strings.Split(strings.TrimSpace(ti.Doc.Text()), "\n")
		var docLines []string
		for _, line := range lines {
			if !strings.HasPrefix(line, "//") {
				docLines = append(docLines, "// "+line)
			} else {
				docLines = append(docLines, line)
			}
		}
		docComment = strings.Join(docLines, "\n")
	} else {
		docComment = fmt.Sprintf("// %s is the REST representation of %s (visible fields only)", ti.Name, ti.Name)
	}

	if isRoot {
		docComment += "\n// +kubebuilder:object:root=true\n// +kubebuilder:resource:scope=Namespaced\n// +kubebuilder:subresource:status"
		for _, m := range ti.Markers {
			docComment += "\n// " + m
		}
	}

	var embeds []restEmbedData
	if isRoot {
		embeds = []restEmbedData{
			{GoType: "metav1.TypeMeta", JSONTag: ",inline"},
			{GoType: "metav1.ObjectMeta", JSONTag: "metadata,omitempty"},
		}
	}

	imports := g.detectImports(goTypes)
	if isRoot {
		imports.NeedsMetav1 = true
	}

	data := restTypeData{
		PackageName: g.restPackageName(),
		TypeName:    ti.Name,
		DocComment:  docComment,
		Fields:      visibleFields,
		Embeds:      embeds,
		IsRoot:      isRoot,
		Imports:     imports,
		CRDPackage:  g.CRDPackage,
	}

	var buf bytes.Buffer
	if err := restTypeTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing template: %w", err)
	}

	return buf.String(), nil
}

// --- Phase 2: ServiceSetFields ---

type serviceSetData struct {
	PackageName string
	CRDPackage  string
	Fields      []serviceSetFieldData
	Imports     importInfo
}

type serviceSetFieldData struct {
	GoName    string
	GoType    string
	JSONTag   string
	FieldPath string
}

var serviceSetTmpl = template.Must(template.New("serviceSet").Parse(`// Code generated by conversion-gen. DO NOT EDIT.

package {{ .PackageName }}

{{ if or .Imports.NeedsCorev1 .Imports.NeedsConfigv1 .Imports.NeedsMetav1 .Imports.NeedsHyperShift .Imports.NeedsV1alpha1 -}}
import (
{{- if .Imports.NeedsConfigv1 }}
	configv1 "github.com/openshift/api/config/v1"
{{- end }}
{{- if .Imports.NeedsHyperShift }}
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
{{- end }}
{{- if .Imports.NeedsCorev1 }}
	corev1 "k8s.io/api/core/v1"
{{- end }}
{{- if .Imports.NeedsMetav1 }}
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
{{- end }}
{{- if .Imports.NeedsV1alpha1 }}
	v1alpha1 "{{ .CRDPackage }}"
{{- end }}
)
{{ end }}
// ServiceSetFields contains platform-managed fields injected during UnprojectX conversions
type ServiceSetFields struct {
{{- range .Fields }}
	// {{ .GoName }} is service-set (platform-managed, hidden from API)
	{{ .GoName }} {{ .GoType }} ` + "`" + `json:"{{ .JSONTag }},omitempty"` + "`" + `
{{- end }}
}
`))

func (g *Generator) generateServiceSetFields() error {
	type ssField struct {
		GoName    string
		GoType    string
		JSONTag   string
		FieldPath string
	}

	fieldsMap := make(map[string]ssField)
	// Iterate through all types in the typed registry
	for _, fields := range registry.FieldRegistry {
		for path, meta := range fields {
			if meta.WriteMode == registry.ServiceSet {
				goName := g.pathToGoName(path)
				goType := g.inferTypeFromPath(path)
				jsonTag := g.pathToJSONTag(path)

				if existing, exists := fieldsMap[jsonTag]; exists {
					if len(goType) > len(existing.GoType) {
						fieldsMap[jsonTag] = ssField{GoName: goName, GoType: goType, JSONTag: jsonTag, FieldPath: path}
					}
				} else {
					fieldsMap[jsonTag] = ssField{GoName: goName, GoType: goType, JSONTag: jsonTag, FieldPath: path}
				}
			}
		}
	}

	var fields []serviceSetFieldData
	var goTypes []string
	for _, f := range fieldsMap {
		qt := g.qualifyType(f.GoType)
		goTypes = append(goTypes, qt)
		fields = append(fields, serviceSetFieldData{
			GoName:    f.GoName,
			GoType:    qt,
			JSONTag:   f.JSONTag,
			FieldPath: f.FieldPath,
		})
	}

	sort.Slice(fields, func(i, j int) bool {
		return fields[i].GoName < fields[j].GoName
	})

	typesDir := filepath.Dir(g.OutputDir)
	pkgName := filepath.Base(typesDir)

	data := serviceSetData{
		PackageName: pkgName,
		CRDPackage:  g.CRDPackage,
		Fields:      fields,
		Imports:     g.detectImports(goTypes),
	}

	var buf bytes.Buffer
	if err := serviceSetTmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("executing service-set template: %w", err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		formatted = buf.Bytes()
	}

	typesPath := filepath.Join(typesDir, "types.go")
	if err := g.ensureDir(typesDir); err != nil {
		return err
	}
	return os.WriteFile(typesPath, formatted, 0644)
}

func (g *Generator) pathToGoName(path string) string {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return ""
	}
	lastPart := parts[len(parts)-1]
	lastPart = strings.ReplaceAll(lastPart, "Id", "ID")
	lastPart = strings.ReplaceAll(lastPart, "Arn", "ARN")
	if len(lastPart) > 0 && lastPart[0] >= 'a' && lastPart[0] <= 'z' {
		lastPart = strings.ToUpper(string(lastPart[0])) + lastPart[1:]
	}
	return lastPart
}

func (g *Generator) pathToJSONTag(path string) string {
	parts := strings.Split(path, ".")
	if len(parts) == 0 {
		return path
	}
	return parts[len(parts)-1]
}

func (g *Generator) inferTypeFromPath(path string) string {
	for _, ti := range g.typeInfos {
		for _, fi := range ti.Fields {
			if fi.FieldPath == path {
				return fi.GoType
			}
		}
	}
	return "string"
}

// --- Phase 3: JSON-roundtrip conversion functions ---

type conversionData struct {
	PackageName  string
	CRDPackage   string
	ParentPkg    string
	RESTPkg      string
	Resource     string
	SpecType     string
	StatusType   string
	SpecFields   []convFieldData
	StatusFields []convFieldData
	MirrorTypes  []mirrorConvData
}

type convFieldData struct {
	GoName     string
	IsMirror   bool
	MirrorBase string
}

type mirrorConvData struct {
	BaseType string
}

var conversionTmpl = template.Must(template.New("conversion").Parse(`// Code generated by conversion-gen. DO NOT EDIT.

package {{ .PackageName }}

import (
	"encoding/json"

	v1alpha1 "{{ .CRDPackage }}"
	"{{ .ParentPkg }}"
	rest "{{ .RESTPkg }}"
)

// Project{{ .Resource }} converts CRD {{ .Resource }} to REST (visible fields only).
// Uses JSON roundtrip: marshal CRD → unmarshal into REST, so hidden fields are
// automatically dropped (REST types lack them).
func Project{{ .Resource }}(crd *v1alpha1.{{ .Resource }}) *rest.{{ .Resource }} {
	if crd == nil {
		return nil
	}

	spec := project{{ .SpecType }}(crd.Spec)
	status := project{{ .StatusType }}(crd.Status)
	return &rest.{{ .Resource }}{
		TypeMeta:   crd.TypeMeta,
		ObjectMeta: crd.ObjectMeta,
		Spec:       spec,
		Status:     status,
	}
}

func project{{ .SpecType }}(crd v1alpha1.{{ .SpecType }}) rest.{{ .SpecType }} {
	data, _ := json.Marshal(crd)
	var out rest.{{ .SpecType }}
	_ = json.Unmarshal(data, &out)
{{- range .SpecFields }}
{{- if .IsMirror }}
	out.{{ .GoName }} = Convert{{ .MirrorBase }}_v1beta1_to_v1alpha1(crd.{{ .GoName }})
{{- end }}
{{- end }}
	return out
}

func project{{ .StatusType }}(crd v1alpha1.{{ .StatusType }}) rest.{{ .StatusType }} {
	data, _ := json.Marshal(crd)
	var out rest.{{ .StatusType }}
	_ = json.Unmarshal(data, &out)
	return out
}

// Unproject{{ .Resource }} converts REST {{ .SpecType }} to CRD with service-set enrichment.
// Uses JSON roundtrip: marshal REST → unmarshal into CRD, then overlay service-set fields.
func Unproject{{ .Resource }}(spec *rest.{{ .SpecType }}, enrichment *conversion.ServiceSetFields) *v1alpha1.{{ .SpecType }} {
	if spec == nil {
		return nil
	}

	data, _ := json.Marshal(spec)
	var crdSpec v1alpha1.{{ .SpecType }}
	_ = json.Unmarshal(data, &crdSpec)
{{- range .SpecFields }}
{{- if .IsMirror }}
	crdSpec.{{ .GoName }} = Convert{{ .MirrorBase }}_v1alpha1_to_v1beta1(spec.{{ .GoName }})
{{- end }}
{{- end }}

	if enrichment != nil {
		ssData, _ := json.Marshal(enrichment)
		_ = json.Unmarshal(ssData, &crdSpec)
	}

	return &crdSpec
}
{{ range .MirrorTypes }}
// Convert{{ .BaseType }}_v1beta1_to_v1alpha1 converts upstream HyperShift to HyperFleet mirror type via JSON roundtrip.
func Convert{{ .BaseType }}_v1beta1_to_v1alpha1(in interface{}) interface{} {
	data, _ := json.Marshal(in)
	var out interface{}
	_ = json.Unmarshal(data, &out)
	return out
}

// Convert{{ .BaseType }}_v1alpha1_to_v1beta1 converts HyperFleet mirror type to upstream HyperShift via JSON roundtrip.
func Convert{{ .BaseType }}_v1alpha1_to_v1beta1(in interface{}) interface{} {
	data, _ := json.Marshal(in)
	var out interface{}
	_ = json.Unmarshal(data, &out)
	return out
}
{{ end }}
`))

func (g *Generator) generateConversionFunctions() error {
	resources := g.discoverResources()

	for _, resource := range resources {
		specType := resource + "Spec"
		if _, ok := g.typeInfos[specType]; !ok {
			continue
		}

		code, err := g.renderConversionFunctions(resource)
		if err != nil {
			return fmt.Errorf("rendering conversions for %s: %w", resource, err)
		}

		filename := strings.ToLower(resource) + ".go"
		if err := g.writeFormattedFile(filename, code); err != nil {
			return fmt.Errorf("writing conversions for %s: %w", resource, err)
		}
	}
	return nil
}

// discoverResources returns top-level CRD resource names — types that have a
// wrapper struct with TypeMeta/ObjectMeta embeds and matching Spec+Status.
func (g *Generator) discoverResources() []string {
	var resources []string
	seen := make(map[string]bool)
	for typeName := range g.typeInfos {
		if !seen[typeName] && g.isCRDResource(typeName) {
			seen[typeName] = true
			resources = append(resources, typeName)
		}
	}
	sort.Strings(resources)
	return resources
}

func (g *Generator) renderConversionFunctions(resource string) (string, error) {
	specType := resource + "Spec"
	statusType := resource + "Status"

	parentPkg := g.outputImportPath()
	restPkg := g.restImportPath()

	specTI := g.typeInfos[specType]
	var specFields []convFieldData
	var mirrorTypes []mirrorConvData
	mirrorSeen := make(map[string]bool)

	if specTI != nil {
		for _, fi := range specTI.Fields {
			if fi.Hidden {
				continue
			}
			cf := convFieldData{GoName: fi.GoName}
			if IsMirrorType(fi.GoName) {
				mapping := GetMirrorMapping(fi.GoName)
				if mapping != nil {
					baseType := strings.TrimPrefix(fi.GoType, "*")
					baseType = strings.TrimPrefix(baseType, "[]")
					if idx := strings.LastIndex(baseType, "."); idx != -1 {
						baseType = baseType[idx+1:]
					}
					cf.IsMirror = true
					cf.MirrorBase = baseType
					if !mirrorSeen[baseType] {
						mirrorSeen[baseType] = true
						mirrorTypes = append(mirrorTypes, mirrorConvData{BaseType: baseType})
					}
				}
			}
			specFields = append(specFields, cf)
		}
	}

	statusTI := g.typeInfos[statusType]
	var statusFields []convFieldData
	if statusTI != nil {
		for _, fi := range statusTI.Fields {
			if fi.Hidden {
				continue
			}
			statusFields = append(statusFields, convFieldData{GoName: fi.GoName})
		}
	}

	data := conversionData{
		PackageName:  filepath.Base(g.OutputDir),
		CRDPackage:   g.CRDPackage,
		ParentPkg:    parentPkg,
		RESTPkg:      restPkg,
		Resource:     resource,
		SpecType:     specType,
		StatusType:   statusType,
		SpecFields:   specFields,
		StatusFields: statusFields,
		MirrorTypes:  mirrorTypes,
	}

	var buf bytes.Buffer
	if err := conversionTmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing conversion template: %w", err)
	}

	return buf.String(), nil
}

// --- Helpers ---

func (g *Generator) outputImportPath() string {
	if g.OutputPackage != "" {
		return g.OutputPackage
	}
	return "github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/conversion"
}

func (g *Generator) ensureDir(dir string) error {
	return os.MkdirAll(dir, 0755)
}

func (g *Generator) writeRESTFile(filename, content string) error {
	fullPath := filepath.Join(g.restOutputDir(), filename)
	if err := g.ensureDir(filepath.Dir(fullPath)); err != nil {
		return err
	}

	formatted, err := format.Source([]byte(content))
	if err != nil {
		formatted = []byte(content)
	}

	return os.WriteFile(fullPath, formatted, 0644)
}

func (g *Generator) writeFormattedFile(relativePath, content string) error {
	fullPath := filepath.Join(g.OutputDir, relativePath)
	if err := g.ensureDir(filepath.Dir(fullPath)); err != nil {
		return err
	}

	formatted, err := format.Source([]byte(content))
	if err != nil {
		formatted = []byte(content)
	}

	return os.WriteFile(fullPath, formatted, 0644)
}
