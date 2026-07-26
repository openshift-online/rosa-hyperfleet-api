package markers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// GenerateJSON creates a JSON file from the field registry for use by other tools
func (s *MarkerScanner) GenerateJSON(outputFile string) error {
	// Ensure output directory exists
	dir := filepath.Dir(outputFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	// Convert registry to sorted slice for deterministic output
	type jsonField struct {
		FieldPath                  string                 `json:"fieldPath"`
		WriteMode                  string                 `json:"writeMode,omitempty"`
		FeatureGate                string                 `json:"featureGate,omitempty"`
		Hidden                     bool                   `json:"hidden,omitempty"`
		FeatureGateAwareWriteModes []FeatureGateWriteMode `json:"featureGateAwareWriteModes,omitempty"`
	}

	var fields []jsonField
	var paths []string
	for path := range s.Registry {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	for _, path := range paths {
		meta := s.Registry[path]
		field := jsonField{
			FieldPath:                  meta.FieldPath,
			WriteMode:                  string(meta.WriteMode),
			FeatureGate:                meta.FeatureGate,
			Hidden:                     meta.Hidden,
			FeatureGateAwareWriteModes: meta.FeatureGateAwareWriteModes,
		}
		fields = append(fields, field)
	}

	// Marshal to JSON with indentation
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling JSON: %w", err)
	}

	// Write to file
	if err := os.WriteFile(outputFile, data, 0644); err != nil {
		return fmt.Errorf("writing output file: %w", err)
	}

	return nil
}

// LoadRegistryFromJSON loads a field registry from a JSON file
func LoadRegistryFromJSON(jsonFile string) (FieldRegistry, error) {
	data, err := os.ReadFile(jsonFile)
	if err != nil {
		return nil, fmt.Errorf("reading JSON file: %w", err)
	}
	return LoadRegistryFromJSONBytes(data)
}

// LoadRegistryFromJSONBytes loads a field registry from raw JSON bytes
func LoadRegistryFromJSONBytes(data []byte) (FieldRegistry, error) {
	type jsonField struct {
		FieldPath                  string                 `json:"fieldPath"`
		WriteMode                  string                 `json:"writeMode,omitempty"`
		FeatureGate                string                 `json:"featureGate,omitempty"`
		Hidden                     bool                   `json:"hidden,omitempty"`
		FeatureGateAwareWriteModes []FeatureGateWriteMode `json:"featureGateAwareWriteModes,omitempty"`
	}

	var fields []jsonField
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("unmarshaling JSON: %w", err)
	}

	registry := make(FieldRegistry)
	for _, field := range fields {
		registry[field.FieldPath] = FieldMeta{
			FieldPath:                  field.FieldPath,
			WriteMode:                  WriteMode(field.WriteMode),
			FeatureGate:                field.FeatureGate,
			Hidden:                     field.Hidden,
			FeatureGateAwareWriteModes: field.FeatureGateAwareWriteModes,
		}
	}

	return registry, nil
}
