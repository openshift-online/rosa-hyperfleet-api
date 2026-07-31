package featuregate

import (
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/registry"
)

// openAPIKeywords are leaf names in an OpenAPI schema that describe the schema
// itself rather than user-defined properties. shouldIncludeField uses this set
// to avoid warning about paths that are structural, not missing registrations.
var openAPIKeywords = map[string]bool{
	"type": true, "description": true, "required": true, "format": true,
	"minimum": true, "maximum": true, "enum": true, "default": true,
	"pattern": true, "minLength": true, "maxLength": true,
	"minItems": true, "maxItems": true, "uniqueItems": true,
	"nullable": true, "readOnly": true, "writeOnly": true,
	"example": true, "allOf": true, "oneOf": true, "anyOf": true, "not": true,
}

// CRDVariantGenerator generates feature-set-specific CRD variants
type CRDVariantGenerator struct {
	fieldRegistry map[string]registry.FieldMeta
}

// NewCRDVariantGenerator creates a new CRD variant generator
func NewCRDVariantGenerator() *CRDVariantGenerator {
	return &CRDVariantGenerator{
		fieldRegistry: registry.FieldRegistry,
	}
}

// GenerateVariant reads a base CRD and generates a filtered variant for a feature set
func (g *CRDVariantGenerator) GenerateVariant(inputPath string, outputPath string, featureSet FeatureSet) error {
	// Read input CRD
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading CRD: %w", err)
	}

	// Parse YAML
	var crd yaml.Node
	if err := yaml.Unmarshal(data, &crd); err != nil {
		return fmt.Errorf("parsing YAML: %w", err)
	}

	// Filter the CRD based on feature set
	ctx := &filterContext{
		featureSet: featureSet,
		inSchema:   false,
		fieldPath:  "",
	}
	if err := g.filterCRDNode(&crd, ctx); err != nil {
		return fmt.Errorf("filtering CRD: %w", err)
	}

	// Write output
	f, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("creating output file: %w", err)
	}

	encoder := yaml.NewEncoder(f)
	encoder.SetIndent(2)
	if err := encoder.Encode(&crd); err != nil {
		f.Close()
		return fmt.Errorf("writing YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		f.Close()
		return fmt.Errorf("closing YAML encoder: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing output file: %w", err)
	}

	return nil
}

type filterContext struct {
	featureSet FeatureSet
	inSchema   bool   // true when inside openAPIV3Schema.properties
	fieldPath  string // current field path (e.g., "spec.tags")
}

// filterCRDNode walks the CRD YAML tree and removes fields not available in the feature set
func (g *CRDVariantGenerator) filterCRDNode(node *yaml.Node, ctx *filterContext) error {
	if node == nil {
		return nil
	}

	switch node.Kind {
	case yaml.DocumentNode:
		// Process document content
		for _, child := range node.Content {
			if err := g.filterCRDNode(child, ctx); err != nil {
				return err
			}
		}

	case yaml.MappingNode:
		// Process key-value pairs
		// YAML mappings have alternating key/value nodes
		newContent := make([]*yaml.Node, 0, len(node.Content))

		for i := 0; i < len(node.Content); i += 2 {
			if i+1 >= len(node.Content) {
				break
			}

			keyNode := node.Content[i]
			valueNode := node.Content[i+1]
			fieldName := keyNode.Value

			// Track when we enter the schema's properties section
			enteringSchema := !ctx.inSchema && fieldName == "properties"

			// Save old context
			oldInSchema := ctx.inSchema
			oldFieldPath := ctx.fieldPath

			// Update context for this key
			isStructuralWrapper := fieldName == "items" || fieldName == "additionalProperties"
			if enteringSchema {
				ctx.inSchema = true
			} else if ctx.inSchema && fieldName != "properties" && !isStructuralWrapper {
				// We're inside schema properties, build field path
				if ctx.fieldPath == "" {
					ctx.fieldPath = fieldName
				} else {
					ctx.fieldPath = ctx.fieldPath + "." + fieldName
				}
			}

			// Check if we should include this field
			shouldInclude := true
			if ctx.inSchema && fieldName != "properties" && ctx.fieldPath != "" {
				shouldInclude = g.shouldIncludeField(ctx.fieldPath, ctx.featureSet)
			}

			if shouldInclude {
				// Recurse into value
				if err := g.filterCRDNode(valueNode, ctx); err != nil {
					return err
				}
				newContent = append(newContent, keyNode, valueNode)
			}

			// Restore context
			ctx.inSchema = oldInSchema
			ctx.fieldPath = oldFieldPath
		}

		node.Content = newContent

	case yaml.SequenceNode:
		// Process array elements
		for _, child := range node.Content {
			if err := g.filterCRDNode(child, ctx); err != nil {
				return err
			}
		}
	}

	return nil
}

// shouldIncludeField checks if a field should be included in the given feature set
func (g *CRDVariantGenerator) shouldIncludeField(fieldPath string, featureSet FeatureSet) bool {
	// Check if field is in registry
	meta, exists := g.fieldRegistry[fieldPath]
	if !exists {
		leaf := fieldPath
		if idx := strings.LastIndex(fieldPath, "."); idx != -1 {
			leaf = fieldPath[idx+1:]
		}
		if !openAPIKeywords[leaf] && !strings.HasPrefix(leaf, "x-kubernetes-") {
			log.Printf("WARNING: field path %q not found in registry, including by default", fieldPath)
		}
		return true
	}

	if meta.Hidden {
		return false
	}

	// If field has a feature gate, check if it's enabled
	if meta.FeatureGate != "" {
		return IsGateEnabled(meta.FeatureGate, featureSet)
	}

	// No feature gate - always include
	return true
}

// GenerateAllVariants generates CRD variants for all feature sets
func (g *CRDVariantGenerator) GenerateAllVariants(inputPath string, outputDir string, baseName string) error {
	featureSets := []struct {
		set    FeatureSet
		suffix string
	}{
		{Default, "default"},
		{TechPreviewNoUpgrade, "techpreview"},
		{DevPreviewNoUpgrade, "devpreview"},
	}

	for _, fs := range featureSets {
		outputPath := fmt.Sprintf("%s/%s_%s.yaml", outputDir, baseName, fs.suffix)
		if err := g.GenerateVariant(inputPath, outputPath, fs.set); err != nil {
			return fmt.Errorf("generating %s variant: %w", fs.suffix, err)
		}
	}

	return nil
}

// WriteVariantToWriter generates a variant and writes it to a writer (useful for testing)
func (g *CRDVariantGenerator) WriteVariantToWriter(inputPath string, w io.Writer, featureSet FeatureSet) error {
	// Read input CRD
	data, err := os.ReadFile(inputPath)
	if err != nil {
		return fmt.Errorf("reading CRD: %w", err)
	}

	// Parse YAML
	var crd yaml.Node
	if err := yaml.Unmarshal(data, &crd); err != nil {
		return fmt.Errorf("parsing YAML: %w", err)
	}

	// Filter the CRD based on feature set
	ctx := &filterContext{
		featureSet: featureSet,
		inSchema:   false,
		fieldPath:  "",
	}
	if err := g.filterCRDNode(&crd, ctx); err != nil {
		return fmt.Errorf("filtering CRD: %w", err)
	}

	// Write to writer
	encoder := yaml.NewEncoder(w)
	encoder.SetIndent(2)
	if err := encoder.Encode(&crd); err != nil {
		return fmt.Errorf("writing YAML: %w", err)
	}

	return nil
}
