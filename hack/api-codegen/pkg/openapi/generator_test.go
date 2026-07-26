package openapi

import (
	"encoding/json"
	"os"
	"testing"

	"k8s.io/kube-openapi/pkg/validation/spec"
)

func TestGenerate(t *testing.T) {
	tmpFile := "/tmp/openapi-test.json"
	defer func() { _ = os.Remove(tmpFile) }()

	// Test with no input dirs (POC mode)
	gen := NewGenerator(nil, tmpFile)
	gen.Title = "Test API"
	gen.Version = "v1"

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	// Verify file exists
	if _, err := os.Stat(tmpFile); err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	// Verify it's valid JSON
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	var swagger spec.Swagger
	if err := json.Unmarshal(data, &swagger); err != nil {
		t.Fatalf("Output is not valid JSON: %v", err)
	}

	// Verify basic structure
	if swagger.Swagger != "2.0" {
		t.Errorf("Expected Swagger 2.0, got %s", swagger.Swagger)
	}

	if swagger.Info.Title != "Test API" {
		t.Errorf("Expected title 'Test API', got %s", swagger.Info.Title)
	}

	if swagger.Info.Version != "v1" {
		t.Errorf("Expected version 'v1', got %s", swagger.Info.Version)
	}

	t.Logf("Generated OpenAPI schema:\n%s", string(data))
}
