package validation

import (
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/registry"
)

func newTestValidator(entries map[string]registry.FieldMeta) *FieldValidator {
	return &FieldValidator{registry: entries}
}

func TestValidate_MutableFieldAllowed(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.displayName": {FieldPath: "spec.displayName", WriteMode: registry.Mutable},
	})

	fields := map[string]interface{}{"spec.displayName": "my-cluster"}

	for _, op := range []Operation{OperationCreate, OperationUpdate} {
		errs := v.validate(fields, nil, op, featuregate.Default)
		if errs != nil {
			t.Errorf("mutable field on %s should be allowed, got: %v", op, errs)
		}
	}
}

func TestValidate_ServiceSetFieldBlocked(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.accountId": {FieldPath: "spec.accountId", WriteMode: registry.ServiceSet},
	})

	fields := map[string]interface{}{"spec.accountId": "123"}

	for _, op := range []Operation{OperationCreate, OperationUpdate} {
		errs := v.validate(fields, nil, op, featuregate.Default)
		if errs == nil {
			t.Errorf("service-set field on %s should be blocked", op)
			continue
		}
		if len(errs) != 1 || errs[0].Field != "spec.accountId" {
			t.Errorf("unexpected error: %v", errs)
		}
	}
}

func TestValidate_ImmutableFieldOnCreate(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.name": {FieldPath: "spec.name", WriteMode: registry.Immutable},
	})

	fields := map[string]interface{}{"spec.name": "my-cluster"}
	errs := v.validate(fields, nil, OperationCreate, featuregate.Default)
	if errs != nil {
		t.Errorf("immutable field on create should be allowed, got: %v", errs)
	}
}

func TestValidate_ImmutableFieldChangedOnUpdate(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.name": {FieldPath: "spec.name", WriteMode: registry.Immutable},
	})

	fields := map[string]interface{}{"spec.name": "new-name"}
	existing := map[string]interface{}{"spec.name": "old-name"}

	errs := v.validate(fields, existing, OperationUpdate, featuregate.Default)
	if errs == nil {
		t.Error("immutable field change on update should be blocked")
		return
	}
	if len(errs) != 1 || errs[0].Field != "spec.name" {
		t.Errorf("unexpected error: %v", errs)
	}
}

func TestValidate_ImmutableFieldFirstSetOnUpdate(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.name": {FieldPath: "spec.name", WriteMode: registry.Immutable},
	})

	fields := map[string]interface{}{"spec.name": "new-name"}
	existing := map[string]interface{}{}

	errs := v.validate(fields, existing, OperationUpdate, featuregate.Default)
	if errs != nil {
		t.Errorf("immutable field first-set on update should be allowed, got: %v", errs)
	}
}

func TestValidate_FeatureGateBlocked(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.tags": {FieldPath: "spec.tags", WriteMode: registry.Mutable, FeatureGate: "HyperFleetAutoScaling"},
	})

	fields := map[string]interface{}{"spec.tags": map[string]string{"env": "prod"}}
	errs := v.validate(fields, nil, OperationCreate, featuregate.Default)
	if errs == nil {
		t.Error("feature-gated field without gate should be blocked")
		return
	}
	if len(errs) != 1 || errs[0].Field != "spec.tags" {
		t.Errorf("unexpected error: %v", errs)
	}
}

func TestValidate_FeatureGateAllowed(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.tags": {FieldPath: "spec.tags", WriteMode: registry.Mutable, FeatureGate: "HyperFleetAutoScaling"},
	})

	fields := map[string]interface{}{"spec.tags": map[string]string{"env": "prod"}}
	errs := v.validate(fields, nil, OperationCreate, featuregate.TechPreviewNoUpgrade)
	if errs != nil {
		t.Errorf("feature-gated field with gate enabled should be allowed, got: %v", errs)
	}
}

func TestValidate_UnknownFieldAllowed(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{})

	fields := map[string]interface{}{"spec.unknownField": "value"}
	errs := v.validate(fields, nil, OperationCreate, featuregate.Default)
	if errs != nil {
		t.Errorf("unknown field should be allowed, got: %v", errs)
	}
}

func TestValidate_EmptyFields(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.accountId": {FieldPath: "spec.accountId", WriteMode: registry.ServiceSet},
	})

	errs := v.validate(map[string]interface{}{}, nil, OperationCreate, featuregate.Default)
	if errs != nil {
		t.Errorf("empty fields should produce no errors, got: %v", errs)
	}
}

func TestFlattenToFieldPaths(t *testing.T) {
	type inner struct {
		Name string `json:"name"`
	}
	type outer struct {
		Display string `json:"displayName"`
		Nested  inner  `json:"nested"`
	}

	result := flattenToFieldPaths("spec", &outer{
		Display: "test",
		Nested:  inner{Name: "foo"},
	})

	if _, ok := result["spec.displayName"]; !ok {
		t.Error("expected spec.displayName in flattened paths")
	}
	if _, ok := result["spec.nested"]; !ok {
		t.Error("expected spec.nested in flattened paths")
	}
	if _, ok := result["spec.nested.name"]; !ok {
		t.Error("expected spec.nested.name in flattened paths")
	}
}

func TestValidate_ImmutableUnchangedAllowed(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.name": {FieldPath: "spec.name", WriteMode: registry.Immutable},
	})

	fields := map[string]interface{}{"spec.name": "same-value"}
	existing := map[string]interface{}{"spec.name": "same-value"}

	errs := v.validate(fields, existing, OperationUpdate, featuregate.Default)
	if errs != nil {
		t.Errorf("unchanged immutable field should be allowed, got: %v", errs)
	}
}

func TestValidate_FeatureGateAwareWriteModes(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.releaseChannel": {
			FieldPath: "spec.releaseChannel",
			WriteMode: registry.Immutable,
			FeatureGateAwareWriteModes: []registry.FeatureGateWriteMode{
				{FeatureGate: "", WriteMode: registry.Immutable},
				{FeatureGate: "HyperFleetAutoScaling", WriteMode: registry.Mutable},
			},
		},
	})

	fields := map[string]interface{}{"spec.releaseChannel": "new-channel"}
	existing := map[string]interface{}{"spec.releaseChannel": "old-channel"}

	// Default feature set: gate not enabled, falls back to immutable — change blocked
	errs := v.validate(fields, existing, OperationUpdate, featuregate.Default)
	if errs == nil {
		t.Error("expected immutable error when gate not enabled")
		return
	}
	if len(errs) != 1 || errs[0].Field != "spec.releaseChannel" {
		t.Errorf("unexpected error: %v", errs)
	}

	// TechPreview feature set: HyperFleetAutoScaling enabled, overrides to mutable — change allowed
	errs = v.validate(fields, existing, OperationUpdate, featuregate.TechPreviewNoUpgrade)
	if errs != nil {
		t.Errorf("expected mutable override when gate enabled, got: %v", errs)
	}
}

func TestValidationErrors_Error(t *testing.T) {
	errs := ValidationErrors{
		{Field: "spec.a", Reason: "blocked"},
		{Field: "spec.b", Reason: "also blocked"},
	}
	s := errs.Error()
	if s == "" {
		t.Error("expected non-empty error string")
	}
}
