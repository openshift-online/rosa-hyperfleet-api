package validation

import (
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/registry"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
)

func newTestValidator(entries map[string]registry.FieldMeta) *FieldValidator {
	return &FieldValidator{
		typedRegistry: registry.TypedFieldRegistry{
			"Test": entries,
		},
		resourceType: "Test",
	}
}

func TestValidateCreate_RejectsServiceSetFields(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.accountId": {FieldPath: "spec.accountId", WriteMode: registry.ServiceSet},
		"spec.name":      {FieldPath: "spec.name", WriteMode: registry.Mutable},
	})

	spec := map[string]any{
		"accountId": "123",
		"name":      "my-cluster",
	}

	errs := v.ValidateCreate(spec, featuregate.Default)
	if errs == nil {
		t.Fatal("expected validation errors, got nil")
	}

	found := false
	for _, e := range errs {
		if e.Field == "spec.accountId" {
			found = true
		}
		if e.Field == "spec.name" {
			t.Error("mutable field 'spec.name' should not be rejected")
		}
	}
	if !found {
		t.Error("expected error for service-set field 'spec.accountId'")
	}
}

func TestValidateCreate_SkipsServiceSetFieldsAtZeroValue(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.networking": {FieldPath: "spec.networking", WriteMode: registry.ServiceSet},
		"spec.etcd":       {FieldPath: "spec.etcd", WriteMode: registry.ServiceSet},
		"spec.fips":       {FieldPath: "spec.fips", WriteMode: registry.ServiceSet},
		"spec.services":   {FieldPath: "spec.services", WriteMode: registry.ServiceSet},
		"spec.pullSecret": {FieldPath: "spec.pullSecret", WriteMode: registry.ServiceSet},
	})

	spec := map[string]any{
		"networking": map[string]any{},
		"etcd":       map[string]any{"managementType": "", "managed": map[string]any{}},
		"fips":       false,
		"services":   nil,
		"pullSecret": map[string]any{"name": ""},
	}

	errs := v.ValidateCreate(spec, featuregate.Default)
	if errs != nil {
		t.Errorf("expected no errors for zero-value service-set fields, got %v", errs)
	}
}

func TestValidateCreate_RejectsNonZeroServiceSetFields(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.networking": {FieldPath: "spec.networking", WriteMode: registry.ServiceSet},
		"spec.fips":       {FieldPath: "spec.fips", WriteMode: registry.ServiceSet},
	})

	spec := map[string]any{
		"networking": map[string]any{"clusterNetwork": []any{map[string]any{"cidr": "10.0.0.0/8"}}},
		"fips":       true,
	}

	errs := v.ValidateCreate(spec, featuregate.Default)
	if errs == nil {
		t.Fatal("expected validation errors for non-zero service-set fields, got nil")
	}
	if len(errs) != 2 {
		t.Errorf("expected 2 errors, got %d: %v", len(errs), errs)
	}
}

func TestValidateCreate_AllowsMutableFields(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.displayName": {FieldPath: "spec.displayName", WriteMode: registry.Mutable},
	})

	spec := map[string]any{
		"displayName": "test",
	}

	errs := v.ValidateCreate(spec, featuregate.Default)
	if errs != nil {
		t.Errorf("expected no errors, got %v", errs)
	}
}

func TestValidateCreate_AllowsImmutableFields(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.fips": {FieldPath: "spec.fips", WriteMode: registry.Immutable},
	})

	spec := map[string]any{
		"fips": true,
	}

	errs := v.ValidateCreate(spec, featuregate.Default)
	if errs != nil {
		t.Errorf("expected no errors on create for immutable field, got %v", errs)
	}
}

func TestValidateUpdate_RejectsImmutableFieldChange(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.fips": {FieldPath: "spec.fips", WriteMode: registry.Immutable},
	})

	existing := map[string]any{"fips": true}
	updated := map[string]any{"fips": false}

	errs := v.ValidateUpdate(updated, existing, featuregate.Default)
	if errs == nil {
		t.Fatal("expected validation error for immutable field change")
	}

	if errs[0].Field != "spec.fips" {
		t.Errorf("expected error on spec.fips, got %s", errs[0].Field)
	}
}

