// Package pkg contains types, constants, and helper functions shared across
// generation modes (cobra, tf).
package pkg

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// RegistryEntry is an entry from field_metadata.json.
type RegistryEntry struct {
	FieldPath string `json:"fieldPath"`
	WriteMode string `json:"writeMode"`
	Hidden    bool   `json:"hidden"`
	OwnerType string `json:"ownerType"`
}

// Draft is the structure of pathbind-draft.yaml.
type Draft struct {
	Resources map[string]DraftResource `yaml:"resources"`
}

// DraftResource describes a single resource in the draft.
type DraftResource struct {
	SDKType string       `yaml:"sdkType"`
	Fields  []DraftField `yaml:"fields"`
}

// DraftField describes a single leaf field in the draft.
type DraftField struct {
	Path       string   `yaml:"path"`
	GoType     string   `yaml:"goType,omitempty"`
	Operations []string `yaml:"operations"`
}

// Overrides is the structure of pathbind-overrides.yaml.
type Overrides struct {
	Config    OverridesConfig             `yaml:"config"`
	Resources map[string]OverrideResource `yaml:"resources"`
}

// OverridesConfig holds consumer-specific generator settings.
type OverridesConfig struct {
	Package        string `yaml:"package"`
	RuntimePkg     string `yaml:"runtimePkg"`
	RuntimeType    string `yaml:"runtimeType"`
	InteractivePkg string `yaml:"interactivePkg"`
	TFProviderPkg  string `yaml:"tfProviderPkg"`
}

// OverrideResource holds override aliases for a resource.
type OverrideResource struct {
	Aliases []OverrideAlias `yaml:"aliases"`
}

// OverrideAlias is a single override entry.
type OverrideAlias struct {
	Path        string   `yaml:"path"`
	Alias       string   `yaml:"alias"`
	Type        string   `yaml:"type"`
	GoName      string   `yaml:"goName"`
	Flag        *string  `yaml:"flag"`
	Description string   `yaml:"description"`
	Required    *bool    `yaml:"required"`
	Operations  []string `yaml:"operations"`
	// TF-specific fields:
	Immutable   *bool `yaml:"immutable"`
	Computed    *bool `yaml:"computed"`
	Sensitive   *bool `yaml:"sensitive"`
	JSONEncoded *bool `yaml:"json_encoded"`
}

// MergedAlias is the unified view of a draft field + its override.
type MergedAlias struct {
	GoName      string
	Type        string
	Path        string // hfsdk tag value ("-" for consumer-only)
	Flag        string
	Description string
	Required    bool
	Operations  []string
	HasFlag     bool
	// TF-specific:
	Immutable   bool
	Computed    bool
	Sensitive   bool
	JSONEncoded bool
}

// UnsetPtrField tracks pointer flag fields needing normalization.
type UnsetPtrField struct {
	GoName     string   `json:"goName"`
	FlagName   string   `json:"flagName"`
	Operations []string `json:"operations"`
}

// CobraTemplateData is passed to cobra templates.
type CobraTemplateData struct {
	Package                  string
	RuntimePkgImport         string
	RuntimeAlias             string
	RuntimeType              string
	InteractivePkg           string
	ResourceName             string
	SDKShortType             string
	CreateFields             []MergedAlias
	UpdateFields             []MergedAlias
	CreateFlagFields         []MergedAlias
	UpdateFlagFields         []MergedAlias
	RequiredCreateFlagFields []MergedAlias
	RequiredUpdateFlagFields []MergedAlias
	HasUpdateFields          bool
	Namespaced               bool
	UnsetPtrFields           []UnsetPtrField
}

// TFTemplateData is passed to tf templates.
type TFTemplateData struct {
	Package        string
	ResourceName   string
	SDKType        string
	SDKShortType   string
	AllFields      []MergedAlias
	CreateFields   []MergedAlias
	UpdateFields   []MergedAlias
	ImmutableList  []string
	ComputedList   []string
	Namespaced     bool
	HandlerFactory string // e.g., "NewClusterHandlerImpl" for template to call
}

// IsNamespacedResource returns true if the resource requires a namespace/parent argument.
func IsNamespacedResource(resourceKey string) bool {
	return strings.EqualFold(resourceKey, "nodepool")
}

// SDKTypeForOwner maps ownerType to SDK Go type.
var SDKTypeForOwner = map[string]string{
	"Cluster":    "v1alpha1.Cluster",
	"NodePool":   "v1alpha1.NodePool",
	"OidcConfig": "v1alpha1.OidcConfig",
}

