package featuregate

import (
	"strings"
	"testing"
)

func TestFeatureStageHierarchy(t *testing.T) {
	tests := []struct {
		name          string
		featureSet    FeatureSet
		stage         FeatureStage
		shouldInclude bool
	}{
		{"Default includes GA", Default, GA, true},
		{"Default excludes TechPreview", Default, TechPreview, false},
		{"Default excludes DevPreview", Default, DevPreview, false},

		{"TechPreview includes GA", TechPreviewNoUpgrade, GA, true},
		{"TechPreview includes TechPreview", TechPreviewNoUpgrade, TechPreview, true},
		{"TechPreview excludes DevPreview", TechPreviewNoUpgrade, DevPreview, false},

		{"DevPreview includes GA", DevPreviewNoUpgrade, GA, true},
		{"DevPreview includes TechPreview", DevPreviewNoUpgrade, TechPreview, true},
		{"DevPreview includes DevPreview", DevPreviewNoUpgrade, DevPreview, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.featureSet.Includes(tt.stage)
			if got != tt.shouldInclude {
				t.Errorf("FeatureSet.Includes() = %v, want %v", got, tt.shouldInclude)
			}
		})
	}
}

func TestIsGateEnabled(t *testing.T) {
	tests := []struct {
		name       string
		gate       string
		featureSet FeatureSet
		want       bool
	}{
		{"GA gate in Default", "HyperFleetEtcdConfig", Default, true},
		{"GA gate in TechPreview", "HyperFleetEtcdConfig", TechPreviewNoUpgrade, true},
		{"GA gate in DevPreview", "HyperFleetEtcdConfig", DevPreviewNoUpgrade, true},

		{"TechPreview gate in Default", "HyperFleetAutoScaling", Default, false},
		{"TechPreview gate in TechPreview", "HyperFleetAutoScaling", TechPreviewNoUpgrade, true},
		{"TechPreview gate in DevPreview", "HyperFleetAutoScaling", DevPreviewNoUpgrade, true},

		{"DevPreview gate in Default", "HyperFleetCustomDNS", Default, false},
		{"DevPreview gate in TechPreview", "HyperFleetCustomDNS", TechPreviewNoUpgrade, false},
		{"DevPreview gate in DevPreview", "HyperFleetCustomDNS", DevPreviewNoUpgrade, true},

		{"Unknown gate is disabled", "NonExistentGate", DevPreviewNoUpgrade, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsGateEnabled(tt.gate, tt.featureSet)
			if got != tt.want {
				t.Errorf("IsGateEnabled() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGatesForFeatureSet(t *testing.T) {
	tests := []struct {
		name       string
		featureSet FeatureSet
		wantCount  int
	}{
		{"Default has 1 gate (GA only)", Default, 1},
		{"TechPreview has 5 gates (GA + TechPreview)", TechPreviewNoUpgrade, 5},
		{"DevPreview has 6 gates (GA + TechPreview + DevPreview)", DevPreviewNoUpgrade, 6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gates := GatesForFeatureSet(tt.featureSet)
			if len(gates) != tt.wantCount {
				t.Errorf("GatesForFeatureSet() returned %d gates, want %d", len(gates), tt.wantCount)
			}
		})
	}
}

func TestFilterCRDFields(t *testing.T) {
	// This test verifies that feature gates properly filter fields
	// Note: Actual field counts depend on the current registry

	defaultFields := FilterCRDFields(Default)
	techPreviewFields := FilterCRDFields(TechPreviewNoUpgrade)
	devPreviewFields := FilterCRDFields(DevPreviewNoUpgrade)

	// DevPreview should have >= TechPreview should have >= Default
	if len(defaultFields) > len(techPreviewFields) {
		t.Errorf("Default has more fields (%d) than TechPreview (%d)",
			len(defaultFields), len(techPreviewFields))
	}

	if len(techPreviewFields) > len(devPreviewFields) {
		t.Errorf("TechPreview has more fields (%d) than DevPreview (%d)",
			len(techPreviewFields), len(devPreviewFields))
	}

	t.Logf("Default: %d fields", len(defaultFields))
	t.Logf("TechPreview: %d fields", len(techPreviewFields))
	t.Logf("DevPreview: %d fields", len(devPreviewFields))
}

func TestFieldsForFeatureSet(t *testing.T) {
	tests := []struct {
		name       string
		featureSet FeatureSet
		minFields  int // Minimum expected fields (depends on registry)
	}{
		{"Default has GA fields only", Default, 1},
		{"TechPreview has GA + TechPreview fields", TechPreviewNoUpgrade, 1},
		{"DevPreview has all fields", DevPreviewNoUpgrade, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fields := FieldsForFeatureSet(tt.featureSet)
			if len(fields) < tt.minFields {
				t.Errorf("FieldsForFeatureSet(%s) returned %d fields, want at least %d",
					tt.featureSet, len(fields), tt.minFields)
			}

			// Verify hierarchy: DevPreview >= TechPreview >= Default
			if tt.featureSet == Default {
				techFields := FieldsForFeatureSet(TechPreviewNoUpgrade)
				if len(fields) > len(techFields) {
					t.Errorf("Default has more fields (%d) than TechPreview (%d)",
						len(fields), len(techFields))
				}
			}
		})
	}
}

func TestSummarizeFeatureSet(t *testing.T) {
	summary := SummarizeFeatureSet()

	// Should return a non-empty string
	if summary == "" {
		t.Error("SummarizeFeatureSet() returned empty string")
	}

	// Should mention all three feature sets
	if !strings.Contains(summary, "Default") {
		t.Error("Summary missing Default feature set")
	}
	if !strings.Contains(summary, "TechPreviewNoUpgrade") {
		t.Error("Summary missing TechPreviewNoUpgrade feature set")
	}
	if !strings.Contains(summary, "DevPreviewNoUpgrade") {
		t.Error("Summary missing DevPreviewNoUpgrade feature set")
	}

	// Should mention total fields
	if !strings.Contains(summary, "Total fields") {
		t.Error("Summary missing 'Total fields' information")
	}

	t.Logf("Summary:\n%s", summary)
}

func TestFeatureStageString(t *testing.T) {
	tests := []struct {
		stage FeatureStage
		want  string
	}{
		{GA, "GA"},
		{TechPreview, "TechPreview"},
		{DevPreview, "DevPreview"},
		{FeatureStage(999), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := tt.stage.String()
			if got != tt.want {
				t.Errorf("FeatureStage.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMaxStage(t *testing.T) {
	tests := []struct {
		name       string
		featureSet FeatureSet
		want       FeatureStage
	}{
		{"Default max is GA", Default, GA},
		{"TechPreview max is TechPreview", TechPreviewNoUpgrade, TechPreview},
		{"DevPreview max is DevPreview", DevPreviewNoUpgrade, DevPreview},
		{"Unknown defaults to GA", FeatureSet("unknown"), GA},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.featureSet.MaxStage()
			if got != tt.want {
				t.Errorf("FeatureSet.MaxStage() = %v, want %v", got, tt.want)
			}
		})
	}
}
