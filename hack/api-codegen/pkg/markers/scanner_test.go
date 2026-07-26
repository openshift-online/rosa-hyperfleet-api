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
	scanner := NewScanner([]string{tmpDir})
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

			scanner := NewScanner([]string{tmpDir})
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
