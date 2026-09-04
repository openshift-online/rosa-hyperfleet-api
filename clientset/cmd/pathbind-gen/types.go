package main

// ── FieldRegistry (field_metadata.json) ──────────────────────────────────────

type RegistryEntry struct {
	FieldPath string `json:"fieldPath"`
	WriteMode string `json:"writeMode"`
	Hidden    bool   `json:"hidden"`
	OwnerType string `json:"ownerType"`
}

// ── Draft YAML (pathbind-draft.yaml) ─────────────────────────────────────────

type Draft struct {
	Resources map[string]DraftResource `yaml:"resources"`
}

type DraftResource struct {
	SDKType string       `yaml:"sdkType"`
	Fields  []DraftField `yaml:"fields"`
}

type DraftField struct {
	Path       string   `yaml:"path"`
	GoType     string   `yaml:"goType,omitempty"` // resolved from OpenAPI schema (string, boolean, integer, number, array, map)
	Operations []string `yaml:"operations"`
}

// ── Overrides YAML (pathbind-overrides.yaml) ──────────────────────────────────

type Overrides struct {
	Config    OverridesConfig             `yaml:"config"`
	Resources map[string]OverrideResource `yaml:"resources"`
}

// OverridesConfig holds consumer-specific generator settings.
type OverridesConfig struct {
	Package        string `yaml:"package"`
	RuntimePkg     string `yaml:"runtimePkg"`
	RuntimeType    string `yaml:"runtimeType"`
	InteractivePkg string `yaml:"interactivePkg"`
}

type OverrideResource struct {
	Aliases []OverrideAlias `yaml:"aliases"`
}

type OverrideAlias struct {
	Path        string   `yaml:"path"`        // matches draft field; empty for consumer-only derived fields
	Alias       string   `yaml:"alias"`       // camelCase; defaults to leaf segment of path
	Type        string   `yaml:"type"`        // Go consumer type; defaults per SDK type mapping
	GoName      string   `yaml:"goName"`      // PascalCase override for acronyms (VPC, APIURL)
	Flag        *string  `yaml:"flag"`        // cobra flag name; omitted = auto-derive, nil = suppress, "" explicitly suppresses
	Description string   `yaml:"description"` // CLI help text; required when flag is set
	Required    *bool    `yaml:"required"`    // interactive mode prompts if empty; no draft-derived default
	Operations  []string `yaml:"operations"`  // [create], [update], [create,update]; inherited from draft if absent
}

// ── merged view used by the cobra generator ───────────────────────────────────

type mergedAlias struct {
	GoName      string
	Type        string
	Path        string // hfsdk tag value ("-" for consumer-only derived fields)
	Flag        string
	Description string
	Required    bool
	Operations  []string
	HasFlag     bool
}
