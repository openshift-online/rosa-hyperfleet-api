package featuregate

// FeatureStage represents the maturity stage of a feature gate
type FeatureStage int

const (
	// GA features are generally available to all customers
	GA FeatureStage = iota

	// TechPreview features are available to customers who opt into tech preview
	// Includes all GA features
	TechPreview

	// DevPreview features are available only for development/testing
	// Includes all GA and TechPreview features
	DevPreview
)

// String returns the string representation of a FeatureStage
func (s FeatureStage) String() string {
	switch s {
	case GA:
		return "GA"
	case TechPreview:
		return "TechPreview"
	case DevPreview:
		return "DevPreview"
	default:
		return "Unknown"
	}
}

// FeatureGateInfo describes a single feature gate
type FeatureGateInfo struct {
	// Stage is the maturity stage of this gate
	Stage FeatureStage

	// Description explains what this gate controls
	Description string
}

// FeatureSet represents a collection of feature gates
type FeatureSet string

const (
	// Default includes only GA features
	Default FeatureSet = "Default"

	// TechPreviewNoUpgrade includes GA + TechPreview features
	// "NoUpgrade" indicates customers cannot upgrade clusters with these features
	TechPreviewNoUpgrade FeatureSet = "TechPreviewNoUpgrade"

	// DevPreviewNoUpgrade includes GA + TechPreview + DevPreview features
	DevPreviewNoUpgrade FeatureSet = "DevPreviewNoUpgrade"
)

// MaxStage returns the maximum feature stage included in this feature set
func (fs FeatureSet) MaxStage() FeatureStage {
	switch fs {
	case Default:
		return GA
	case TechPreviewNoUpgrade:
		return TechPreview
	case DevPreviewNoUpgrade:
		return DevPreview
	default:
		return GA
	}
}

// Includes returns true if this feature set includes the given stage
func (fs FeatureSet) Includes(stage FeatureStage) bool {
	return stage <= fs.MaxStage()
}
