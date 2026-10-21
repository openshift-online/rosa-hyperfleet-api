package validation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/openshift-online/rosa-seaplane-api/hack/api-codegen/pkg/registry"
	"github.com/openshift-online/rosa-seaplane-api/platform-api/internal/codegen/featuregate"
)

type Operation string

const (
	OperationCreate Operation = "create"
	OperationUpdate Operation = "update"

	immutableFieldErrorReason = "field is immutable and cannot be changed after creation"
)

type ValidationError struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("field %s: %s", e.Field, e.Reason)
}

type ValidationErrors []*ValidationError

func (e ValidationErrors) Error() string {
	if len(e) == 0 {
		return "no validation errors"
	}
	msgs := make([]string, len(e))
	for i, err := range e {
		msgs[i] = err.Error()
	}
	return strings.Join(msgs, "; ")
}

type FieldValidator struct {
	// typedRegistry maps resource type (CRD Kind) to field metadata map
	typedRegistry registry.TypedFieldRegistry
	// resourceType is the CRD Kind this validator is for (e.g., "Cluster", "NodePool")
	resourceType string
}

// NewFieldValidator creates a validator for a specific CRD resource type
// This enables per-type field metadata with different rules for the same field in different CRDs
func NewFieldValidator(resourceType string) *FieldValidator {
	return &FieldValidator{
		typedRegistry: registry.FieldRegistry,
		resourceType:  resourceType,
	}
}

// ValidateCreate checks that a create request does not set service-set or
// feature-gated fields. The spec is JSON-roundtripped to extract field paths.
func (v *FieldValidator) ValidateCreate(spec any, fs featuregate.FeatureSet) ValidationErrors {
	if spec == nil {
		return nil
	}
	fields := flattenToFieldPaths(spec)
	pointers := make(map[string]bool)
	collectPointerSet("spec", reflect.ValueOf(spec), pointers)
	return v.validate(fields, nil, OperationCreate, fs, pointers)
}

// ValidateUpdate checks that an update request does not change service-set or
// immutable fields, or use feature-gated fields without the gate enabled. When
// newSpec is a raw map, its explicit null and zero values are used for immutable
// field checks as well.
func (v *FieldValidator) ValidateUpdate(newSpec, existingSpec any, fs featuregate.FeatureSet) ValidationErrors {
	return v.validateUpdate(newSpec, newSpec, existingSpec, fs)
}

func (v *FieldValidator) validateUpdate(newSpec, rawSpec, existingSpec any, fs featuregate.FeatureSet) ValidationErrors {
	if newSpec == nil {
		return nil
	}
	fieldsToValidate := newSpec
	var rawFields map[string]any
	if rawSpec != nil {
		// The decoded type includes zero values for omitted non-pointer fields.
		// For partial updates, validate only paths explicitly present in the raw
		// request so omitted immutable and service-set fields remain unchanged.
		fieldsToValidate = rawSpec
		rawFields = flattenToFieldPaths(rawSpec)
	}
	newFields := flattenToFieldPaths(fieldsToValidate)
	pointers := make(map[string]bool)
	collectPointerSet("spec", reflect.ValueOf(newSpec), pointers)
	var existingFields map[string]any
	if existingSpec != nil {
		existingFields = flattenToFieldPaths(existingSpec)
	}
	errs := v.validate(newFields, existingFields, OperationUpdate, fs, pointers)
	if rawFields != nil && existingFields != nil {
		errs = appendUniqueValidationErrors(errs, v.validateRawImmutableChanges(rawFields, existingFields, fs))
		errs = appendUniqueValidationErrors(errs, v.validateRawServiceSetClears(rawFields, existingFields, fs))
	}
	return errs
}

// ValidateUpdateWithRawSpec validates the fields explicitly present in the
// original JSON spec against the existing object. newSpec supplies pointer
// presence information; rawSpec preserves omitted fields and explicit null/zero
// values that a typed partial update may lose. A missing or empty raw spec is
// rejected so validation cannot silently fall back to decoded zero values.
func (v *FieldValidator) ValidateUpdateWithRawSpec(newSpec any, rawSpec map[string]any, existingSpec any, fs featuregate.FeatureSet) ValidationErrors {
	if newSpec == nil {
		return ValidationErrors{&ValidationError{Field: "spec", Reason: "typed spec is required for update validation"}}
	}
	if len(rawSpec) == 0 {
		return ValidationErrors{&ValidationError{Field: "spec", Reason: "raw spec is required for update validation"}}
	}
	return v.validateUpdate(newSpec, rawSpec, existingSpec, fs)
}

