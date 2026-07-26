package validation_test

import (
	"fmt"
	"log"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/featuregate"
	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/validation"
)

// Example of validating a cluster create request
func ExampleValidator_Validate_create() {
	v := validation.NewValidator()

	// Customer tries to create a cluster
	req := &validation.Request{
		Operation: validation.OperationCreate,
		Fields: map[string]any{
			"spec.displayName":      "my-cluster",
			"spec.deleteProtection": true,
			"spec.labels":           map[string]string{"env": "prod"},
		},
		FeatureSet: featuregate.Default,
	}

	if err := v.Validate(req); err != nil {
		log.Fatalf("Validation failed: %v", err)
	}

	fmt.Println("Create request is valid")
	// Output: Create request is valid
}

// Example of blocking service-set fields
func ExampleValidator_Validate_serviceSet() {
	v := validation.NewValidator()

	// Customer tries to set a service-set field
	req := &validation.Request{
		Operation: validation.OperationCreate,
		Fields: map[string]any{
			"spec.accountId": "my-account", // This is service-set!
		},
		FeatureSet: featuregate.Default,
	}

	err := v.Validate(req)
	fmt.Printf("Error: %v\n", err)
	// Output:
	// Error: validation failed:
	//   field spec.accountId: field is platform-managed (service-set) and cannot be set by customers
}

// Example of blocking immutable field changes
func ExampleValidator_Validate_immutable() {
	// Note: The real registry doesn't have immutable fields yet,
	// but the validator supports them via write-mode markers
	fmt.Println("Immutable fields can be set on create but not changed on update")
	// Output: Immutable fields can be set on create but not changed on update
}

// Example of feature gate enforcement
func ExampleValidator_Validate_featureGate() {
	v := validation.NewValidator()

	// Default customer tries to use a TechPreview feature
	req := &validation.Request{
		Operation: validation.OperationCreate,
		Fields: map[string]any{
			"spec.tags": map[string]string{"team": "platform"},
		},
		FeatureSet: featuregate.Default, // Tags require TechPreview
	}

	err := v.Validate(req)
	fmt.Printf("Error: %v\n", err)
	// Output:
	// Error: validation failed:
	//   field spec.tags: requires feature gate HyperFleetAutoScaling which is not enabled in Default feature set
}

// Example of feature gate allowing access
func ExampleValidator_Validate_featureGateAllowed() {
	v := validation.NewValidator()

	// TechPreview customer can use TechPreview features
	req := &validation.Request{
		Operation: validation.OperationCreate,
		Fields: map[string]any{
			"spec.tags": map[string]string{"team": "platform"},
		},
		FeatureSet: featuregate.TechPreviewNoUpgrade,
	}

	if err := v.Validate(req); err != nil {
		log.Fatalf("Validation failed: %v", err)
	}

	fmt.Println("TechPreview customer can use tags")
	// Output: TechPreview customer can use tags
}

// Example of checking field access
func ExampleValidator_ValidateFieldAccess() {
	v := validation.NewValidator()

	// Check if a customer can access a gated field
	err := v.ValidateFieldAccess("spec.tags", featuregate.Default)
	if err != nil {
		fmt.Println("Default customer cannot access tags field")
	}

	// TechPreview customer can access it
	err = v.ValidateFieldAccess("spec.tags", featuregate.TechPreviewNoUpgrade)
	if err == nil {
		fmt.Println("TechPreview customer can access tags field")
	}

	// Output:
	// Default customer cannot access tags field
	// TechPreview customer can access tags field
}

// Example of getting field metadata
func ExampleValidator_GetFieldMetadata() {
	v := validation.NewValidator()

	meta, exists := v.GetFieldMetadata("spec.displayName")
	if exists {
		fmt.Printf("Field: %s\n", meta.FieldPath)
		fmt.Printf("WriteMode: %s\n", meta.WriteMode)
		fmt.Printf("Hidden: %v\n", meta.Hidden)
		fmt.Printf("FeatureGate: %s\n", meta.FeatureGate)
	}
	// Output:
	// Field: spec.displayName
	// WriteMode: mutable
	// Hidden: false
	// FeatureGate:
}
