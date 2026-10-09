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
		Operation:    validation.OperationCreate,
		ResourceType: "Cluster",
		Fields: map[string]any{
			"spec.properties":       map[string]string{"environment": "prod"},
			"spec.deleteProtection": true,
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
		Operation:    validation.OperationCreate,
		ResourceType: "Cluster",
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

// Example of blocking immutable field changes. Customer AWS tags are applied
// when the cluster's AWS resources are provisioned and cannot be changed
// afterwards, so spec.tags is marked immutable.
func ExampleValidator_Validate_immutable() {
	v := validation.NewValidator()

	req := &validation.Request{
		Operation:    validation.OperationUpdate,
		ResourceType: "Cluster",
		Fields: map[string]any{
			"spec.tags": map[string]any{"cost-center": "cc-9999"},
		},
		ExistingFields: map[string]any{
			"spec.tags": map[string]any{"cost-center": "cc-1234"},
		},
		FeatureSet: featuregate.Default,
	}

	err := v.Validate(req)
	fmt.Printf("Error: %v\n", err)
	// Output:
	// Error: validation failed:
	//   field spec.tags: field is immutable and cannot be changed after creation
}

// Example of feature gate enforcement
func ExampleValidator_Validate_featureGate() {
	v := validation.NewValidator()

	// Default customer tries to use a TechPreview feature
	req := &validation.Request{
		Operation:    validation.OperationCreate,
		ResourceType: "Cluster",
		Fields: map[string]any{
			"spec.hostedCluster.configuration.kubelet.registryPullQPS": 10,
		},
		FeatureSet: featuregate.Default, // Advanced kubelet config requires TechPreview
	}

	err := v.Validate(req)
	fmt.Printf("Error: %v\n", err)
	// Output:
	// Error: validation failed:
	//   field spec.hostedCluster.configuration.kubelet.registryPullQPS: requires feature gate HyperFleetKubeletAdvanced which is not enabled in Default feature set
}

// Example of feature gate allowing access
func ExampleValidator_Validate_featureGateAllowed() {
	v := validation.NewValidator()

	// TechPreview customer can use TechPreview features
	req := &validation.Request{
		Operation:    validation.OperationCreate,
		ResourceType: "Cluster",
		Fields: map[string]any{
			"spec.hostedCluster.configuration.kubelet.registryPullQPS": 10,
		},
		FeatureSet: featuregate.TechPreviewNoUpgrade,
	}

	if err := v.Validate(req); err != nil {
		log.Fatalf("Validation failed: %v", err)
	}

	fmt.Println("TechPreview customer can use advanced kubelet config")
	// Output: TechPreview customer can use advanced kubelet config
}

// Example of checking field access
func ExampleValidator_ValidateFieldAccess() {
	v := validation.NewValidator()

	// Check if a customer can access a gated field
	err := v.ValidateFieldAccess("Cluster", "spec.hostedCluster.configuration.kubelet.registryPullQPS", featuregate.Default)
	if err != nil {
		fmt.Println("Default customer cannot access advanced kubelet config")
	}

	// TechPreview customer can access it
	err = v.ValidateFieldAccess("Cluster", "spec.hostedCluster.configuration.kubelet.registryPullQPS", featuregate.TechPreviewNoUpgrade)
	if err == nil {
		fmt.Println("TechPreview customer can access advanced kubelet config")
	}

	// Output:
	// Default customer cannot access advanced kubelet config
	// TechPreview customer can access advanced kubelet config
}

// Example of getting field metadata
func ExampleValidator_GetFieldMetadata() {
	v := validation.NewValidator()

	meta, exists := v.GetFieldMetadata("Cluster", "spec.properties")
	if exists {
		fmt.Printf("Field: %s\n", meta.FieldPath)
		fmt.Printf("WriteMode: %s\n", meta.WriteMode)
		fmt.Printf("Hidden: %v\n", meta.Hidden)
		fmt.Printf("FeatureGate: %s\n", meta.FeatureGate)
	}
	// Output:
	// Field: spec.properties
	// WriteMode: mutable
	// Hidden: false
	// FeatureGate:
}
