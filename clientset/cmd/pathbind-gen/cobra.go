package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"text/template"
	"time"

	"gopkg.in/yaml.v3"
)

// numericPtrField tracks numeric pointer fields for normalization (clearing unset pre-allocated zeros).
type numericPtrField struct {
	GoName     string   // Struct field name (e.g. "Port")
	FlagName   string   // Flag name (e.g. "port")
	Operations []string // ["create"] or ["create", "update"]
}

// templateData is passed to both create.go.tmpl and update.go.tmpl.
type templateData struct {
	GeneratedAt              string
	Package                  string
	RuntimePkgImport         string
	RuntimeAlias             string
	RuntimeType              string
	InteractivePkg           string
	ResourceName             string
	SDKShortType             string
	CreateFields             []mergedAlias
	UpdateFields             []mergedAlias
	CreateFlagFields         []mergedAlias
	UpdateFlagFields         []mergedAlias
	RequiredCreateFlagFields []mergedAlias
	RequiredUpdateFlagFields []mergedAlias
	HasUpdateFields          bool
	Namespaced               bool
	NumericPtrFields         []numericPtrField // Numeric pointer fields needing normalization
}

func runCobra(draftPath, overridesPath, outputDir string) error {
	rawOv, err := os.ReadFile(overridesPath)
	if err != nil {
		return fmt.Errorf("reading overrides %s: %w", overridesPath, err)
	}
	var ov Overrides
	if err := yaml.Unmarshal(rawOv, &ov); err != nil {
		return fmt.Errorf("parsing overrides: %w", err)
	}

	cfg := ov.Config
	if cfg.Package == "" || cfg.RuntimePkg == "" || cfg.RuntimeType == "" || cfg.InteractivePkg == "" {
		return fmt.Errorf("overrides config must set package, runtimePkg, runtimeType, and interactivePkg")
	}

	// Load draft fields indexed by resource → path.
	draftIndex := map[string]map[string]DraftField{}
	draftSDKTypes := map[string]string{}
	if err := loadDraft(draftPath, draftIndex, draftSDKTypes); err != nil {
		return err
	}

	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating output dir: %w", err)
	}

	// Emit the shared helpers file once before generating per-resource files.
	if err := emitHelpersFile(outputDir, cfg.Package); err != nil {
		return err
	}

	funcMap := buildFuncMap()

	createTmpl := template.Must(template.New("create").Funcs(funcMap).Parse(createTemplate))
	updateTmpl := template.Must(template.New("update").Funcs(funcMap).Parse(updateTemplate))

	// Iterate over union of draft and override resource keys.
	allResKeys := unionKeys(draftSDKTypes, ov.Resources)
	for _, resKey := range allResKeys {
		ovRes := ov.Resources[resKey]

		sdkType, ok := draftSDKTypes[resKey]
		if !ok {
			sdkType = sdkTypeForOwner[titleCase(resKey)]
		}

		aliases := buildMergedAliases(draftIndex[resKey], ovRes.Aliases)

		createFields, createFlagFields, reqCreate, updateFields, updateFlagFields, reqUpdate := categorizeAliases(aliases)

		resName := titleCase(resKey)
		sdkShort := sdkShortType(sdkType)
		runtimeAlias := pkgAlias(cfg.RuntimePkg)

		// Collect numeric pointer fields for normalization (clearing unset pre-allocated zeros).
		numericPtrFields := collectNumericPtrFields(aliases)

		td := templateData{
			GeneratedAt:              time.Now().UTC().Format(time.RFC3339),
			Package:                  cfg.Package,
			RuntimePkgImport:         cfg.RuntimePkg,
			RuntimeAlias:             runtimeAlias,
			RuntimeType:              cfg.RuntimeType,
			InteractivePkg:           cfg.InteractivePkg,
			ResourceName:             resName,
			SDKShortType:             sdkShort,
			CreateFields:             createFields,
			UpdateFields:             updateFields,
			CreateFlagFields:         createFlagFields,
			UpdateFlagFields:         updateFlagFields,
			RequiredCreateFlagFields: reqCreate,
			RequiredUpdateFlagFields: reqUpdate,
			HasUpdateFields:          len(updateFlagFields) > 0,
			Namespaced:               strings.EqualFold(resKey, "nodepool"),
			NumericPtrFields:         numericPtrFields,
		}

		if err := emitFile(createTmpl, td, filepath.Join(outputDir, strings.ToLower(resKey)+"_create_gen.go"), true); err != nil {
			return err
		}
		if td.HasUpdateFields {
			// Update template shares imports from the create file header; emit separately.
			if err := emitFile(updateTmpl, td, filepath.Join(outputDir, strings.ToLower(resKey)+"_update_gen.go"), false); err != nil {
				return err
			}
		}
	}
	return nil
}

