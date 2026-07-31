package conversion

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewGenerator(t *testing.T) {
	gen := NewGenerator("v1alpha1", "test/pkg", []string{"/test/dir"}, "/test/output")

	if gen.APIVersion != "v1alpha1" {
		t.Errorf("Expected APIVersion v1alpha1, got %s", gen.APIVersion)
	}
	if gen.CRDPackage != "test/pkg" {
		t.Errorf("Expected CRDPackage test/pkg, got %s", gen.CRDPackage)
	}
	if gen.OutputDir != "/test/output" {
		t.Errorf("Expected OutputDir /test/output, got %s", gen.OutputDir)
	}
	if len(gen.InputDirs) != 1 || gen.InputDirs[0] != "/test/dir" {
		t.Errorf("Expected InputDirs [/test/dir], got %v", gen.InputDirs)
	}
	if gen.knownTypes == nil {
		t.Error("Expected knownTypes map to be initialized")
	}
	if gen.typeInfos == nil {
		t.Error("Expected typeInfos map to be initialized")
	}
}

func TestBuildFieldPath(t *testing.T) {
	gen := NewGenerator("v1alpha1", "test", []string{}, "")

	tests := []struct {
		name     string
		typeName string
		jsonName string
		want     string
	}{
		{
			name:     "Spec type",
			typeName: "ClusterSpec",
			jsonName: "displayName",
			want:     "spec.displayName",
		},
		{
			name:     "Status type",
			typeName: "ClusterStatus",
			jsonName: "state",
			want:     "status.state",
		},
		{
			name:     "HostedCluster passthrough",
			typeName: "HostedClusterSpecPassthrough",
			jsonName: "platform",
			want:     "spec.hostedCluster.platform",
		},
		{
			name:     "NodePool passthrough",
			typeName: "NodePoolSpecPassthrough",
			jsonName: "release",
			want:     "spec.nodePool.release",
		},
		{
			name:     "Other type",
			typeName: "ClusterReference",
			jsonName: "name",
			want:     "name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gen.buildFieldPath(tt.typeName, tt.jsonName)
			if got != tt.want {
				t.Errorf("buildFieldPath(%q, %q) = %q, want %q", tt.typeName, tt.jsonName, got, tt.want)
			}
		})
	}
}

func TestExtractJSONTag(t *testing.T) {
	// This would require creating AST field structures
	// For now, just test the basic logic is correct
	t.Skip("Requires AST field creation - tested via integration")
}

func TestExprToString(t *testing.T) {
	// This would require creating AST expression structures
	// For now, just test the basic logic is correct
	t.Skip("Requires AST expression creation - tested via integration")
}

func TestGeneratePassthroughHelpers_PointerAndSlice(t *testing.T) {
	gen := NewGenerator("v1alpha1", "test/pkg", nil, "/out/v1alpha1")
	gen.typeInfos["ChildType"] = &typeInfo{
		Name:       "ChildType",
		StructType: nil,
		Fields: []*fieldInfo{
			{GoName: "Name", JSONName: "name", GoType: "string"},
		},
	}
	gen.typeInfos["TestSpec"] = &typeInfo{
		Name:       "TestSpec",
		StructType: nil,
		Fields: []*fieldInfo{
			{GoName: "PtrField", JSONName: "ptrField", GoType: "*ChildType"},
			{GoName: "SliceField", JSONName: "sliceField", GoType: "[]ChildType"},
			{GoName: "ValField", JSONName: "valField", GoType: "ChildType"},
		},
	}

	code := gen.generatePassthroughHelpers("TestSpec")

	// Value helpers should exist
	if !strings.Contains(code, "func projectChildType(crd v1alpha1.ChildType) rest.ChildType") {
		t.Error("missing value project helper")
	}
	if !strings.Contains(code, "func unprojectChildType(rest rest.ChildType) v1alpha1.ChildType") {
		t.Error("missing value unproject helper")
	}
	// Pointer helpers should exist
	if !strings.Contains(code, "func projectChildTypePtr(crd *v1alpha1.ChildType) *rest.ChildType") {
		t.Error("missing pointer project helper")
	}
	if !strings.Contains(code, "func unprojectChildTypePtr(r *rest.ChildType) *v1alpha1.ChildType") {
		t.Error("missing pointer unproject helper")
	}
	// Pointer helpers must nil-check
	if !strings.Contains(code, "if crd == nil") {
		t.Error("pointer project helper missing nil check")
	}
	if !strings.Contains(code, "if r == nil") {
		t.Error("pointer unproject helper missing nil check")
	}
	// Slice helpers should exist
	if !strings.Contains(code, "func projectChildTypeSlice(crd []v1alpha1.ChildType) []rest.ChildType") {
		t.Error("missing slice project helper")
	}
	if !strings.Contains(code, "func unprojectChildTypeSlice(r []rest.ChildType) []v1alpha1.ChildType") {
		t.Error("missing slice unproject helper")
	}
}

// Note: needsFieldPrefix and makeUniqueFieldName are private helper methods
// They are tested indirectly via the integration tests that verify
// the generated ServiceSetFields struct has correctly prefixed field names

func TestEnsureDir(t *testing.T) {
	gen := NewGenerator("v1alpha1", "test", []string{}, "")

	// Create temp dir for test
	tmpDir := t.TempDir()
	testDir := filepath.Join(tmpDir, "test", "nested", "dir")

	// Directory should not exist yet
	if _, err := os.Stat(testDir); !os.IsNotExist(err) {
		t.Fatalf("Test directory should not exist yet")
	}

	// Create it
	if err := gen.ensureDir(testDir); err != nil {
		t.Fatalf("ensureDir failed: %v", err)
	}

	// Should exist now
	if stat, err := os.Stat(testDir); err != nil {
		t.Fatalf("Directory was not created: %v", err)
	} else if !stat.IsDir() {
		t.Fatal("Path exists but is not a directory")
	}

	// Calling again should be idempotent
	if err := gen.ensureDir(testDir); err != nil {
		t.Fatalf("ensureDir should be idempotent: %v", err)
	}
}
