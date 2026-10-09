package conversion

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestContainerProjection(t *testing.T) {
	root := t.TempDir()
	gen := NewGenerator("v1alpha1", "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1", []string{"../../../../api/v1alpha1"}, filepath.Join(root, "conversion", "v1alpha1"))
	gen.RESTOutputDir = filepath.Join(root, "public")
	if err := gen.Generate(); err != nil {
		t.Fatal(err)
	}
	for file, names := range map[string][]string{
		"hostedclusterspecpassthrough_types.go": {"Platform", "DNS", "Networking", "Configuration"},
		"nodepoolspecpassthrough_types.go":      {"Platform"},
	} {
		fields := generatedFields(t, filepath.Join(gen.RESTOutputDir, file))
		for _, name := range names {
			if _, exists := fields[name]; !exists {
				t.Errorf("public %s lost container %s", file, name)
			}
		}
		if _, exists := fields["ClusterID"]; exists {
			t.Errorf("public %s exposed hidden scalar", file)
		}
	}
	fields := generatedFields(t, filepath.Join(root, "conversion", "types.go"))
	for _, name := range []string{"Configuration", "Dns", "Networking", "Platform", "Kubelet", "MachineConfig", "Proxy"} {
		if _, exists := fields[name]; exists {
			t.Errorf("enrichment added reduced container %s", name)
		}
	}
	if fields["DNS"] != "dns" || fields["ClusterID"] != "clusterID" {
		t.Fatalf("existing leaf enrichment lost: %v", fields)
	}
}

func generatedFields(t *testing.T, path string) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	fields := make(map[string]string)
	ast.Inspect(file, func(node ast.Node) bool {
		structure, ok := node.(*ast.StructType)
		if !ok {
			return true
		}
		seen := make(map[string]bool)
		for _, field := range structure.Fields.List {
			tag, err := strconv.Unquote(field.Tag.Value)
			if err != nil {
				t.Fatal(err)
			}
			name := strings.Split(reflect.StructTag(tag).Get("json"), ",")[0]
			if seen[name] {
				t.Errorf("duplicate JSON field %s in %s", name, path)
			}
			seen[name] = true
			for _, member := range field.Names {
				fields[member.Name] = name
			}
		}
		return false
	})
	return fields
}