// emitFile renders tmpl with data, gofmts the result, and writes to path.
// withHeader=true wraps the output in a full Go file with package + imports.
func emitFile(tmpl *template.Template, data templateData, path string, withHeader bool) error {
	var buf bytes.Buffer

	// The update template is a fragment (no package/imports); wrap it.
	if !withHeader {
		fmt.Fprintf(&buf, "package %s\n\nimport (\n\t\"context\"\n\t\"github.com/spf13/cobra\"\n\t%s %q\n\tv1alpha1 \"github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public\"\n\tpathbind \"github.com/openshift-online/rosa-hyperfleet-api/clientset/pathbind\"\n\tplatform \"github.com/openshift-online/rosa-hyperfleet-api/clientset/platform\"\n\tinteractive %q\n)\n\n",
			data.Package,
			data.RuntimeAlias, data.RuntimePkgImport,
			data.InteractivePkg,
		)
	}

	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("rendering %s: %w", path, err)
	}

	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		debugPath := path + ".debug"
		_ = os.WriteFile(debugPath, buf.Bytes(), 0o644)
		return fmt.Errorf("formatting %s: %w\n(unformatted written to %s)", path, err, debugPath)
	}

	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Printf("pathbind-gen: wrote %s\n", path)
	return nil
}

// buildMergedAliases builds the merged alias list with the draft as the primary source.
// Every draft field gets a struct field and an auto-derived cobra flag.
// Override entries refine auto-derived defaults (alias, flag name, description, type, required).
// Consumer-only override entries (no path, e.g. operatorRolesPrefix) are appended at the end.
//
// Operations: always come from the draft (via FieldRegistry write-mode markers).
// Override entries may restrict operations but never widen them. Consumer-only entries
// must declare operations explicitly — there is no default.
func buildMergedAliases(draft map[string]DraftField, overrides []OverrideAlias) []mergedAlias {
	// Index overrides by path for O(1) lookup.
	ovByPath := map[string]OverrideAlias{}
	for _, ov := range overrides {
		if ov.Path != "" {
			ovByPath[ov.Path] = ov
		}
	}

	// All draft paths for unique alias generation.
	allDraftPaths := make([]string, 0, len(draft))
	for p := range draft {
		allDraftPaths = append(allDraftPaths, p)
	}

	var out []mergedAlias

	// 1. Draft fields are the source — every draft path gets a merged alias.
	for _, df := range sortedDraftFields(draft) {
		ov, hasOv := ovByPath[df.Path]
		ma := mergeDraftField(df, ov, hasOv, allDraftPaths)
		out = append(out, ma)
	}

	// 2. Consumer-only override entries (no path — not in the draft).
	// Operations must be declared explicitly; there is no implicit default.
	out = append(out, mergeConsumerOnlyAliases(overrides)...)

	return out
}