func TestValidateUpdate_AllowsImmutableFieldSameValue(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.fips": {FieldPath: "spec.fips", WriteMode: registry.Immutable},
	})

	existing := map[string]any{"fips": true}
	updated := map[string]any{"fips": true}

	errs := v.ValidateUpdate(updated, existing, featuregate.Default)
	if errs != nil {
		t.Errorf("expected no errors when immutable field unchanged, got %v", errs)
	}
}

func TestValidateUpdate_RejectsImmutableFieldChangeFromOmittedZeroValue(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.oidcConfigId": {FieldPath: "spec.oidcConfigId", WriteMode: registry.Immutable},
	})

	// An omitempty zero value is absent from the flattened existing spec entirely,
	// which must still be treated as a change when the update sets a real value.
	existing := map[string]any{}
	updated := map[string]any{"oidcConfigId": "new-oidc-config"}

	errs := v.ValidateUpdate(updated, existing, featuregate.Default)
	if errs == nil {
		t.Fatal("expected validation error when setting an immutable field that was previously omitted")
	}

	if errs[0].Field != "spec.oidcConfigId" {
		t.Errorf("expected error on spec.oidcConfigId, got %s", errs[0].Field)
	}
}

func TestValidateCreate_RejectsFeatureGatedField(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.tags": {FieldPath: "spec.tags", WriteMode: registry.Mutable, FeatureGate: "HyperFleetAutoScaling"},
	})

	spec := map[string]any{
		"tags": map[string]string{"env": "prod"},
	}

	errs := v.ValidateCreate(spec, featuregate.Default)
	if errs == nil {
		t.Fatal("expected validation error for feature-gated field")
	}

	if errs[0].Field != "spec.tags" {
		t.Errorf("expected error on spec.tags, got %s", errs[0].Field)
	}
}

func TestValidateCreate_AllowsFeatureGatedFieldWhenEnabled(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.tags": {FieldPath: "spec.tags", WriteMode: registry.Mutable, FeatureGate: "HyperFleetAutoScaling"},
	})

	spec := map[string]any{
		"tags": map[string]string{"env": "prod"},
	}

	errs := v.ValidateCreate(spec, featuregate.TechPreviewNoUpgrade)
	if errs != nil {
		t.Errorf("expected no errors with gate enabled, got %v", errs)
	}
}

func TestValidateCreate_NilSpecReturnsNil(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{})
	errs := v.ValidateCreate(nil, featuregate.Default)
	if errs != nil {
		t.Errorf("expected nil for nil spec, got %v", errs)
	}
}

func TestValidateUpdate_RejectsServiceSetFields(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.creatorARN": {FieldPath: "spec.creatorARN", WriteMode: registry.ServiceSet},
	})

	existing := map[string]any{"creatorARN": "old-arn"}
	updated := map[string]any{"creatorARN": "new-arn"}

	errs := v.ValidateUpdate(updated, existing, featuregate.Default)
	if errs == nil {
		t.Fatal("expected validation error for service-set field on update")
	}
}

func TestFlattenToFieldPaths(t *testing.T) {
	spec := map[string]any{
		"name": "test",
		"hostedCluster": map[string]any{
			"fips":    true,
			"channel": "stable-4.16",
		},
	}

	result := flattenToFieldPaths(spec)

	expected := []string{
		"spec.name",
		"spec.hostedCluster",
		"spec.hostedCluster.fips",
		"spec.hostedCluster.channel",
	}

	for _, path := range expected {
		if _, found := result[path]; !found {
			t.Errorf("expected path %s in result, not found", path)
		}
	}
}

func TestValidateCreate_FeatureGateAwareWriteMode(t *testing.T) {
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.maxPods": {
			FieldPath: "spec.maxPods",
			WriteMode: registry.ServiceSet,
			FeatureGateAwareWriteModes: []registry.FeatureGateWriteMode{
				{FeatureGate: "HyperFleetKubeletAdvanced", WriteMode: registry.Mutable},
				{FeatureGate: "", WriteMode: registry.ServiceSet},
			},
		},
	})

	spec := map[string]any{"maxPods": 110}

	errs := v.ValidateCreate(spec, featuregate.Default)
	if errs == nil {
		t.Fatal("expected error without feature gate")
	}

	errs = v.ValidateCreate(spec, featuregate.TechPreviewNoUpgrade)
	if errs != nil {
		t.Errorf("expected no errors with gate enabled, got %v", errs)
	}
}
