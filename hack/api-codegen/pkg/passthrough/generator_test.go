package passthrough

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
)

func TestGenerate(t *testing.T) {
	// Create generator from import path (resolves via go.mod)
	gen, err := NewGeneratorFromImportPath(
		"github.com/openshift/hypershift/api/hypershift/v1beta1",
		[]string{"HostedClusterSpec"},
		make(markers.FieldRegistry),
	)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	if err := gen.LoadSourceFiles(gen.SourceDir); err != nil {
		t.Fatalf("Failed to load source files: %v", err)
	}

	// Create temp output directory
	tmpDir := t.TempDir()

	t.Logf("Generating to: %s", tmpDir)
	if err := gen.Generate(tmpDir); err != nil {
		// Try to read raw output for debugging
		rawFile := filepath.Join(tmpDir, "zz_generated.passthrough.go.raw")
		if raw, err2 := os.ReadFile(rawFile); err2 == nil {
			t.Logf("Raw generated output:\n%s", raw)
		}
		t.Fatalf("Failed to generate: %v", err)
	}

	// Check output file exists
	outputFile := filepath.Join(tmpDir, "zz_generated.passthrough.go")
	if _, err := os.Stat(outputFile); err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	// Read and display generated content
	content, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	t.Logf("Generated file size: %d bytes", len(content))

	// Show first 50 lines
	lines := 0
	for i, b := range content {
		if b == '\n' {
			lines++
			if lines >= 50 {
				t.Logf("First 50 lines of generated code:\n%s\n... (truncated)", content[:i])
				break
			}
		}
	}
	if lines < 50 {
		t.Logf("Generated code:\n%s", content)
	}
}
