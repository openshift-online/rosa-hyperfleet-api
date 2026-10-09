package passthrough

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
)

func TestPassthrough_UpdateActionMetadataRoundTrip(t *testing.T) {
	registry, err := markers.LoadTypedRegistryFromJSONBytes([]byte(`[{"ownerType":"Cluster","ownerGVK":"hyperfleet.io/v1alpha1.Cluster","fieldPath":"spec.hostedCluster.release","writeMode":"service-set","hidden":true,"featureGate":"HyperFleetKubeletAdvanced","updateAction":"UpdateClusterVersion","featureGateAwareWriteModes":[{"featureGate":"HyperFleetKubeletAdvanced","writeMode":"mutable"},{"featureGate":"","writeMode":"service-set"}]}]`))
	if err != nil {
		t.Fatal(err)
	}
	sourceDir := t.TempDir()
	source := "package upstream\ntype HostedClusterSpec struct {\nRelease string `json:\"release\"`\n}\n"
	if err := os.WriteFile(filepath.Join(sourceDir, "types.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	gen := NewGenerator(sourceDir, []string{"HostedClusterSpec"}, registry)
	gen.OutputPackage = "v1alpha1"
	if err := gen.LoadSourceFiles(sourceDir); err != nil {
		t.Fatal(err)
	}
	output := t.TempDir()
	if err := gen.Generate(output); err != nil {
		t.Fatal(err)
	}
	generated, err := os.ReadFile(filepath.Join(output, "zz_generated.passthrough.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(generated), "+hyperfleet:update-action=UpdateClusterVersion") {
		t.Fatal("passthrough dropped action marker")
	}
	scanner, err := markers.NewScanner([]string{output}, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := scanner.Scan(); err != nil {
		t.Fatal(err)
	}
	if got, want := scanner.TypedRegistry["Cluster"]["spec.hostedCluster.release"], registry["Cluster"]["spec.hostedCluster.release"]; !reflect.DeepEqual(got, want) {
		t.Fatalf("passthrough rescan lost metadata: got %+v, want %+v", got, want)
	}
	if err := gen.Generate(output); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(output, "zz_generated.passthrough.go"))
	if err != nil {
		t.Fatal(err)
	}
	if string(generated) != string(second) {
		t.Fatal("second passthrough generation differs")
	}
}
