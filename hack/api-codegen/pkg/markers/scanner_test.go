package markers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMarkerExtraction(t *testing.T) {
	// Create a temporary directory with test files
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "types.go")
	content := `package test

// Root type - scanner starts here
type Cluster struct {
	Spec ClusterSpec ` + "`json:\"spec\"`" + `
}

type ClusterSpec struct {
	// Customer can set and change
	// +hyperfleet:write-mode=mutable
	DeleteProtection *bool ` + "`json:\"deleteProtection,omitempty\"`" + `

	// Customer sets on create, cannot change
	// +hyperfleet:write-mode=immutable
	Name string ` + "`json:\"name\"`" + `

	// Platform sets, customer cannot see
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	AccountID string ` + "`json:\"accountId\"`" + `

	// Gated field
	// +openshift:enable:FeatureGate=HyperFleetEtcdConfig
	// +hyperfleet:write-mode=immutable
	Etcd *EtcdSpec ` + "`json:\"etcd,omitempty\"`" + `
}

type EtcdSpec struct {
	// +hyperfleet:write-mode=immutable
	ManagementType string ` + "`json:\"managementType\"`" + `
}
`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	// Create scanner and scan
	scanner := NewScanner([]string{tmpDir}, false)
	if err := scanner.Scan(); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Verify results - paths are now fully qualified from root type
	tests := []struct {
		fieldPath   string
		writeMode   WriteMode
		featureGate string
		hidden      bool
	}{
		{"spec.deleteProtection", Mutable, "", false},
		{"spec.name", Immutable, "", false},
		{"spec.accountId", ServiceSet, "", true},
		{"spec.etcd", Immutable, "HyperFleetEtcdConfig", false},
		{"spec.etcd.managementType", Immutable, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.fieldPath, func(t *testing.T) {
			meta, found := scanner.Registry[tt.fieldPath]
			if !found {
				t.Fatalf("Field %s not found in registry", tt.fieldPath)
			}

			if meta.WriteMode != tt.writeMode {
				t.Errorf("WriteMode = %v, want %v", meta.WriteMode, tt.writeMode)
			}

			if meta.FeatureGate != tt.featureGate {
				t.Errorf("FeatureGate = %v, want %v", meta.FeatureGate, tt.featureGate)
			}

			if meta.Hidden != tt.hidden {
				t.Errorf("Hidden = %v, want %v", meta.Hidden, tt.hidden)
			}
		})
	}
}

func TestPassthroughTypesPrefixed(t *testing.T) {
	tmpDir := t.TempDir()

	testFile := filepath.Join(tmpDir, "types.go")
	content := `package test

type HostedClusterSpecPassthrough struct {
	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=service-set
	PausedUntil *string ` + "`json:\"pausedUntil,omitempty\"`" + `

	// +k8s:openapi-gen=true
	// +hyperfleet:write-mode=immutable
	FIPS bool ` + "`json:\"fips\"`" + `
}

type NodePoolSpecPassthrough struct {
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	PausedUntil *string ` + "`json:\"pausedUntil,omitempty\"`" + `

	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Replicas *int ` + "`json:\"replicas,omitempty\"`" + `
}
`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	scanner := NewScanner([]string{tmpDir}, false)
	if err := scanner.Scan(); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	tests := []struct {
		fieldPath string
		writeMode WriteMode
		hidden    bool
	}{
		{"spec.hostedCluster.pausedUntil", ServiceSet, false},
		{"spec.hostedCluster.fips", Immutable, false},
		{"spec.nodePool.pausedUntil", ServiceSet, true},
		{"spec.nodePool.replicas", ServiceSet, true},
	}

	for _, tt := range tests {
		t.Run(tt.fieldPath, func(t *testing.T) {
			meta, found := scanner.Registry[tt.fieldPath]
			if !found {
				t.Fatalf("Field %s not found in registry (keys: %v)", tt.fieldPath, registryKeys(scanner.Registry))
			}
			if meta.WriteMode != tt.writeMode {
				t.Errorf("WriteMode = %v, want %v", meta.WriteMode, tt.writeMode)
			}
			if meta.Hidden != tt.hidden {
				t.Errorf("Hidden = %v, want %v", meta.Hidden, tt.hidden)
			}
		})
	}

	// Verify no flat "pausedUntil" key exists (the old collision)
	if _, found := scanner.Registry["pausedUntil"]; found {
		t.Error("flat key \"pausedUntil\" should not exist; passthrough fields must be prefixed")
	}
}

func registryKeys(r FieldRegistry) []string {
	keys := make([]string, 0, len(r))
	for k := range r {
		keys = append(keys, k)
	}
	return keys
}

func TestValidation(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr bool
	}{
		{
			name: "valid - all visible fields have write mode",
			content: `package test
type Root struct {
	Spec Spec ` + "`json:\"spec\"`" + `
}
type Spec struct {
	// +hyperfleet:write-mode=mutable
	Field string ` + "`json:\"field\"`" + `
}`,
			wantErr: false,
		},
		{
			name: "invalid - field has marker but missing write mode",
			content: `package test
type Root struct {
	Spec Spec ` + "`json:\"spec\"`" + `
}
type Spec struct {
	// +openshift:enable:FeatureGate=Test
	Field string ` + "`json:\"field\"`" + `
}`,
			wantErr: true,
		},
		{
			name: "valid - hidden field without write mode is OK",
			content: `package test
type Root struct {
	Spec Spec ` + "`json:\"spec\"`" + `
}
type Spec struct {
	// +k8s:openapi-gen=false
	// +hyperfleet:write-mode=service-set
	Field string ` + "`json:\"field\"`" + `
}`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			testFile := filepath.Join(tmpDir, "types.go")

			if err := os.WriteFile(testFile, []byte(tt.content), 0644); err != nil {
				t.Fatalf("Failed to write test file: %v", err)
			}

			scanner := NewScanner([]string{tmpDir}, false)
			if err := scanner.Scan(); err != nil {
				t.Fatalf("Scan failed: %v", err)
			}

			err := scanner.Registry.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
