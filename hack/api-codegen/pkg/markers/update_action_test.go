package markers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUpdateAction_ScanGenerateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	source := `package sample
 // +hyperfleet:upstream-reduced-object=upstream.Config
 type Config struct {
 // +hyperfleet:write-mode=mutable
 // +hyperfleet:update-action=UpdateClusterConfig
 // +openshift:enable:FeatureGate=HyperFleetKubeletAdvanced
 Count *int64 ` + "`json:\"count,omitempty\"`" + `
 }
 type Cluster struct {
 Spec struct {
 // +k8s:openapi-gen=false
 // +hyperfleet:write-mode=service-set
 // +hyperfleet:update-action=UpdateClusterConfig
 Configuration *upstream.Config ` + "`json:\"configuration,omitempty\"`" + `
 // +hyperfleet:update-action=UpdateClusterVersion
 Release string ` + "`json:\"release\"`" + `
 } ` + "`json:\"spec\"`" + `
 }
 `
	if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	scanner, err := NewScanner([]string{dir}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := scanner.Scan(); err != nil {
		t.Fatal(err)
	}
	if got := scanner.TypedRegistry["Cluster"]["spec.release"].UpdateAction; got != "UpdateClusterVersion" {
		t.Fatalf("marker-only field lost: %q", got)
	}
	if err := scanner.TypedRegistry.Validate(); err != nil {
		t.Fatalf("action-only field changed effective writability: %v", err)
	}
	goFile := filepath.Join(t.TempDir(), "registry.go")
	if err := scanner.Generate(goFile); err != nil {
		t.Fatal(err)
	}
	jsonFile := goFile + ".json"
	if err := scanner.GenerateJSON(jsonFile); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadTypedRegistryFromJSON(jsonFile)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, scanner.TypedRegistry) {
		t.Fatal("JSON metadata roundtrip differs")
	}
	for _, path := range []string{"spec.release", "spec.configuration", "spec.configuration.count"} {
		if loaded["Cluster"][path].UpdateAction == "" {
			t.Errorf("missing direct/container/synthetic action: %s", path)
		}
	}
	if meta := loaded["Cluster"]["spec.configuration"]; !meta.Hidden || meta.WriteMode != ServiceSet || !meta.IsReducedContainer {
		t.Fatalf("container restrictions lost: %+v", meta)
	}
	if loaded["Cluster"]["spec.configuration.count"].IsReducedContainer {
		t.Fatal("container provenance leaked to a scalar descendant")
	}
	data, err := os.ReadFile(goFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `IsReducedContainer: true`) || !strings.Contains(string(data), `UpdateAction: "UpdateClusterConfig"`) {
		t.Fatal("Go output lost action")
	}
	if err := scanner.Generate(goFile); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(goFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(again) {
		t.Fatal("second generation differs")
	}
}

func TestUpdateAction_RejectsConflictingAndInvalidSources(t *testing.T) {
	for _, test := range []struct{ name, markers string }{
		{"same field conflicting actions", "// +hyperfleet:update-action=UpdateCluster\n// +hyperfleet:update-action=UpdateClusterVersion"},
		{"unknown action", "// +hyperfleet:update-action=ImaginaryAction"},
		{"wrong resource action", "// +hyperfleet:update-action=ScaleNodePool"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			source := "package sample\ntype Cluster struct {\nSpec struct {\n" + test.markers + "\nName string `json:\"name\"`\n} `json:\"spec\"`\n}\n"
			if err := os.WriteFile(filepath.Join(dir, "types.go"), []byte(source), 0600); err != nil {
				t.Fatal(err)
			}
			scanner, err := NewScanner([]string{dir}, false)
			if err != nil {
				t.Fatal(err)
			}
			err = scanner.Scan()
			if err == nil {
				err = scanner.TypedRegistry.Validate()
			}
			if err == nil {
				t.Fatal("expected conflicting/unknown/incompatible source declaration rejection")
			}
		})
	}
}

