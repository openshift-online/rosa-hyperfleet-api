package passthrough_test

import (
	"os"
	"path/filepath"
	"testing"

	// Import HyperShift API to ensure it's in go.mod as a direct dependency
	_ "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/passthrough"
)

// TestGenerateFromHyperShiftModule verifies passthrough generation from go.mod dependency
func TestGenerateFromHyperShiftModule(t *testing.T) {
	// Create generator from import path (resolves via go.mod)
	registry := make(markers.FieldRegistry)
	types := []string{"HostedClusterSpec", "NodePoolSpec"}

	gen, err := passthrough.NewGeneratorFromImportPath(
		"github.com/openshift/hypershift/api/hypershift/v1beta1",
		types,
		registry,
	)
	if err != nil {
		t.Fatalf("Failed to create generator from import path: %v", err)
	}

	// Verify source directory was resolved
	if gen.SourceDir == "" {
		t.Fatal("SourceDir is empty")
	}

	t.Logf("Resolved source directory: %s", gen.SourceDir)

	// Load source files
	if err := gen.LoadSourceFiles(gen.SourceDir); err != nil {
		t.Fatalf("Failed to load source files: %v", err)
	}

	parsedFiles := gen.ParsedFiles()
	if len(parsedFiles) == 0 {
		t.Fatal("No source files were parsed")
	}

	t.Logf("Loaded %d source files", len(parsedFiles))

	// Generate to temp directory
	outputDir := t.TempDir()
	if err := gen.Generate(outputDir); err != nil {
		t.Fatalf("Failed to generate: %v", err)
	}

	// Verify output file exists
	outputFile := filepath.Join(outputDir, "zz_generated.passthrough.go")
	if _, err := os.Stat(outputFile); os.IsNotExist(err) {
		t.Fatalf("Output file not created: %s", outputFile)
	}

	// Read and verify output
	content, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output file: %v", err)
	}

	// Basic sanity checks
	contentStr := string(content)
	if len(content) == 0 {
		t.Fatal("Generated file is empty")
	}

	// Check for expected type definitions
	if !contains(contentStr, "type HostedClusterSpecPassthrough struct") {
		t.Error("Missing HostedClusterSpecPassthrough type definition")
	}

	if !contains(contentStr, "type NodePoolSpecPassthrough struct") {
		t.Error("Missing NodePoolSpecPassthrough type definition")
	}

	// Check for safe default markers
	if !contains(contentStr, "+k8s:openapi-gen=false") {
		t.Error("Missing visibility marker")
	}

	if !contains(contentStr, "+hyperfleet:write-mode=service-set") {
		t.Error("Missing write-mode marker")
	}

	t.Logf("Successfully generated %d bytes", len(content))
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && len(s) >= len(substr) && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
