package openapi

import (
	"encoding/json"
	"os"
	"testing"
)

func TestConfigurationUsesLocalType(t *testing.T) {
	tmpFile := t.TempDir() + "/openapi.json"

	// Resolve the v2alpha1 package relative to this test file's location
	// (hack/api-codegen/pkg/openapi/) → ../../../../api/public/v2alpha1
	v2alpha1Dir := "../../../../api/public/v2alpha1"
	if _, err := os.Stat(v2alpha1Dir); err != nil {
		t.Skipf("v2alpha1 source not available: %v", err)
	}

	gen := NewGenerator([]string{v2alpha1Dir}, tmpFile)
	gen.Title = "Test"
	gen.Version = "v1"

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Read output: %v", err)
	}

	var output schemaOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	// ClusterConfiguration must exist as its own definition (from the local type)
	cc, ok := output.Definitions["ClusterConfiguration"]
	if !ok {
		t.Fatal("ClusterConfiguration definition not found")
	}

	// The local type's markers hide all sub-configs except kubelet and machineConfig.
	// If the upstream hypershiftv1beta1.ClusterConfiguration were used instead,
	// all 10 sub-config fields would be present (no hidden markers).
	for _, visible := range []string{"kubelet", "machineConfig"} {
		if _, found := cc.Properties[visible]; !found {
			t.Errorf("expected visible property %q in ClusterConfiguration", visible)
		}
	}
	for _, hidden := range []string{"apiServer", "authentication", "featureGate", "image", "ingress", "network", "oauth", "scheduler", "proxy"} {
		if _, found := cc.Properties[hidden]; found {
			t.Errorf("property %q should be hidden in ClusterConfiguration (local markers not applied?)", hidden)
		}
	}

	// KubeletConfig must retain its visible fields (nested path test)
	kc, ok := output.Definitions["KubeletConfig"]
	if !ok {
		t.Fatal("KubeletConfig definition not found")
	}
	for _, visible := range []string{"podPidsLimit", "maxPods", "containerLogMaxFiles"} {
		if _, found := kc.Properties[visible]; !found {
			t.Errorf("expected visible property %q in KubeletConfig", visible)
		}
	}
	for _, hidden := range []string{"evictionHard", "cpuManagerPolicy", "topologyManagerPolicy"} {
		if _, found := kc.Properties[hidden]; found {
			t.Errorf("property %q should be hidden in KubeletConfig", hidden)
		}
	}
}

func TestGenerateMinimal(t *testing.T) {
	tmpFile := "/tmp/openapi-test.json"
	defer func() { _ = os.Remove(tmpFile) }()

	gen := NewGenerator(nil, tmpFile)
	gen.Title = "Test API"
	gen.Version = "v1"

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if _, err := os.Stat(tmpFile); err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	var output schemaOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("Output is not valid JSON: %v", err)
	}

	if output.OpenAPI != "3.0.0" {
		t.Errorf("Expected OpenAPI 3.0.0, got %s", output.OpenAPI)
	}
	if output.Info.Title != "Test API" {
		t.Errorf("Expected title 'Test API', got %s", output.Info.Title)
	}
	if output.Info.Version != "v1" {
		t.Errorf("Expected version 'v1', got %s", output.Info.Version)
	}
	if len(output.Definitions) != 0 {
		t.Errorf("Expected 0 definitions in minimal mode, got %d", len(output.Definitions))
	}
}