func TestUpdateAction_OverlayPreservesRestrictionsAndRejectsMistakes(t *testing.T) {
	base := FieldMeta{FieldPath: "spec.release", OwnerType: "Cluster", WriteMode: ServiceSet, Hidden: true, FeatureGate: "Gate", FeatureGateAwareWriteModes: []FeatureGateWriteMode{{FeatureGate: "Gate", WriteMode: Mutable}}}
	for _, test := range []struct {
		name           string
		declarations   []UpdateActionDeclaration
		originalAction string
		fail           bool
	}{
		{name: "valid", declarations: []UpdateActionDeclaration{{Kind: "Cluster", FieldPath: "spec.release", UpdateAction: "UpdateClusterVersion"}}},
		{name: "identical generated copy", originalAction: "UpdateClusterVersion", declarations: []UpdateActionDeclaration{{Kind: "Cluster", FieldPath: "spec.release", UpdateAction: "UpdateClusterVersion"}}},
		{name: "unknown kind", fail: true, declarations: []UpdateActionDeclaration{{Kind: "MadeUp", FieldPath: "spec.release", UpdateAction: "UpdateClusterVersion"}}},
		{name: "unknown path", fail: true, declarations: []UpdateActionDeclaration{{Kind: "Cluster", FieldPath: "spec.madeUp", UpdateAction: "UpdateClusterVersion"}}},
		{name: "unknown action", fail: true, declarations: []UpdateActionDeclaration{{Kind: "Cluster", FieldPath: "spec.release", UpdateAction: "MadeUp"}}},
		{name: "wrong resource action", fail: true, declarations: []UpdateActionDeclaration{{Kind: "Cluster", FieldPath: "spec.release", UpdateAction: "ScaleNodePool"}}},
		{name: "conflicting source", fail: true, originalAction: "UpdateCluster", declarations: []UpdateActionDeclaration{{Kind: "Cluster", FieldPath: "spec.release", UpdateAction: "UpdateClusterVersion"}}},
		{name: "duplicate overlay", fail: true, declarations: []UpdateActionDeclaration{{Kind: "Cluster", FieldPath: "spec.release", UpdateAction: "UpdateClusterVersion"}, {Kind: "Cluster", FieldPath: "spec.release", UpdateAction: "UpdateClusterVersion"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			scanner, err := NewScanner(nil, false)
			if err != nil {
				t.Fatal(err)
			}
			original := base
			original.UpdateAction = test.originalAction
			scanner.TypedRegistry["Cluster"] = map[string]FieldMeta{"spec.release": original}
			scanner.UpdateActions = test.declarations
			err = scanner.Generate(filepath.Join(t.TempDir(), "registry.go"))
			if (err != nil) != test.fail {
				t.Fatalf("error = %v, want failure=%v", err, test.fail)
			}
			if !test.fail {
				got := scanner.TypedRegistry["Cluster"]["spec.release"]
				if got.UpdateAction != "UpdateClusterVersion" {
					t.Fatalf("missing overlay action: %+v", got)
				}
				got.UpdateAction = original.UpdateAction
				if !reflect.DeepEqual(got, original) {
					t.Fatalf("overlay changed write/gate/visibility metadata: %+v", got)
				}
			}
		})
	}
}

func TestUpdateAction_JSONLoadPreservesMetadata(t *testing.T) {
	loaded, err := LoadTypedRegistryFromJSONBytes([]byte(`[{"ownerType":"Cluster","fieldPath":"spec.release","writeMode":"mutable","hidden":true,"featureGate":"Gate","updateAction":"UpdateClusterVersion","featureGateAwareWriteModes":[{"featureGate":"Gate","writeMode":"mutable"},{"featureGate":"","writeMode":"service-set"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	meta := loaded["Cluster"]["spec.release"]
	if meta.UpdateAction != "UpdateClusterVersion" {
		t.Fatal("JSON load dropped updateAction")
	}
	encoded, err := json.Marshal(meta)
	if err != nil || !strings.Contains(string(encoded), `"updateAction":"UpdateClusterVersion"`) {
		t.Fatalf("lowercase updateAction wire key lost: %s (%v)", encoded, err)
	}
	if !meta.Hidden || meta.FeatureGate != "Gate" || len(meta.FeatureGateAwareWriteModes) != 2 {
		t.Fatalf("metadata lost: %+v", meta)
	}
}