// mergeDraftField constructs a mergedAlias from a draft field and its optional override.
func mergeDraftField(df DraftField, ov OverrideAlias, hasOv bool, allDraftPaths []string) mergedAlias {
	// Alias: override > auto-derived unique trailing-segment alias.
	alias := uniqueAlias(df.Path, allDraftPaths)
	if hasOv && ov.Alias != "" {
		alias = ov.Alias
	}

	goName := toPascal(alias)
	if hasOv && ov.GoName != "" {
		goName = ov.GoName
	}

	// Type: override > draft GoType mapping > string default.
	typ := ""
	if hasOv && ov.Type != "" {
		typ = ov.Type
	}
	if typ == "" && df.GoType != "" {
		typ = goTypeToConsumer(df.GoType)
	}
	if typ == "" {
		typ = "string"
	}

	// Flag: omitted = auto-derive; nil/empty = suppress; non-empty = use provided.
	flag := toKebab(alias)
	if hasOv && ov.Flag != nil {
		flag = *ov.Flag
	}

	description := ""
	if hasOv {
		description = ov.Description
	}

	required := false
	if hasOv && ov.Required != nil {
		required = *ov.Required
	}

	// Operations always come from the draft (FieldRegistry write-mode).
	// Override may narrow them (e.g. force [create] for an immutable field).
	ops := df.Operations
	if hasOv && len(ov.Operations) > 0 {
		ops = narrowOperations(df.Operations, ov.Operations)
	}

	// Validate type if this field has a flag.
	if flag != "" && !isSupportedConsumerType(typ) {
		fmt.Fprintf(os.Stderr, "warning: unsupported type %q for flagged field %s (path %q); will default to string\n", typ, goName, df.Path)
	}

	return mergedAlias{
		GoName:      goName,
		Type:        typ,
		Path:        df.Path,
		Flag:        flag,
		Description: description,
		Required:    required,
		Operations:  ops,
		HasFlag:     flag != "",
	}
}

// narrowOperations returns override operations that the draft also allows.
func narrowOperations(draftOps, overrideOps []string) []string {
	var narrowed []string
	for _, o := range overrideOps {
		if hasOperation(draftOps, o) {
			narrowed = append(narrowed, o)
		}
	}
	return narrowed
}

// mergeConsumerOnlyAliases constructs merged aliases from consumer-only override entries.
func mergeConsumerOnlyAliases(overrides []OverrideAlias) []mergedAlias {
	var out []mergedAlias
	for _, ov := range overrides {
		if ov.Path != "" {
			continue
		}
		if len(ov.Operations) == 0 {
			// No operations declared — skip; the caller must be explicit.
			continue
		}

		alias := ov.Alias
		goName := toPascal(alias)
		if ov.GoName != "" {
			goName = ov.GoName
		}

		typ := ov.Type
		if typ == "" {
			typ = "string"
		}

		flag := ""
		if ov.Flag != nil {
			flag = *ov.Flag
		}

		required := false
		if ov.Required != nil {
			required = *ov.Required
		}

		// Validate type if this field has a flag.
		if flag != "" && !isSupportedConsumerType(typ) {
			fmt.Fprintf(os.Stderr, "warning: unsupported type %q for flagged field %s; will default to string\n", typ, goName)
		}

		out = append(out, mergedAlias{
			GoName:      goName,
			Type:        typ,
			Path:        "-",
			Flag:        flag,
			Description: ov.Description,
			Required:    required,
			Operations:  ov.Operations,
			HasFlag:     flag != "",
		})
	}
	return out
}

