package validation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"unicode"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
)

const jsonTagPunctuation = "!#$%&()*+-./:;<=>?@[]^_{|}~ "

// AnalyzeUpdate checks submitted/affected fields once without merging or mutating specs.
// Gates apply to echoes; actions require effective changes. Base authorization stays mandatory.
func (v *FieldValidator) AnalyzeUpdate(rawSpec json.RawMessage, existingSpec, candidateSpec any, fs featuregate.FeatureSet) ([]string, ValidationErrors) {
	fields, err := v.submittedUpdateFields(rawSpec, existingSpec, candidateSpec)
	if err != nil {
		return nil, ValidationErrors{&ValidationError{Field: "spec", Reason: err.Error()}}
	}
	required := make(map[string]bool)
	var errs ValidationErrors
	for _, field := range fields {
		meta := v.typedRegistry[v.resourceType][field.path]
		if field.changed && meta.UpdateAction != "" {
			required[meta.UpdateAction] = true
		}
		if meta.FeatureGate != "" && !featuregate.IsGateEnabled(meta.FeatureGate, fs) {
			errs = append(errs, &ValidationError{Field: field.path, Reason: fmt.Sprintf("requires feature gate %s which is not enabled in %s feature set", meta.FeatureGate, fs)})
			continue
		}
		// Reduced containers carry ancestor actions; their leaves control mutability.
		if !meta.IsReducedContainer {
			if err := v.validateWriteMode(field.path, meta, OperationUpdate, field.newValue, map[string]any{field.path: field.oldValue}, fs); err != nil {
				errs = append(errs, err)
			}
		}
		if v.resourceType == "Cluster" && field.path == additionalTrustBundleField {
			if err := validateAdditionalTrustBundle(flattenToFieldPaths(candidateSpec)); err != nil {
				errs = append(errs, err)
			}
		}
	}
	var actions []string
	for action := range required {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	return actions, errs
}

type submittedUpdateField struct {
	path               string
	oldValue, newValue any
	changed            bool
}

func (v *FieldValidator) submittedUpdateFields(rawSpec json.RawMessage, existingSpec, candidateSpec any) ([]submittedUpdateField, error) {
	old := reflect.ValueOf(existingSpec)
	candidate := reflect.ValueOf(candidateSpec)
	for old.IsValid() && old.Kind() == reflect.Pointer {
		if old.IsNil() {
			return nil, fmt.Errorf("existing spec must not be nil")
		}
		old = old.Elem()
	}
	for candidate.IsValid() && candidate.Kind() == reflect.Pointer {
		if candidate.IsNil() {
			return nil, fmt.Errorf("candidate spec must not be nil")
		}
		candidate = candidate.Elem()
	}
	if !old.IsValid() || !candidate.IsValid() || old.Kind() != reflect.Struct || old.Type() != candidate.Type() {
		return nil, fmt.Errorf("existing and candidate specs must have the same struct type")
	}
	submitted := make(map[string]bool)
	decoder := json.NewDecoder(bytes.NewReader(rawSpec))
	decoder.UseNumber()
	if err := readSubmittedJSON(decoder, old.Type(), "spec", submitted); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("spec must contain exactly one JSON object")
	}
	// An update spec is an object, never a top-level null or array.
	trimmed := bytes.TrimSpace(rawSpec)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("spec must be a JSON object")
	}
	var paths []string
	for path := range v.typedRegistry[v.resourceType] {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var result []submittedUpdateField
	for _, path := range paths {
		parts := strings.Split(strings.TrimPrefix(path, "spec."), ".")
		oldValue, ok := typedFieldValue(old, parts)
		if !ok {
			continue
		}
		newValue, ok := typedFieldValue(candidate, parts)
		if !ok {
			continue
		}
		changed := !reflect.DeepEqual(oldValue, newValue)
		present, related := false, false
		for submittedPath := range submitted {
			if submittedPath == path || strings.HasPrefix(submittedPath, path+".") {
				present = true
			}
			if present || strings.HasPrefix(path, submittedPath+".") {
				related = true
			}
		}
		if present || (related && changed) {
			result = append(result, submittedUpdateField{path: path, oldValue: oldValue, newValue: newValue, changed: changed && related})
		}
	}
	return result, nil
}

