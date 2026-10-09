package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/clientset/cmd/pathbind-gen/pkg"
)

func TestRunInitIncludesMutableFieldsHiddenFromKubernetesOpenAPI(t *testing.T) {
	registry := []pkg.RegistryEntry{
		{FieldPath: "spec.nodePool.nodeLabels", WriteMode: "mutable", Hidden: true, OwnerType: "NodePool"},
		{FieldPath: "spec.nodePool.serviceField", WriteMode: "service-set", Hidden: true, OwnerType: "NodePool"},
		{FieldPath: "spec.nodePool.immutableField", WriteMode: "immutable", Hidden: true, OwnerType: "NodePool"},
	}
	registryJSON, err := json.Marshal(registry)
	if err != nil {
		t.Fatalf("marshal registry: %v", err)
	}
	tempDir := t.TempDir()
	registryPath := filepath.Join(tempDir, "field_metadata.json")
	outputPath := filepath.Join(tempDir, "pathbind-draft.yaml")
	if err := os.WriteFile(registryPath, registryJSON, 0o600); err != nil {
		t.Fatalf("write registry: %v", err)
	}

	if err := runInit(registryPath, "", outputPath); err != nil {
		t.Fatalf("runInit(): %v", err)
	}
	output, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	text := string(output)
	if !strings.Contains(text, "spec.nodePool.nodeLabels") {
		t.Errorf("mutable hidden field missing from pathbind draft:\n%s", text)
	}
	for _, excluded := range []string{"serviceField", "immutableField"} {
		if strings.Contains(text, excluded) {
			t.Errorf("non-public field %q unexpectedly appeared in pathbind draft:\n%s", excluded, text)
		}
	}
}

func TestPathCoveredByExistingPath(t *testing.T) {
	covered := map[string]bool{
		"spec.hostedCluster.configuration.ingress.componentRoutes": true,
	}
	tests := []struct {
		name    string
		path    string
		covered map[string]bool
		want    bool
	}{
		{
			name: "parent array binding covers item fields",
			path: "spec.hostedCluster.configuration.ingress.componentRoutes.hostname",
			want: true,
		},
		{
			name: "exact path is covered",
			path: "spec.hostedCluster.configuration.ingress.componentRoutes",
			want: true,
		},
		{
			name: "prefix without a path separator is not covered",
			path: "spec.hostedCluster.configuration.ingress.componentRoutesExtra.hostname",
		},
		{
			name: "unrelated path is not covered",
			path: "spec.hostedCluster.configuration.scheduler.profile",
		},
		{
			name: "child path does not cover its parent",
			path: "spec.hostedCluster.configuration.ingress.componentRoutes",
			covered: map[string]bool{
				"spec.hostedCluster.configuration.ingress.componentRoutes.hostname": true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := tt.covered
			if existing == nil {
				existing = covered
			}
			if got := pathCoveredByExistingPath(tt.path, existing); got != tt.want {
				t.Errorf("pathCoveredByExistingPath(%q) = %t, want %t", tt.path, got, tt.want)
			}
		})
	}
}
