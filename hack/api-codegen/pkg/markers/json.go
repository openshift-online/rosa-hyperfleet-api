package markers

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type jsonField struct {
	FieldPath                  string                 `json:"fieldPath"`
	UpdateAction               string                 `json:"updateAction,omitempty"`
	WriteMode                  string                 `json:"writeMode,omitempty"`
	FeatureGate                string                 `json:"featureGate,omitempty"`
	Hidden                     bool                   `json:"hidden,omitempty"`
	IsReducedContainer         bool                   `json:"reducedContainer,omitempty"`
	FeatureGateAwareWriteModes []FeatureGateWriteMode `json:"featureGateAwareWriteModes,omitempty"`
	OwnerType                  string                 `json:"ownerType"`
	OwnerGVK                   string                 `json:"ownerGVK"`
}

// GenerateJSON creates a JSON file from the field registry for use by other tools
func (s *MarkerScanner) GenerateJSON(outputFile string) error {
	// Ensure output directory exists
	dir := filepath.Dir(outputFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	// Collect all fields with their owners
	var fields []jsonField

	// Sort owners for deterministic output
	var owners []string
	for owner := range s.TypedRegistry {
		owners = append(owners, owner)
	}
	sort.Strings(owners)

	// Process each owner type
	for _, owner := range owners {
		ownerFields := s.TypedRegistry[owner]

		// Sort field paths for this owner
		var paths []string
		for path := range ownerFields {
			paths = append(paths, path)
		}
		sort.Strings(paths)

		// Add each field to the output
		for _, path := range paths {
			meta := ownerFields[path]
			field := jsonField{
				UpdateAction:               meta.UpdateAction,
				FieldPath:                  meta.FieldPath,
				WriteMode:                  string(meta.WriteMode),
				FeatureGate:                meta.FeatureGate,
				Hidden:                     meta.Hidden,
				IsReducedContainer:         meta.IsReducedContainer,
				FeatureGateAwareWriteModes: meta.FeatureGateAwareWriteModes,
				OwnerType:                  meta.OwnerType,
				OwnerGVK:                   meta.OwnerGVK,
			}
			fields = append(fields, field)
		}
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

// LoadTypedRegistryFromJSON loads a typed field registry from a JSON file
func LoadTypedRegistryFromJSON(jsonFile string) (TypedFieldRegistry, error) {
	data, err := os.ReadFile(jsonFile)
	if err != nil {
		return nil, fmt.Errorf("reading JSON file: %w", err)
	}
	return LoadTypedRegistryFromJSONBytes(data)
}

// LoadTypedRegistryFromJSONBytes loads a typed field registry from raw JSON bytes
func LoadTypedRegistryFromJSONBytes(data []byte) (TypedFieldRegistry, error) {
	var fields []jsonField
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("unmarshaling JSON: %w", err)
	}

	// Build typed registry indexed by owner type
	registry := make(TypedFieldRegistry)
	for _, field := range fields {
		if registry[field.OwnerType] == nil {
			registry[field.OwnerType] = make(map[string]FieldMeta)
		}

		registry[field.OwnerType][field.FieldPath] = FieldMeta{
			UpdateAction:               field.UpdateAction,
			FieldPath:                  field.FieldPath,
			WriteMode:                  WriteMode(field.WriteMode),
			FeatureGate:                field.FeatureGate,
			Hidden:                     field.Hidden,
			IsReducedContainer:         field.IsReducedContainer,
			FeatureGateAwareWriteModes: field.FeatureGateAwareWriteModes,
			OwnerType:                  field.OwnerType,
			OwnerGVK:                   field.OwnerGVK,
		}
	}

	return registry, nil
}