// ── Helper functions ──────────────────────────────────────────────────────────

// LoadDraft reads and indexes draft fields by resource and path.
func LoadDraft(draftPath string, draftIndex map[string]map[string]DraftField, draftSDKTypes map[string]string) error {
	if draftPath == "" {
		return nil
	}
	rawDr, err := os.ReadFile(draftPath)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return nil
	}
	var dr Draft
	if err := yaml.Unmarshal(rawDr, &dr); err != nil {
		return nil
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

// BuildMergedAliases builds the merged alias list with the draft as the primary source.
func BuildMergedAliases(draft map[string]DraftField, overrides []OverrideAlias) []MergedAlias {
	ovByPath := map[string]OverrideAlias{}
	for _, ov := range overrides {
		if ov.Path != "" {
			ovByPath[ov.Path] = ov
		}
	}

	allDraftPaths := make([]string, 0, len(draft))
	for p := range draft {
		allDraftPaths = append(allDraftPaths, p)
	}

	var out []MergedAlias

	for _, df := range sortedDraftFields(draft) {
		ov, hasOv := ovByPath[df.Path]
		ma := mergeDraftField(df, ov, hasOv, allDraftPaths)
		out = append(out, ma)
	}

	out = append(out, mergeConsumerOnlyAliases(overrides)...)

	return out
}

// mergeDraftField constructs a MergedAlias from a draft field and its optional override.
func mergeDraftField(df DraftField, ov OverrideAlias, hasOv bool, allDraftPaths []string) MergedAlias {
	alias := uniqueAlias(df.Path, allDraftPaths)
	if hasOv && ov.Alias != "" {
		alias = ov.Alias
	}

	goName := ToPascal(alias)
	if hasOv && ov.GoName != "" {
		goName = ov.GoName
	}

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

	flag := ToKebab(alias)
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

	ops := df.Operations
	if hasOv && len(ov.Operations) > 0 {
		ops = narrowOperations(df.Operations, ov.Operations)
	}

	immutable := false
	if hasOv && ov.Immutable != nil {
		immutable = *ov.Immutable
	}

	computed := false
	if hasOv && ov.Computed != nil {
		computed = *ov.Computed
	}

	sensitive := false
	if hasOv && ov.Sensitive != nil {
		sensitive = *ov.Sensitive
	}

	jsonEncoded := false
	if hasOv && ov.JSONEncoded != nil {
		jsonEncoded = *ov.JSONEncoded
	}

	return MergedAlias{
		GoName:      goName,
		Type:        typ,
		Path:        df.Path,
		Flag:        flag,
		Description: description,
		Required:    required,
		Operations:  ops,
		HasFlag:     flag != "",
		Immutable:   immutable,
		Computed:    computed,
		Sensitive:   sensitive,
		JSONEncoded: jsonEncoded,
	}
}

// mergeConsumerOnlyAliases constructs merged aliases from consumer-only override entries.
func mergeConsumerOnlyAliases(overrides []OverrideAlias) []MergedAlias {
	var out []MergedAlias
	for _, ov := range overrides {
		if ov.Path != "" {
			continue
		}
		if len(ov.Operations) == 0 {
			continue
		}

		alias := ov.Alias
		goName := ToPascal(alias)
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

		immutable := false
		if ov.Immutable != nil {
			immutable = *ov.Immutable
		}

		computed := false
		if ov.Computed != nil {
			computed = *ov.Computed
		}

		sensitive := false
		if ov.Sensitive != nil {
			sensitive = *ov.Sensitive
		}

		jsonEncoded := false
		if ov.JSONEncoded != nil {
			jsonEncoded = *ov.JSONEncoded
		}

		out = append(out, MergedAlias{
			GoName:      goName,
			Type:        typ,
			Path:        "-",
			Flag:        flag,
			Description: ov.Description,
			Required:    required,
			Operations:  ov.Operations,
			HasFlag:     flag != "",
			Immutable:   immutable,
			Computed:    computed,
			Sensitive:   sensitive,
			JSONEncoded: jsonEncoded,
		})
	}
	return out
}

// CategorizeAliases categorizes aliases into create/update field lists.
func CategorizeAliases(aliases []MergedAlias) (
	createFields, createFlagFields, reqCreate []MergedAlias,
	updateFields, updateFlagFields, reqUpdate []MergedAlias) {
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

// CollectUnsetPtrFields extracts pointer flag fields needing normalization.
func CollectUnsetPtrFields(aliases []MergedAlias) []UnsetPtrField {
	var fields []UnsetPtrField
	for _, a := range aliases {
		if !a.HasFlag || !strings.HasPrefix(a.Type, "*") {
			continue
		}
		baseType := strings.TrimPrefix(a.Type, "*")
		isNumericPtr := strings.Contains("int8 int16 int32 int64 uint8 uint16 uint32 uint64 float32 float64", baseType)
		if isNumericPtr || baseType == "bool" {
			fields = append(fields, UnsetPtrField{
				GoName:     a.GoName,
				FlagName:   a.Flag,
				Operations: a.Operations,
			})
		}
	}
	return fields
}

func ToKebab(s string) string {
	runes := []rune(s)
	var result []rune
	for i, r := range runes {
		upper := r >= 'A' && r <= 'Z'
		if i > 0 && upper {
			prev := runes[i-1]
			prevUpper := prev >= 'A' && prev <= 'Z'
			if !prevUpper {
				result = append(result, '-')
			} else if i+1 < len(runes) {
				next := runes[i+1]
				if next >= 'a' && next <= 'z' {
					isSuffix := next == 's' &&
						(i+2 >= len(runes) || (runes[i+2] >= 'A' && runes[i+2] <= 'Z'))
					if !isSuffix {
						result = append(result, '-')
					}
				}
			}
		}
		if upper {
			result = append(result, r+32)
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

// ToSnake converts camelCase to snake_case (same as ToKebab but with underscore).
func ToSnake(s string) string {
	runes := []rune(s)
	var result []rune
	for i, r := range runes {
		upper := r >= 'A' && r <= 'Z'
		if i > 0 && upper {
			prev := runes[i-1]
			prevUpper := prev >= 'A' && prev <= 'Z'
			if !prevUpper {
				result = append(result, '_')
			} else if i+1 < len(runes) {
				next := runes[i+1]
				if next >= 'a' && next <= 'z' {
					isSuffix := next == 's' &&
						(i+2 >= len(runes) || (runes[i+2] >= 'A' && runes[i+2] <= 'Z'))
					if !isSuffix {
						result = append(result, '_')
					}
				}
			}
		}
		if upper {
			result = append(result, r+32)
		} else {
			result = append(result, r)
		}
	}
	return string(result)
}

func ToPascal(s string) string {
	if s == "" {
		return ""
	}
	r := []rune(s)
	r[0] = rune(strings.ToUpper(string(r[0]))[0])
	return string(r)
}

func TitleCase(s string) string { return ToPascal(s) }

func UniqueAlias(path string, allPaths []string) string {
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
	return joinCamel(segs)
}

func uniqueAlias(path string, allPaths []string) string { return UniqueAlias(path, allPaths) }

func joinCamel(segs []string) string {
	if len(segs) == 0 {
		return ""
	}
	var result strings.Builder
	result.WriteString(segs[0])
	for _, s := range segs[1:] {
		result.WriteString(ToPascal(s))
	}
	return result.String()
}

func hasOperation(ops []string, op string) bool {
	for _, o := range ops {
		if o == op {
			return true
		}
	}
	return false
}

// SortedKeys returns the keys of a map sorted as strings.
// Works with any map type by converting keys to strings for comparison.
func SortedKeys[K comparable, V any](m map[K]V) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return fmt.Sprint(keys[i]) < fmt.Sprint(keys[j])
	})
	return keys
}

func UnionKeys[V any](a map[string]V, b map[string]OverrideResource) []string {
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
		return ""
	case "array":
		return ""
	case "map":
		return "map"
	}
	return ""
}

func SDKShortType(sdkType string) string {
	if dot := strings.LastIndex(sdkType, "."); dot >= 0 {
		return sdkType[dot+1:]
	}
	return sdkType
}

func PkgAlias(importPath string) string {
	parts := strings.Split(importPath, "/")
	return parts[len(parts)-1]
}

func narrowOperations(draftOps, overrideOps []string) []string {
	var narrowed []string
	for _, o := range overrideOps {
		if hasOperation(draftOps, o) {
			narrowed = append(narrowed, o)
		}
	}
	return narrowed
}

func IsSupportedConsumerType(typ string) bool {
	switch typ {
	case "string", "*string", "bool", "*bool", "int32", "*int32", "int64", "*int64":
		return true
	default:
		return false
	}
}