func (v *FieldValidator) validateRawImmutableChanges(rawFields, existingFields map[string]any, fs featuregate.FeatureSet) ValidationErrors {
	var errs ValidationErrors
	fieldMetaMap := v.typedRegistry[v.resourceType]

	for fieldPath, meta := range fieldMetaMap {
		if meta.Hidden || isStructuralContainer(fieldPath, fieldMetaMap) || effectiveWriteMode(meta, fs) != registry.Immutable {
			continue
		}

		oldValue, oldExists := existingFields[fieldPath]
		newValue, newExists := rawFields[fieldPath]
		if newExists && !reflect.DeepEqual(oldValue, newValue) {
			errs = append(errs, &ValidationError{Field: fieldPath, Reason: immutableFieldErrorReason})
			continue
		}

		// A null parent object clears all of its descendants during JSON merge,
		// even though the flattened raw spec has no entry for the immutable leaf.
		if oldExists && !isZeroValue(oldValue) && hasNullAncestor(fieldPath, rawFields) {
			errs = append(errs, &ValidationError{Field: fieldPath, Reason: immutableFieldErrorReason})
		}
	}

	return errs
}

func (v *FieldValidator) validateRawServiceSetClears(rawFields, existingFields map[string]any, fs featuregate.FeatureSet) ValidationErrors {
	var errs ValidationErrors
	fieldMetaMap := v.typedRegistry[v.resourceType]

	for fieldPath, meta := range fieldMetaMap {
		if meta.Hidden || isStructuralContainer(fieldPath, fieldMetaMap) || effectiveWriteMode(meta, fs) != registry.ServiceSet {
			continue
		}
		oldValue, oldExists := existingFields[fieldPath]
		if oldExists && !isZeroValue(oldValue) && hasNullAncestor(fieldPath, rawFields) {
			errs = append(errs, &ValidationError{
				Field:  fieldPath,
				Reason: "field is platform-managed and cannot be set by customers",
			})
		}
	}

	return errs
}

func hasNullAncestor(fieldPath string, fields map[string]any) bool {
	parts := strings.Split(fieldPath, ".")
	for i := 1; i < len(parts); i++ {
		ancestor := strings.Join(parts[:i], ".")
		if value, exists := fields[ancestor]; exists && value == nil {
			return true
		}
	}
	return false
}

func appendUniqueValidationErrors(existing, additional ValidationErrors) ValidationErrors {
	if len(additional) == 0 {
		return existing
	}

	seen := make(map[string]struct{}, len(existing))
	for _, err := range existing {
		seen[err.Field+"\x00"+err.Reason] = struct{}{}
	}
	for _, err := range additional {
		key := err.Field + "\x00" + err.Reason
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		existing = append(existing, err)
	}
	return existing
}

