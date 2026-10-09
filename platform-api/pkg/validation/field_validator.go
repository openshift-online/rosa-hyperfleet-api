package validation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/registry"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
)

type Operation string

const (
	OperationCreate Operation = "create"
	OperationUpdate Operation = "update"
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
	return v.validate(fields, nil, OperationCreate, fs)
}

// ValidateUpdate checks that an update request does not change service-set
// fields (echo of the server value, or both unset, is allowed), change
// immutable fields, or use feature-gated fields without the gate enabled.
func (v *FieldValidator) ValidateUpdate(newSpec, existingSpec any, fs featuregate.FeatureSet) ValidationErrors {
	if newSpec == nil {
		return nil
	}
	newFields := flattenToFieldPaths(newSpec)
	var existingFields map[string]any
	if existingSpec != nil {
		existingFields = flattenToFieldPaths(existingSpec)
	}
	return v.validate(newFields, existingFields, OperationUpdate, fs)
}

func (v *FieldValidator) validate(fields, existingFields map[string]any, op Operation, fs featuregate.FeatureSet) ValidationErrors {
	var errs ValidationErrors

	// Get the field metadata map for this resource type
	fieldMetaMap := v.typedRegistry[v.resourceType]

	for fieldPath, value := range fields {
		meta, canonical, ok := lookupFieldMeta(fieldMetaMap, fieldPath)
		if !ok {
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
					Field:  canonical,
					Reason: fmt.Sprintf("requires feature gate %s which is not enabled in %s feature set", meta.FeatureGate, fs),
				})
				continue
			}
		}

		if err := v.validateWriteMode(canonical, meta, op, value, existingFields, fs); err != nil {
			errs = append(errs, err)
		}
	}
	if v.resourceType == "Cluster" {
		if err := validateAdditionalTrustBundle(fields); err != nil {
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

// lookupFieldMeta finds registry metadata for a flattened path, matching keys case-insensitively.
func lookupFieldMeta(fieldMetaMap map[string]registry.FieldMeta, fieldPath string) (registry.FieldMeta, string, bool) {
	if meta, ok := fieldMetaMap[fieldPath]; ok {
		return meta, fieldPath, true
	}
	for canonical, meta := range fieldMetaMap {
		if strings.EqualFold(canonical, fieldPath) {
			return meta, canonical, true
		}
	}
	return registry.FieldMeta{}, "", false
}

func (v *FieldValidator) validateWriteMode(fieldPath string, meta registry.FieldMeta, op Operation, value any, existingFields map[string]any, fs featuregate.FeatureSet) *ValidationError {
	effectiveMode := meta.WriteMode

	if len(meta.FeatureGateAwareWriteModes) > 0 {
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
	}

	switch effectiveMode {
	case registry.ServiceSet:
		// Create: skip JSON zero values so typed create specs do not false-fail.
		// Update: allow echo of the server value (or both unset); reject changes.
		if op == OperationCreate {
			if isZeroValue(value) {
				return nil
			}
		} else {
			var oldVal any
			if existingFields != nil {
				oldVal = existingFields[fieldPath]
			}
			if serviceSetValuesMatch(oldVal, value) {
				return nil
			}
		}
		return &ValidationError{
			Field:  fieldPath,
			Reason: "field is platform-managed and cannot be set by customers",
		}
	case registry.Immutable:
		if op == OperationUpdate && existingFields != nil {
			// A field absent from a flattened spec (e.g. an omitempty zero value) is
			// treated as nil so that "unset" and "explicitly zero" compare as equal.
			if oldVal := existingFields[fieldPath]; !reflect.DeepEqual(oldVal, value) {
				return &ValidationError{
					Field:  fieldPath,
					Reason: "field is immutable and cannot be changed after creation",
				}
			}
		}
		return nil
	case registry.Mutable:
		return nil
	default:
		return nil
	}
}

// serviceSetValuesMatch reports whether a client value is an allowed echo of
// the server value, including both unset (nil vs "").
func serviceSetValuesMatch(oldVal, newVal any) bool {
	if reflect.DeepEqual(oldVal, newVal) {
		return true
	}
	return isZeroValue(oldVal) && isZeroValue(newVal)
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
	// Raw JSON validation uses exact typed values rather than float64-flattened
	// values. Preserve the same both-unset behavior for typed zero echoes.
	return isTypedZeroValue(reflect.ValueOf(v))
}

func isTypedZeroValue(value reflect.Value) bool {
	if !value.IsValid() {
		return true
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		return value.IsNil() || isTypedZeroValue(value.Elem())
	case reflect.Slice:
		return value.Len() == 0
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if !isTypedZeroValue(iter.Value()) {
				return false
			}
		}
		return true
	case reflect.Struct, reflect.Array:
		if value.Kind() == reflect.Struct {
			for i := 0; i < value.NumField(); i++ {
				if !isTypedZeroValue(value.Field(i)) {
					return false
				}
			}
		} else {
			for i := 0; i < value.Len(); i++ {
				if !isTypedZeroValue(value.Index(i)) {
					return false
				}
			}
		}
		return true
	default:
		return value.IsZero()
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
