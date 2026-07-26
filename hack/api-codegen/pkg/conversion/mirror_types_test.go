package conversion

import (
	"testing"
)

func TestGetMirrorMapping(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		wantNil   bool
	}{
		{
			name:      "Configuration exists",
			fieldName: "Configuration",
			wantNil:   false,
		},
		{
			name:      "NonExistentField",
			fieldName: "NonExistentField",
			wantNil:   true,
		},
		{
			name:      "Empty string",
			fieldName: "",
			wantNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetMirrorMapping(tt.fieldName)
			if (got == nil) != tt.wantNil {
				t.Errorf("GetMirrorMapping() nil = %v, want nil = %v", got == nil, tt.wantNil)
			}
			if !tt.wantNil && got != nil {
				// Verify the mapping has required fields
				if got.FieldName != tt.fieldName {
					t.Errorf("GetMirrorMapping().FieldName = %s, want %s", got.FieldName, tt.fieldName)
				}
				if got.HyperFleetType == "" {
					t.Error("GetMirrorMapping().HyperFleetType is empty")
				}
				if got.HyperShiftType == "" {
					t.Error("GetMirrorMapping().HyperShiftType is empty")
				}
				if got.ConversionStrategy == "" {
					t.Error("GetMirrorMapping().ConversionStrategy is empty")
				}
			}
		})
	}
}

func TestGetMirrorMapping_Configuration(t *testing.T) {
	mapping := GetMirrorMapping("Configuration")
	if mapping == nil {
		t.Fatal("GetMirrorMapping(\"Configuration\") returned nil")
	}

	if mapping.FieldName != "Configuration" {
		t.Errorf("FieldName = %s, want Configuration", mapping.FieldName)
	}
	if mapping.HyperFleetType != "v1alpha1.ClusterConfiguration" {
		t.Errorf("HyperFleetType = %s, want v1alpha1.ClusterConfiguration", mapping.HyperFleetType)
	}
	if mapping.HyperShiftType != "v1beta1.ClusterConfiguration" {
		t.Errorf("HyperShiftType = %s, want v1beta1.ClusterConfiguration", mapping.HyperShiftType)
	}
	if mapping.ConversionStrategy != "json-roundtrip" {
		t.Errorf("ConversionStrategy = %s, want json-roundtrip", mapping.ConversionStrategy)
	}
}

func TestIsMirrorType(t *testing.T) {
	tests := []struct {
		name      string
		fieldName string
		want      bool
	}{
		{
			name:      "Configuration is mirror type",
			fieldName: "Configuration",
			want:      true,
		},
		{
			name:      "NonMirrorField is not mirror type",
			fieldName: "NonMirrorField",
			want:      false,
		},
		{
			name:      "Empty string is not mirror type",
			fieldName: "",
			want:      false,
		},
		{
			name:      "Replicas is not mirror type",
			fieldName: "Replicas",
			want:      false,
		},
		{
			name:      "ClusterName is not mirror type",
			fieldName: "ClusterName",
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsMirrorType(tt.fieldName)
			if got != tt.want {
				t.Errorf("IsMirrorType(%s) = %v, want %v", tt.fieldName, got, tt.want)
			}
		})
	}
}

func TestMirrorTypeMappings_Completeness(t *testing.T) {
	// Verify that all mirror type mappings have required fields
	for _, mapping := range mirrorTypeMappings {
		if mapping.FieldName == "" {
			t.Error("Found mapping with empty FieldName")
		}

		if mapping.HyperFleetType == "" {
			t.Errorf("Mirror field %s has empty HyperFleetType", mapping.FieldName)
		}

		if mapping.HyperShiftType == "" {
			t.Errorf("Mirror field %s has empty HyperShiftType", mapping.FieldName)
		}

		if mapping.ConversionStrategy == "" {
			t.Errorf("Mirror field %s has empty ConversionStrategy", mapping.FieldName)
		}

		// Verify we can look it up
		found := GetMirrorMapping(mapping.FieldName)
		if found == nil {
			t.Errorf("Cannot lookup mirror field: %s", mapping.FieldName)
		}
	}
}

func TestIsMirrorType_AllMappings(t *testing.T) {
	// Verify IsMirrorType works for all registered mirror fields
	for _, mapping := range mirrorTypeMappings {
		if !IsMirrorType(mapping.FieldName) {
			t.Errorf("IsMirrorType(%s) = false, want true (registered mirror field)", mapping.FieldName)
		}
	}
}

func TestMirrorTypeMappings_KnownFields(t *testing.T) {
	// Verify specific known mirror fields exist
	knownFields := []string{"Configuration"}

	for _, fieldName := range knownFields {
		if !IsMirrorType(fieldName) {
			t.Errorf("Known mirror field %s not found in registry", fieldName)
		}

		mapping := GetMirrorMapping(fieldName)
		if mapping == nil {
			t.Errorf("GetMirrorMapping(%s) returned nil", fieldName)
		}
	}
}
