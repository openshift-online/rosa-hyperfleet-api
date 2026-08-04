package validation

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/registry"
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
	var sb strings.Builder
	sb.WriteString("validation failed:\n")
	for _, err := range e {
		sb.WriteString("  ")
		sb.WriteString(err.Error())
		sb.WriteString("\n")
	}
	return sb.String()
}

type FieldValidator struct {
	registry map[string]registry.FieldMeta
}

func NewFieldValidator() *FieldValidator {
	return &FieldValidator{
		registry: registry.FieldRegistry,
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

// ValidateUpdate checks that an update request does not set service-set fields,
// change immutable fields, or use feature-gated fields without the gate enabled.
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

	for fieldPath := range fields {
		meta, exists := v.registry[fieldPath]
		if !exists {
			continue
		}

		if meta.FeatureGate != "" {
			if !featuregate.IsGateEnabled(meta.FeatureGate, fs) {
				errs = append(errs, &ValidationError{
					Field:  fieldPath,
					Reason: fmt.Sprintf("requires feature gate %s which is not enabled in %s feature set", meta.FeatureGate, fs),
				})
				continue
			}
		}

		if err := v.validateWriteMode(fieldPath, meta, op, fields, existingFields, fs); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return errs
	}
	return nil
}

func (v *FieldValidator) validateWriteMode(fieldPath string, meta registry.FieldMeta, op Operation, fields, existingFields map[string]any, fs featuregate.FeatureSet) *ValidationError {
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
		return &ValidationError{
			Field:  fieldPath,
			Reason: "field is platform-managed and cannot be set by customers",
		}
	case registry.Immutable:
		if op == OperationUpdate && existingFields != nil {
			oldVal, existsInOld := existingFields[fieldPath]
			if existsInOld {
				newVal := fields[fieldPath]
				if !reflect.DeepEqual(oldVal, newVal) {
					return &ValidationError{
						Field:  fieldPath,
						Reason: "field is immutable and cannot be changed after creation",
					}
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
