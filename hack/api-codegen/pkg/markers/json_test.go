package markers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRegistryFromJSON_NonexistentFile(t *testing.T) {
	_, err := LoadRegistryFromJSON("/nonexistent/file.json")
	if err == nil {
		t.Error("LoadRegistryFromJSON() with nonexistent file should return error")
	}
}

func TestLoadRegistryFromJSON_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	invalidFile := filepath.Join(tmpDir, "invalid.json")

	// Write invalid JSON
	err := os.WriteFile(invalidFile, []byte("not valid json {"), 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	_, err = LoadRegistryFromJSON(invalidFile)
	if err == nil {
		t.Error("LoadRegistryFromJSON() with invalid JSON should return error")
	}
}

func TestLoadRegistryFromJSON_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	validFile := filepath.Join(tmpDir, "valid.json")

	// Write valid JSON (array format)
	jsonContent := `[
		{
			"fieldPath": "spec.name",
			"writeMode": "immutable",
			"hidden": false
		},
		{
			"fieldPath": "spec.accountId",
			"writeMode": "service-set",
			"hidden": true
		}
	]`

	err := os.WriteFile(validFile, []byte(jsonContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	loaded, err := LoadRegistryFromJSON(validFile)
	if err != nil {
		t.Fatalf("LoadRegistryFromJSON() error = %v", err)
	}

	if len(loaded) != 2 {
		t.Errorf("LoadRegistryFromJSON() loaded %d fields, want 2", len(loaded))
	}

	// Check specific field
	if meta, exists := loaded["spec.name"]; exists {
		if string(meta.WriteMode) != "immutable" {
			t.Errorf("spec.name WriteMode = %s, want immutable", meta.WriteMode)
		}
		if meta.Hidden {
			t.Error("spec.name should not be hidden")
		}
	} else {
		t.Error("spec.name not found in loaded registry")
	}

	// Check hidden field
	if meta, exists := loaded["spec.accountId"]; exists {
		if string(meta.WriteMode) != "service-set" {
			t.Errorf("spec.accountId WriteMode = %s, want service-set", meta.WriteMode)
		}
		if !meta.Hidden {
			t.Error("spec.accountId should be hidden")
		}
	} else {
		t.Error("spec.accountId not found in loaded registry")
	}
}

func TestLoadRegistryFromJSON_EmptyArray(t *testing.T) {
	tmpDir := t.TempDir()
	emptyFile := filepath.Join(tmpDir, "empty.json")

	// Write empty JSON array
	err := os.WriteFile(emptyFile, []byte("[]"), 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	loaded, err := LoadRegistryFromJSON(emptyFile)
	if err != nil {
		t.Fatalf("LoadRegistryFromJSON() error = %v", err)
	}

	if len(loaded) != 0 {
		t.Errorf("Expected empty registry, got %d fields", len(loaded))
	}
}

func TestLoadRegistryFromJSON_WithFeatureGates(t *testing.T) {
	tmpDir := t.TempDir()
	gatedFile := filepath.Join(tmpDir, "gated.json")

	// Write JSON with feature-gated field (array format)
	jsonContent := `[
		{
			"fieldPath": "spec.etcd",
			"writeMode": "mutable",
			"featureGate": "HyperFleetEtcdConfig",
			"hidden": false
		}
	]`

	err := os.WriteFile(gatedFile, []byte(jsonContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	loaded, err := LoadRegistryFromJSON(gatedFile)
	if err != nil {
		t.Fatalf("LoadRegistryFromJSON() error = %v", err)
	}

	if meta, exists := loaded["spec.etcd"]; exists {
		if meta.FeatureGate != "HyperFleetEtcdConfig" {
			t.Errorf("spec.etcd FeatureGate = %s, want HyperFleetEtcdConfig", meta.FeatureGate)
		}
	} else {
		t.Error("spec.etcd not found in loaded registry")
	}
}