func (v *FieldValidator) validate(fields, existingFields map[string]any, op Operation, fs featuregate.FeatureSet, pointers map[string]bool) ValidationErrors {
	var errs ValidationErrors

	// Get the field metadata map for this resource type
	fieldMetaMap := v.typedRegistry[v.resourceType]

	for fieldPath := range fields {
		meta, exists := fieldMetaMap[fieldPath]
		if !exists {
			continue
		}

		// Skip hidden fields and service-set structural containers. The latter
		// can contain mutable descendants, such as proxy.httpProxy, and must not
		// reject the whole object before its children are validated.
		if meta.Hidden || isStructuralContainer(fieldPath, fieldMetaMap) {
			continue
		}

		// Value-type fields with omitempty can still be serialized as empty
		// objects. Treat only a literal empty object as unset; scalar zero values
		// can be meaningful configuration and must still require their gate.
		if meta.FeatureGate != "" && !isEmptyObject(fields[fieldPath]) {
			if !featuregate.IsGateEnabled(meta.FeatureGate, fs) {
				errs = append(errs, &ValidationError{
					Field:  fieldPath,
					Reason: fmt.Sprintf("requires feature gate %s which is not enabled in %s feature set", meta.FeatureGate, fs),
				})
				continue
			}
		}

		if err := v.validateWriteMode(fieldPath, meta, op, fields, existingFields, fs, pointers); err != nil {
			errs = append(errs, err)
		}
	}
	if v.resourceType == "Cluster" {
		if err := validateAdditionalTrustBundle(fields); err != nil {
			errs = append(errs, err)
		}
		if err := validateAdditionalAllowedPrincipals(fields); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func isEmptyObject(v any) bool {
	object, ok := v.(map[string]any)
	return ok && len(object) == 0
}

func isStructuralContainer(fieldPath string, fieldMetaMap map[string]registry.FieldMeta) bool {
	prefix := fieldPath + "."
	for childPath, childMeta := range fieldMetaMap {
		if !strings.HasPrefix(childPath, prefix) {
			continue
		}
		if childMeta.WriteMode == registry.Mutable || childMeta.WriteMode == registry.Immutable {
			return true
		}
	}
	return false
}

func (v *FieldValidator) validateWriteMode(fieldPath string, meta registry.FieldMeta, op Operation, fields, existingFields map[string]any, fs featuregate.FeatureSet, pointers map[string]bool) *ValidationError {
	effectiveMode := effectiveWriteMode(meta, fs)

	switch effectiveMode {
	case registry.ServiceSet:
		if op == OperationUpdate && existingFields != nil {
			oldValue, oldExists := existingFields[fieldPath]
			newValue, newExists := fields[fieldPath]
			if !newExists || (oldExists && reflect.DeepEqual(oldValue, newValue)) {
				return nil
			}
			if !oldExists && isZeroValue(newValue) && !pointers[fieldPath] {
				return nil
			}
		} else if isZeroValue(fields[fieldPath]) && !pointers[fieldPath] {
			return nil
		}
		// Current Terraform will send the full object for resource update
		// Skip validation if the field is not changed.
		if op == OperationUpdate && existingFields != nil && !fieldChanged(fields, existingFields, fieldPath) {
			return nil
		}
		return &ValidationError{
			Field:  fieldPath,
			Reason: "field is platform-managed and cannot be set by customers",
		}
	case registry.Immutable:
		if op == OperationUpdate && fieldChanged(fields, existingFields, fieldPath) {
			// A field absent from a flattened spec (e.g. an omitempty zero value) is
			// treated as nil so that "unset" and "explicitly zero" compare as equal.
			return &ValidationError{
				Field:  fieldPath,
				Reason: immutableFieldErrorReason,
			}
		}
		return nil
	case registry.Mutable:
		return nil
	default:
		return nil
	}
}

func effectiveWriteMode(meta registry.FieldMeta, fs featuregate.FeatureSet) registry.WriteMode {
	effectiveMode := meta.WriteMode
	if len(meta.FeatureGateAwareWriteModes) == 0 {
		return effectiveMode
	}

	matched := false
	for _, override := range meta.FeatureGateAwareWriteModes {
		if override.FeatureGate != "" && featuregate.IsGateEnabled(override.FeatureGate, fs) {
			effectiveMode = override.WriteMode
			matched = true
			break
		}
	}
	if !matched {
		for _, override := range meta.FeatureGateAwareWriteModes {
			if override.FeatureGate == "" {
				effectiveMode = override.WriteMode
				break
			}
		}
	}
	return effectiveMode
}

func flattenToFieldPaths(v any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}

	result := make(map[string]any)
	flattenMap("spec", m, result)
	return result
}

// isZeroValue returns true when v is a JSON-deserialized zero value.
// After json.Unmarshal into map[string]any, Go zero values appear as:
// nil (null slices/pointers), "" (strings), false (bools), 0.0 (numbers),
// and empty maps (zero-value structs).
func isZeroValue(v any) bool {
	if v == nil {
		return true
	}
	switch val := v.(type) {
	case string:
		return val == ""
	case bool:
		return !val
	case float64:
		return val == 0
	case map[string]any:
		for _, child := range val {
			if !isZeroValue(child) {
				return false
			}
		}
		return true
	case []any:
		return len(val) == 0
	}
	return false
}

// collectPointerSet records, for every pointer-typed struct field reachable from v,
// whether it was non-nil at its JSON field path. Unresolvable fields are omitted.
func collectPointerSet(prefix string, v reflect.Value, result map[string]bool) {
	for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}

	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := strings.Split(field.Tag.Get("json"), ",")[0]
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = field.Name
		}

		path := prefix
		if !field.Anonymous {
			path = prefix + "." + tag
		}

		fv := v.Field(i)
		switch fv.Kind() { //nolint:exhaustive // only pointer/struct/interface need handling here
		case reflect.Pointer:
			if fv.IsNil() {
				result[path] = false
				continue
			}
			result[path] = true
			collectPointerSet(path, fv.Elem(), result)
		case reflect.Struct:
			collectPointerSet(path, fv, result)
		case reflect.Interface:
			if !fv.IsNil() {
				collectPointerSet(path, fv.Elem(), result)
			}
		}
	}
}

func flattenMap(prefix string, m map[string]any, result map[string]any) {
	for key, val := range m {
		var path string
		if prefix == "" {
			path = key
		} else {
			path = prefix + "." + key
		}

		result[path] = val

		if nested, ok := val.(map[string]any); ok {
			flattenMap(path, nested, result)
		}
	}
}

func fieldChanged(fields, existingFields map[string]any, fieldPath string) bool {
	if existingFields != nil {
		oldVal, newVal := existingFields[fieldPath], fields[fieldPath]
		return !reflect.DeepEqual(oldVal, newVal)
	}
	return false
}
