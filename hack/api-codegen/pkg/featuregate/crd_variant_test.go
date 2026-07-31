package featuregate

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/registry"
)

func TestCRDVariantGenerator_shouldIncludeField(t *testing.T) {
	g := &CRDVariantGenerator{
		fieldRegistry: map[string]registry.FieldMeta{
			"spec.tags": {
				FieldPath:   "spec.tags",
				WriteMode:   registry.Mutable,
				FeatureGate: "HyperFleetAutoScaling", // TechPreview gate
			},
			"spec.displayName": {
				FieldPath: "spec.displayName",
				WriteMode: registry.Mutable,
				// No feature gate
			},
			"spec.creatorARN": {
				FieldPath: "spec.creatorARN",
				WriteMode: registry.ServiceSet,
				Hidden:    true,
			},
			"spec.hiddenGated": {
				FieldPath:   "spec.hiddenGated",
				WriteMode:   registry.ServiceSet,
				Hidden:      true,
				FeatureGate: "HyperFleetAutoScaling",
			},
		},
	}

	tests := []struct {
		name       string
		fieldPath  string
		featureSet FeatureSet
		want       bool
	}{
		{
			name:       "gated field with Default - excluded",
			fieldPath:  "spec.tags",
			featureSet: Default,
			want:       false,
		},
		{
			name:       "gated field with TechPreview - included",
			fieldPath:  "spec.tags",
			featureSet: TechPreviewNoUpgrade,
			want:       true,
		},
		{
			name:       "non-gated field with Default - included",
			fieldPath:  "spec.displayName",
			featureSet: Default,
			want:       true,
		},
		{
			name:       "structural field (not in registry) - included",
			fieldPath:  "properties",
			featureSet: Default,
			want:       true,
		},
		{
			name:       "type field (not in registry) - included",
			fieldPath:  "type",
			featureSet: Default,
			want:       true,
		},
		{
			name:       "hidden field with Default - excluded",
			fieldPath:  "spec.creatorARN",
			featureSet: Default,
			want:       false,
		},
		{
			name:       "hidden field with TechPreview - excluded",
			fieldPath:  "spec.creatorARN",
			featureSet: TechPreviewNoUpgrade,
			want:       false,
		},
		{
			name:       "hidden and gated field with TechPreview - excluded",
			fieldPath:  "spec.hiddenGated",
			featureSet: TechPreviewNoUpgrade,
			want:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.shouldIncludeField(tt.fieldPath, tt.featureSet)
			if got != tt.want {
				t.Errorf("shouldIncludeField(%q, %v) = %v, want %v", tt.fieldPath, tt.featureSet, got, tt.want)
			}
		})
	}
}

func TestCRDVariantGenerator_GenerateVariant(t *testing.T) {
	// Create a minimal test CRD
	testCRD := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: clusters.hyperfleet.io
spec:
  group: hyperfleet.io
  names:
    kind: Cluster
    plural: clusters
  scope: Namespaced
  versions:
  - name: v1alpha1
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            properties:
              displayName:
                type: string
                description: Display name for the cluster
              tags:
                type: object
                description: Customer tags (TechPreview feature)
                additionalProperties:
                  type: string
`

	// Write test CRD to temp file
	tmpFile, err := os.CreateTemp("", "test-crd-*.yaml")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.WriteString(testCRD); err != nil {
		t.Fatalf("writing test CRD: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("closing temp file: %v", err)
	}

	// Create generator with test registry
	g := &CRDVariantGenerator{
		fieldRegistry: map[string]registry.FieldMeta{
			"spec.tags": {
				FieldPath:   "spec.tags",
				WriteMode:   registry.Mutable,
				FeatureGate: "HyperFleetAutoScaling", // TechPreview
			},
			"spec.displayName": {
				FieldPath: "spec.displayName",
				WriteMode: registry.Mutable,
			},
		},
	}

	tests := []struct {
		name             string
		featureSet       FeatureSet
		shouldContain    []string
		shouldNotContain []string
	}{
		{
			name:             "Default variant excludes gated fields",
			featureSet:       Default,
			shouldContain:    []string{"displayName"},
			shouldNotContain: []string{"tags:"},
		},
		{
			name:             "TechPreview variant includes gated fields",
			featureSet:       TechPreviewNoUpgrade,
			shouldContain:    []string{"displayName", "tags:"},
			shouldNotContain: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := g.WriteVariantToWriter(tmpFile.Name(), &buf, tt.featureSet); err != nil {
				t.Fatalf("GenerateVariant() error = %v", err)
			}

			output := buf.String()

			for _, want := range tt.shouldContain {
				if !strings.Contains(output, want) {
					t.Errorf("output should contain %q but doesn't", want)
				}
			}

			for _, notWant := range tt.shouldNotContain {
				if strings.Contains(output, notWant) {
					t.Errorf("output should not contain %q but does", notWant)
				}
			}
		})
	}
}

func TestCRDVariantGenerator_GenerateAllVariants(t *testing.T) {
	// Create minimal test CRD
	testCRD := `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: test.hyperfleet.io
spec:
  group: hyperfleet.io
  names:
    kind: Test
    plural: tests
`

	// Write test CRD
	tmpFile, err := os.CreateTemp("", "test-crd-*.yaml")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	if _, err := tmpFile.WriteString(testCRD); err != nil {
		t.Fatalf("writing test CRD: %v", err)
	}
	if err := tmpFile.Close(); err != nil {
		t.Fatalf("closing temp file: %v", err)
	}

	// Create temp output directory
	tmpDir := t.TempDir()

	g := NewCRDVariantGenerator()

	// Generate all variants
	if err := g.GenerateAllVariants(tmpFile.Name(), tmpDir, "test"); err != nil {
		t.Fatalf("GenerateAllVariants() error = %v", err)
	}

	// Check that all three variants were created
	expectedFiles := []string{
		"test_default.yaml",
		"test_techpreview.yaml",
		"test_devpreview.yaml",
	}

	for _, filename := range expectedFiles {
		path := tmpDir + "/" + filename
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("expected file %s was not created", filename)
		}
	}
}

func TestNewCRDVariantGenerator(t *testing.T) {
	g := NewCRDVariantGenerator()
	if g == nil {
		t.Fatal("NewCRDVariantGenerator() returned nil")
	}

	// Verify it uses the real registry
	if g.fieldRegistry == nil {
		t.Error("fieldRegistry is nil")
	}

	// Check that canonical prefixed keys exist
	canonicalKeys := []string{
		"spec.displayName",
		"spec.hostedCluster.release",
		"spec.nodePool.release",
	}
	for _, key := range canonicalKeys {
		if _, exists := g.fieldRegistry[key]; !exists {
			t.Errorf("expected canonical key %q to exist in registry", key)
		}
	}

	// Verify bare duplicate keys are absent — these should only appear
	// with their canonical spec.-prefixed form.
	bareKeys := []string{"release", "platform", "pausedUntil"}
	for _, key := range bareKeys {
		if _, exists := g.fieldRegistry[key]; exists {
			t.Errorf("bare key %q should not exist in registry; use canonical prefixed form", key)
		}
	}
}
