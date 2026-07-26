package passthrough

import (
	"go/ast"
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
)

func TestLoadHyperShiftTypes(t *testing.T) {
	// Create generator from import path (resolves via go.mod)
	gen, err := NewGeneratorFromImportPath(
		"github.com/openshift/hypershift/api/hypershift/v1beta1",
		[]string{"HostedClusterSpec"},
		make(markers.FieldRegistry),
	)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	if err := gen.LoadSourceFiles(gen.SourceDir); err != nil {
		t.Fatalf("Failed to load source files: %v", err)
	}

	if len(gen.parsedFiles) == 0 {
		t.Fatal("No files were loaded")
	}

	t.Logf("Loaded %d files from %s", len(gen.parsedFiles), gen.SourceDir)
}

func TestGetMarkersForField_WithRegistry(t *testing.T) {
	registry := markers.FieldRegistry{
		"spec.hostedCluster.autoNode": markers.FieldMeta{
			FieldPath: "spec.hostedCluster.autoNode",
			WriteMode: markers.ServiceSet,
			Hidden:    false,
		},
		"spec.hostedCluster.release": markers.FieldMeta{
			FieldPath: "spec.hostedCluster.release",
			WriteMode: markers.ServiceSet,
			Hidden:    true,
		},
		"spec.hostedCluster.configuration": markers.FieldMeta{
			FieldPath: "spec.hostedCluster.configuration",
			WriteMode: markers.ServiceSet,
			Hidden:    false,
		},
		"spec.hostedCluster.etcd": markers.FieldMeta{
			FieldPath:   "spec.hostedCluster.etcd",
			WriteMode:   markers.Mutable,
			FeatureGate: "HyperFleetEtcd",
			Hidden:      false,
		},
	}

	gen := &Generator{
		Registry:    registry,
		FieldPrefix: "spec.hostedCluster",
		parsedFiles: make(map[string]*ast.File),
	}

	tests := []struct {
		name           string
		jsonTagName    string
		wantOpenAPIGen string
		wantWriteMode  string
		wantGate       string
		wantLen        int
	}{
		{
			name:           "visible field gets openapi-gen=true",
			jsonTagName:    "autoNode",
			wantOpenAPIGen: "+k8s:openapi-gen=true",
			wantWriteMode:  "+hyperfleet:write-mode=service-set",
			wantLen:        2,
		},
		{
			name:           "hidden field gets openapi-gen=false",
			jsonTagName:    "release",
			wantOpenAPIGen: "+k8s:openapi-gen=false",
			wantWriteMode:  "+hyperfleet:write-mode=service-set",
			wantLen:        2,
		},
		{
			name:           "field with feature gate emits gate marker",
			jsonTagName:    "etcd",
			wantOpenAPIGen: "+k8s:openapi-gen=true",
			wantWriteMode:  "+hyperfleet:write-mode=mutable",
			wantGate:       "+openshift:enable:FeatureGate=HyperFleetEtcd",
			wantLen:        3,
		},
		{
			name:           "field not in registry gets defaults",
			jsonTagName:    "unknownField",
			wantOpenAPIGen: "+k8s:openapi-gen=false",
			wantWriteMode:  "+hyperfleet:write-mode=service-set",
			wantLen:        2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			markers := gen.getMarkersForField(tt.jsonTagName)

			if len(markers) != tt.wantLen {
				t.Errorf("expected %d markers, got %d: %v", tt.wantLen, len(markers), markers)
			}

			if markers[0] != tt.wantOpenAPIGen {
				t.Errorf("expected openapi marker %q, got %q", tt.wantOpenAPIGen, markers[0])
			}

			if markers[1] != tt.wantWriteMode {
				t.Errorf("expected write-mode marker %q, got %q", tt.wantWriteMode, markers[1])
			}

			if tt.wantGate != "" && (len(markers) < 3 || markers[2] != tt.wantGate) {
				gate := ""
				if len(markers) >= 3 {
					gate = markers[2]
				}
				t.Errorf("expected gate marker %q, got %q", tt.wantGate, gate)
			}
		})
	}
}

func TestGetMarkersForField_NoPrefix(t *testing.T) {
	registry := markers.FieldRegistry{
		"autoNode": markers.FieldMeta{
			FieldPath: "autoNode",
			WriteMode: markers.ServiceSet,
			Hidden:    false,
		},
	}

	gen := &Generator{
		Registry:    registry,
		parsedFiles: make(map[string]*ast.File),
	}

	m := gen.getMarkersForField("autoNode")
	if m[0] != "+k8s:openapi-gen=true" {
		t.Errorf("expected openapi-gen=true with no prefix, got %q", m[0])
	}
}

func TestDeriveFieldPrefix(t *testing.T) {
	tests := []struct {
		typeName string
		want     string
	}{
		{"HostedClusterSpec", "spec.hostedCluster"},
		{"NodePoolSpec", "spec.nodePool"},
		{"SomeOtherType", ""},
	}
	for _, tt := range tests {
		t.Run(tt.typeName, func(t *testing.T) {
			got := deriveFieldPrefix(tt.typeName)
			if got != tt.want {
				t.Errorf("deriveFieldPrefix(%q) = %q, want %q", tt.typeName, got, tt.want)
			}
		})
	}
}

func TestGenerateTypeDef(t *testing.T) {
	// Create generator from import path (resolves via go.mod)
	gen, err := NewGeneratorFromImportPath(
		"github.com/openshift/hypershift/api/hypershift/v1beta1",
		[]string{"HostedClusterSpec"},
		make(markers.FieldRegistry),
	)
	if err != nil {
		t.Fatalf("Failed to create generator: %v", err)
	}

	if err := gen.LoadSourceFiles(gen.SourceDir); err != nil {
		t.Fatalf("Failed to load source files: %v", err)
	}

	typeDef, err := gen.GenerateTypeDef("HostedClusterSpec")
	if err != nil {
		t.Fatalf("Failed to generate type def: %v", err)
	}

	if typeDef.Name != "HostedClusterSpecPassthrough" {
		t.Errorf("Expected name HostedClusterSpecPassthrough, got %s", typeDef.Name)
	}

	if len(typeDef.Fields) == 0 {
		t.Error("Expected some fields, got none")
	}

	t.Logf("Generated %d fields for %s", len(typeDef.Fields), typeDef.Name)
	for i, field := range typeDef.Fields {
		if i < 5 { // Show first 5 fields
			t.Logf("  Field %d: %s %s `json:\"%s\"`", i, field.Name, field.Type, field.JSONTag)
			t.Logf("    Markers: %v", field.Markers)
		}
	}
}