// readSubmittedJSON uses the target type to canonicalize struct fields while
// preserving dynamic map keys. Token decoding retains duplicate keys that a raw
// map would lose. Collections are protected units, but their contents are still
// checked for ambiguous object keys before any authorization or persistence.
func readSubmittedJSON(decoder *json.Decoder, target reflect.Type, path string, submitted map[string]bool) error {
	for target != nil && target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("invalid spec JSON: %w", err)
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]bool)
		var structFields []jsonField
		if target != nil && target.Kind() == reflect.Struct {
			structFields = jsonStructFields(target)
		}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("invalid object key at %s", path)
			}
			canonical, childType, known := submittedJSONTarget(target, structFields, key)
			if target != nil && target.Kind() == reflect.Struct {
				// Unknown aliases are ignored by decoding, but still may not be ambiguous.
				for prior := range seen {
					if strings.EqualFold(prior, canonical) {
						return fmt.Errorf("duplicate or case-aliased JSON field %s.%s", path, key)
					}
				}
			}
			if seen[canonical] {
				return fmt.Errorf("duplicate JSON field %s.%s", path, key)
			}
			seen[canonical] = true
			childPath := path + "." + canonical
			childSubmitted := submitted
			if !known {
				childSubmitted = nil
			} else if target.Kind() == reflect.Map {
				// Dynamic keys are not registry fields. The scanner records map
				// value fields directly beneath the protected collection path.
				childPath = path
			} else if submitted != nil {
				submitted[childPath] = true
			}
			if err := readSubmittedJSON(decoder, childType, childPath, childSubmitted); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim('}') {
			return fmt.Errorf("invalid JSON object at %s", path)
		}
	case '[':
		var element reflect.Type
		if target != nil && (target.Kind() == reflect.Slice || target.Kind() == reflect.Array) {
			element = target.Elem()
		}
		for decoder.More() {
			// As in the marker scanner, element fields share the array path;
			// no numeric indexes become registry field names.
			if err := readSubmittedJSON(decoder, element, path, submitted); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil || end != json.Delim(']') {
			return fmt.Errorf("invalid JSON array at %s", path)
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter at %s", path)
	}
	return nil
}

// Resolve struct aliases without folding dynamic map keys.
func submittedJSONTarget(target reflect.Type, fields []jsonField, key string) (string, reflect.Type, bool) {
	if target == nil {
		return key, nil, false
	}
	switch target.Kind() {
	case reflect.Struct:
		if field, ok := matchingJSONField(fields, key); ok {
			return field.name, field.typ, true
		}
	case reflect.Map:
		return key, target.Elem(), true
	}
	return key, nil, false
}

type jsonField struct {
	name   string
	typ    reflect.Type
	index  []int
	tagged bool
}

// jsonStructFields follows encoding/json's exported-field, embedding and
// tagged-field dominance rules. Exact names win over folded matches.
func jsonStructFields(target reflect.Type) []jsonField {
	var candidates []jsonField
	var visit func(reflect.Type, []int, map[reflect.Type]bool)
	visit = func(t reflect.Type, prefix []int, parents map[reflect.Type]bool) {
		if parents[t] {
			return
		}
		nextParents := make(map[reflect.Type]bool, len(parents)+1)
		for key, value := range parents {
			nextParents[key] = value
		}
		nextParents[t] = true
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			typ := field.Type
			if typ.Kind() == reflect.Pointer {
				typ = typ.Elem()
			}
			if field.PkgPath != "" && (!field.Anonymous || typ.Kind() != reflect.Struct) {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if strings.ContainsFunc(tag, func(r rune) bool {
				return !strings.ContainsRune(jsonTagPunctuation, r) && !unicode.IsLetter(r) && !unicode.IsDigit(r)
			}) {
				tag = ""
			}
			if tag == "-" {
				continue
			}
			index := append(append([]int(nil), prefix...), i)
			if tag == "" && field.Anonymous && typ.Kind() == reflect.Struct {
				visit(typ, index, nextParents)
				continue
			}
			name := tag
			if name == "" {
				name = field.Name
			}
			candidates = append(candidates, jsonField{name: name, typ: field.Type, index: index, tagged: tag != ""})
		}
	}
	visit(target, nil, map[reflect.Type]bool{})
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.name != b.name {
			return a.name < b.name
		}
		if len(a.index) != len(b.index) {
			return len(a.index) < len(b.index)
		}
		return a.tagged && !b.tagged
	})
	var fields []jsonField
	for i := 0; i < len(candidates); {
		j := i + 1
		for j < len(candidates) && candidates[j].name == candidates[i].name {
			j++
		}
		if j == i+1 || len(candidates[i].index) != len(candidates[i+1].index) || candidates[i].tagged != candidates[i+1].tagged {
			fields = append(fields, candidates[i])
		}
		i = j
	}
	// Folded matching uses the first field in declaration/index order.
	sort.Slice(fields, func(i, j int) bool {
		a, b := fields[i].index, fields[j].index
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return len(a) < len(b)
	})
	return fields
}

func matchingJSONField(fields []jsonField, key string) (jsonField, bool) {
	for _, field := range fields {
		if field.name == key {
			return field, true
		}
	}
	for _, field := range fields {
		if strings.EqualFold(field.name, key) {
			return field, true
		}
	}
	return jsonField{}, false
}

func typedFieldValue(value reflect.Value, parts []string) (any, bool) {
	for _, part := range parts {
		for value.Kind() == reflect.Pointer {
			if value.IsNil() {
				value = reflect.Zero(value.Type().Elem())
			} else {
				value = value.Elem()
			}
		}
		// Indexed/map traversal cannot name a protected registry descendant. Treat
		// the whole collection as the unit instead of silently dropping protection.
		if value.Kind() == reflect.Map || value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
			return value.Interface(), true
		}
		if value.Kind() != reflect.Struct {
			return nil, false
		}
		field, ok := matchingJSONField(jsonStructFields(value.Type()), part)
		if !ok {
			return nil, false
		}
		for _, index := range field.index {
			for value.Kind() == reflect.Pointer {
				if value.IsNil() {
					value = reflect.Zero(value.Type().Elem())
				} else {
					value = value.Elem()
				}
			}
			value = value.Field(index)
		}
	}
	return value.Interface(), true
}