// sortedDraftFields returns draft fields in deterministic path order.
func sortedDraftFields(draft map[string]DraftField) []DraftField {
	paths := make([]string, 0, len(draft))
	for p := range draft {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	fields := make([]DraftField, 0, len(paths))
	for _, p := range paths {
		fields = append(fields, draft[p])
	}
	return fields
}

// toKebab converts camelCase or PascalCase to kebab-case, preserving acronyms.
// Rules:
//   - Lower→upper boundary always inserts a dash:  "issuerURL" → "issuer-url"
//   - Upper→upper boundary inserts a dash only when the next char starts a new
//     word. A lowercase letter starts a new word UNLESS it is a plural suffix
//     ('s') immediately followed by uppercase or end-of-string (e.g. CIDRs,
//     ARNs):  "URLConfig" → "url-config",  "CIDRs" → "cidrs"
//   - Consecutive uppercase with no word-start follower stay together: "ARN" → "arn"
//
// Examples:
//
//	"displayName"              → "display-name"
//	"issuerURL"                → "issuer-url"
//	"kubeCloudControllerARN"   → "kube-cloud-controller-arn"
//	"AWSPlatform"              → "aws-platform"
//	"registryPullQPS"          → "registry-pull-qps"
//	"allocateNodeCIDRs"        → "allocate-node-cidrs"
//	"allowedCIDRBlocks"        → "allowed-cidr-blocks"
func toKebab(s string) string {
	runes := []rune(s)
	var result []rune
	for i, r := range runes {
		upper := r >= 'A' && r <= 'Z'
		if i > 0 && upper {
			prev := runes[i-1]
			prevUpper := prev >= 'A' && prev <= 'Z'
			if !prevUpper {
				// lower→upper: always a word boundary
				result = append(result, '-')
			} else if i+1 < len(runes) {
				next := runes[i+1]
				if next >= 'a' && next <= 'z' {
					// upper→upper followed by lowercase: new word — UNLESS the
					// lowercase is a plural 's' suffix (e.g. CIDRs, ARNs, QPs).
					// A suffix 's' is detected when it is followed by uppercase
					// or end-of-string (not the start of a multi-char word).
					isSuffix := next == 's' &&
						(i+2 >= len(runes) || (runes[i+2] >= 'A' && runes[i+2] <= 'Z'))
					if !isSuffix {
						result = append(result, '-')
					}
				}
			}
		}
		if upper {
			result = append(result, r+32) // toLower
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

// unionKeys returns the sorted union of keys from two maps.
func unionKeys[V any](a map[string]V, b map[string]OverrideResource) []string {
	seen := map[string]bool{}
	for k := range a {
		seen[k] = true
	}
	for k := range b {
		seen[k] = true
	}
	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// buildFuncMap returns template functions used by create.go.tmpl and update.go.tmpl.
func buildFuncMap() template.FuncMap {
	return template.FuncMap{
		"lower": strings.ToLower,
		"contains": func(slice []string, item string) bool {
			return slices.Contains(slice, item)
		},
		// flagCall emits the cobra flag registration statement for a field, wrapped in
		// registerIfNew so the call is a no-op when the flag is already registered.
		// Pointer types (*bool, *int32) must be pre-allocated inside the closure before
		// BoolVar/Int32Var; the switch uses the full type string so "*bool" and "bool"
		// are distinct cases.
		// flagCall emits the cobra flag registration for a field, wrapped in registerIfNew.
		// Pointer types must be pre-allocated inside the closure before Var calls.
		// The switch uses the full type string so "*bool" and "bool" are distinct cases.
		"flagCall": func(a mergedAlias) string {
			var inner string
			switch a.Type {
			case "*bool":
				inner = fmt.Sprintf("input.%s = new(bool); f.BoolVar(input.%s, %q, false, %q)",
					a.GoName, a.GoName, a.Flag, a.Description)
			case "bool":
				inner = fmt.Sprintf("f.BoolVar(&input.%s, %q, false, %q)", a.GoName, a.Flag, a.Description)
			case "*int32":
				inner = fmt.Sprintf("input.%s = new(int32); f.Int32Var(input.%s, %q, 0, %q)",
					a.GoName, a.GoName, a.Flag, a.Description)
			case "int32":
				inner = fmt.Sprintf("f.Int32Var(&input.%s, %q, 0, %q)", a.GoName, a.Flag, a.Description)
			case "*int64":
				inner = fmt.Sprintf("input.%s = new(int64); f.Int64Var(input.%s, %q, 0, %q)",
					a.GoName, a.GoName, a.Flag, a.Description)
			case "int64":
				inner = fmt.Sprintf("f.Int64Var(&input.%s, %q, 0, %q)", a.GoName, a.Flag, a.Description)
			default:
				inner = fmt.Sprintf("f.StringVar(&input.%s, %q, \"\", %q)", a.GoName, a.Flag, a.Description)
			}
			return fmt.Sprintf("registerIfNew(f, %q, func() { %s })", a.Flag, inner)
		},
		"emptyCheck": func(a mergedAlias) string {
			switch a.Type {
			case "*bool", "*int32", "*int64", "*string":
				return fmt.Sprintf("input.%s == nil", a.GoName)
			case "bool":
				return fmt.Sprintf("input.%s == false", a.GoName)
			case "int32", "int64":
				return fmt.Sprintf("input.%s == 0", a.GoName)
			case "string":
				return fmt.Sprintf(`input.%s == ""`, a.GoName)
			default:
				return fmt.Sprintf(`input.%s == ""`, a.GoName)
			}
		},
		"promptAssign": func(a mergedAlias) string {
			return fmt.Sprintf(
				`input.%s, err = interactive.GetString(interactive.Input{Question: %q, Help: cmd.Flags().Lookup(%q).Usage, Required: true})`,
				a.GoName, a.Description, a.Flag,
			)
		},
		"createSDKCall": func(td templateData) string {
			if td.Namespaced {
				return fmt.Sprintf(
					"created, err := r.HyperFleetClient.HyperfleetV1alpha1().%ss(namespace).Create(ctx, obj, platform.CreateOptions{})",
					td.ResourceName,
				)
			}
			return fmt.Sprintf(
				"created, err := r.HyperFleetClient.HyperfleetV1alpha1().%ss().Create(ctx, obj, platform.CreateOptions{})",
				td.ResourceName,
			)
		},
		"updateSDKCall": func(td templateData) string {
			if td.Namespaced {
				return fmt.Sprintf(
					"updated, err := r.HyperFleetClient.HyperfleetV1alpha1().%ss(namespace).Update(ctx, obj, platform.UpdateOptions{})",
					td.ResourceName,
				)
			}
			return fmt.Sprintf(
				"updated, err := r.HyperFleetClient.HyperfleetV1alpha1().%ss().Update(ctx, obj, platform.UpdateOptions{})",
				td.ResourceName,
			)
		},
	}
}

// emitHelpersFile renders helpers.go.tmpl and writes helpers_gen.go once per output dir.
func emitHelpersFile(outputDir, pkg string) error {
	tmpl := template.Must(template.New("helpers").Parse(helpersTemplate))
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, map[string]string{"Package": pkg}); err != nil {
		return fmt.Errorf("rendering helpers_gen.go: %w", err)
	}
	formatted, err := format.Source(buf.Bytes())
	if err != nil {
		return fmt.Errorf("formatting helpers_gen.go: %w", err)
	}
	path := filepath.Join(outputDir, "helpers_gen.go")
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	fmt.Printf("pathbind-gen: wrote %s\n", path)
	return nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

// goTypeToConsumer maps an OpenAPI goType string to the default consumer Go type.
func goTypeToConsumer(goType string) string {
	switch goType {
	case "string":
		return "string"
	case "boolean":
		return "*bool"
	case "integer(int32)":
		return "*int32"
	case "integer(int64)":
		return "*int64"
	case "number":
		return "" // consumer must specify explicitly
	case "array", "map":
		return "" // consumer must specify explicitly
	}
	return ""
}

func sdkShortType(sdkType string) string {
	if dot := strings.LastIndex(sdkType, "."); dot >= 0 {
		return sdkType[dot+1:]
	}
	return sdkType
}

func pkgAlias(importPath string) string {
	parts := strings.Split(importPath, "/")
	return parts[len(parts)-1]
}

func toPascal(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = rune(strings.ToUpper(string(r[0]))[0])
	return string(r)
}

func titleCase(s string) string { return toPascal(s) }

// uniqueAlias returns the minimum-length camelCase alias built from trailing path
// segments that is unique within allPaths. Starts with 1 segment and extends until
// no other path produces the same alias suffix.
//
// Examples (when both paths are in allPaths):
//
//	"spec.nodePool.platform.aws.subnet.id"               → "subnetId"     (2 segs)
//	"spec.nodePool.platform.aws.placement.capacityReservation.id"
//	                                                     → "capacityReservationId" (3 segs)
func uniqueAlias(path string, allPaths []string) string {
	segs := strings.Split(path, ".")
	for n := 1; n <= len(segs); n++ {
		candidate := joinCamel(segs[len(segs)-n:])
		unique := true
		for _, other := range allPaths {
			if other == path {
				continue
			}
			otherSegs := strings.Split(other, ".")
			if len(otherSegs) < n {
				continue
			}
			if joinCamel(otherSegs[len(otherSegs)-n:]) == candidate {
				unique = false
				break
			}
		}
		if unique {
			return candidate
		}
	}
	return joinCamel(segs) // fallback: full path as camel
}

// joinCamel joins path segments into a single camelCase identifier.
// ["capacityReservation", "id"] → "capacityReservationId"
func joinCamel(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	var result strings.Builder
	result.WriteString(segs[0])
	for _, s := range segs[1:] {
		result.WriteString(toPascal(s))
	}
	return result.String()
}

func hasOperation(ops []string, op string) bool {
	return slices.Contains(ops, op)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// isSupportedConsumerType checks if a type is supported for flag generation and prompting.
// loadDraft reads and indexes draft fields by resource and path.
func loadDraft(draftPath string, draftIndex map[string]map[string]DraftField, draftSDKTypes map[string]string) error {
	if draftPath == "" {
		return nil
	}
	rawDr, err := os.ReadFile(draftPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("reading draft %s: %w", draftPath, err)
	}
	if err != nil {
		return nil // draftPath doesn't exist, which is OK
	}
	var dr Draft
	if err := yaml.Unmarshal(rawDr, &dr); err != nil {
		return fmt.Errorf("parsing draft: %w", err)
	}
	for resKey, r := range dr.Resources {
		draftSDKTypes[resKey] = r.SDKType
		draftIndex[resKey] = map[string]DraftField{}
		for _, f := range r.Fields {
			draftIndex[resKey][f.Path] = f
		}
	}
	return nil
}

// categorizeAliases categorizes aliases into create/update field lists.
func categorizeAliases(aliases []mergedAlias) (
	createFields, createFlagFields, reqCreate []mergedAlias,
	updateFields, updateFlagFields, reqUpdate []mergedAlias) {
	for _, a := range aliases {
		isCreate := hasOperation(a.Operations, "create")
		isUpdate := hasOperation(a.Operations, "update")
		if isCreate {
			createFields = append(createFields, a)
			if a.HasFlag {
				createFlagFields = append(createFlagFields, a)
				if a.Required {
					reqCreate = append(reqCreate, a)
				}
			}
		}
		if isUpdate {
			updateFields = append(updateFields, a)
			if a.HasFlag {
				updateFlagFields = append(updateFlagFields, a)
				if a.Required {
					reqUpdate = append(reqUpdate, a)
				}
			}
		}
	}
	return
}

// collectNumericPtrFields extracts numeric pointer fields needing normalization.
func collectNumericPtrFields(aliases []mergedAlias) []numericPtrField {
	var fields []numericPtrField
	for _, a := range aliases {
		// Numeric pointer types: *int, *uint, *float variations.
		isNumericPtr := strings.HasPrefix(a.Type, "*") &&
			strings.Contains("int8 int16 int32 int64 uint8 uint16 uint32 uint64 float32 float64", strings.TrimPrefix(a.Type, "*"))
		if isNumericPtr && a.HasFlag {
			fields = append(fields, numericPtrField{
				GoName:     a.GoName,
				FlagName:   a.Flag,
				Operations: a.Operations,
			})
		}
	}
	return fields
}

func isSupportedConsumerType(typ string) bool {
	switch typ {
	case "string", "*string", "bool", "*bool", "int32", "*int32", "int64", "*int64":
		return true
	default:
		return false
	}
}
