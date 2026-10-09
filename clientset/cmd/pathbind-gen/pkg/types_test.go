package pkg

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBuildMergedAliasesPreservesBundle(t *testing.T) {
	draft := map[string]DraftField{
		"spec.hostedCluster.networking.machineNetwork": {Path: "spec.hostedCluster.networking.machineNetwork", GoType: "string", Operations: []string{"create"}},
	}
	aliases, err := BuildMergedAliases(draft, []OverrideAlias{{
		Path:   "spec.hostedCluster.networking.machineNetwork",
		Bundle: "network",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 || aliases[0].Bundle != "network" {
		t.Fatalf("bundle was not preserved: %#v", aliases)
	}
}

func TestBuildMergedAliasesListObjectElementSchema(t *testing.T) {
	path := "spec.nodePool.config"
	draft := map[string]DraftField{
		path: {Path: path, GoType: "array(object)", Operations: []string{"create", "update"}},
	}
	_, err := BuildMergedAliases(draft, []OverrideAlias{{Path: path, Type: "list(object)"}})
	if err == nil || !strings.Contains(err.Error(), "requires element.attributes") {
		t.Fatalf("expected missing list object element schema error, got %v", err)
	}
	aliases, err := BuildMergedAliases(draft, []OverrideAlias{{
		Path: path,
		Type: "list(object)",
		Element: &OverrideObjectElement{Attributes: map[string]OverrideObjectAttribute{
			"name": {Type: "string", Required: boolPtr(true)},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 1 || len(aliases[0].ElementAttributes) != 1 || aliases[0].ElementAttributes[0].Name != "name" || !aliases[0].ElementAttributes[0].Required {
		t.Fatalf("unexpected list object field merge: %#v", aliases)
	}
	_, err = BuildMergedAliases(draft, []OverrideAlias{{
		Path: path,
		Type: "list(object)",
		Element: &OverrideObjectElement{Attributes: map[string]OverrideObjectAttribute{
			"metadata": {Type: "map"},
		}},
	}})
	if err == nil || !strings.Contains(err.Error(), "unsupported object element type") {
		t.Fatalf("expected unsupported object element type error, got %v", err)
	}
}

func TestLoadDraft(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "draft.yaml")
	if err := os.WriteFile(path, []byte(`resources:
  cluster:
    sdkType: v1alpha1.Cluster
    fields:
      - path: metadata.uid
        goType: string
        operations: [read]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	index := map[string]map[string]DraftField{}
	types := map[string]string{}
	if err := LoadDraft(path, index, types); err != nil {
		t.Fatal(err)
	}
	if types["cluster"] != "v1alpha1.Cluster" || index["cluster"]["metadata.uid"].GoType != "string" {
		t.Fatalf("unexpected draft indexes: types=%v fields=%v", types, index)
	}
	if err := LoadDraft("", index, types); err != nil {
		t.Fatalf("empty path should be a no-op: %v", err)
	}
	if err := LoadDraft(filepath.Join(dir, "missing.yaml"), index, types); err == nil {
		t.Fatal("expected missing draft error")
	}
	if err := os.WriteFile(path, []byte("resources: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadDraft(path, index, types); err == nil {
		t.Fatal("expected malformed draft error")
	}
}

func TestBuildMergedAliasesValidationAndConsumerOnly(t *testing.T) {
	draft := map[string]DraftField{
		"field.string": {Path: "field.string", GoType: "string", Operations: []string{"create", "update"}},
	}
	if _, err := BuildMergedAliases(map[string]DraftField{"field.array": {Path: "field.array", GoType: "array(object)"}}, nil); err == nil {
		t.Fatal("expected unsupported draft type error")
	}
	if _, err := BuildMergedAliases(map[string]DraftField{"field.number": {Path: "field.number", GoType: "number"}}, nil); err == nil {
		t.Fatal("expected missing consumer type error")
	}
	aliases, err := BuildMergedAliases(draft, []OverrideAlias{
		{Path: "field.string", Alias: "friendlyName", Immutable: boolPtr(true), Computed: boolPtr(true), Sensitive: boolPtr(true), JSONEncoded: boolPtr(true), Operations: []string{"create"}},
		{Alias: "localOnly", Operations: []string{"create"}, Type: "string", Flag: stringPtr("local-only")},
		{Alias: "notAnInput", Type: "string"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(aliases) != 2 {
		t.Fatalf("got %d aliases, want mapped and consumer-only entries", len(aliases))
	}
	mapped := aliases[0]
	if mapped.GoName != "FriendlyName" || mapped.Type != "string" || mapped.Path != "field.string" ||
		!mapped.Immutable || !mapped.Computed || !mapped.Sensitive || !mapped.JSONEncoded ||
		!reflect.DeepEqual(mapped.Operations, []string{"create"}) {
		t.Fatalf("unexpected mapped alias: %#v", mapped)
	}
	localOnly := aliases[1]
	if localOnly.GoName != "LocalOnly" || localOnly.Path != "-" || localOnly.Flag != "local-only" {
		t.Fatalf("unexpected consumer-only alias: %#v", localOnly)
	}
}

func TestCategorizeAndCollectUnsetPtrFields(t *testing.T) {
	aliases := []MergedAlias{
		{GoName: "Required", Type: "string", HasFlag: true, Required: true, Operations: []string{"create"}},
		{GoName: "PtrCount", Type: "*int32", HasFlag: true, Operations: []string{"create", "update"}},
		{GoName: "PtrBool", Type: "*bool", HasFlag: true, Operations: []string{"update"}},
		{GoName: "PtrString", Type: "*string", HasFlag: true, Operations: []string{"create"}},
		{GoName: "NoFlag", Type: "*int64", Operations: []string{"create"}},
	}
	create, createFlags, requiredCreate, update, updateFlags, requiredUpdate := CategorizeAliases(aliases)
	if len(create) != 4 || len(createFlags) != 3 || len(requiredCreate) != 1 || len(update) != 2 || len(updateFlags) != 2 || len(requiredUpdate) != 0 {
		t.Fatalf("unexpected categorized lengths create=%d/%d/%d update=%d/%d/%d", len(create), len(createFlags), len(requiredCreate), len(update), len(updateFlags), len(requiredUpdate))
	}
	unset := CollectUnsetPtrFields(aliases)
	if len(unset) != 2 || unset[0].GoName != "PtrCount" || unset[1].GoName != "PtrBool" {
		t.Fatalf("unexpected unset pointer fields: %#v", unset)
	}
}

func TestNameAndTypeHelpers(t *testing.T) {
	if got := ToSnake("AWSRolesRefIngressARN"); got != "aws_roles_ref_ingress_arn" {
		t.Errorf("ToSnake = %q", got)
	}
	if ToPascal("") != "" || TitleCase("oidcConfig") != "OidcConfig" {
		t.Fatal("unexpected Pascal/title conversion")
	}
	if SDKShortType("v1alpha1.Cluster") != "Cluster" || PkgAlias("a/b/c") != "c" {
		t.Fatal("unexpected SDK short type or package alias")
	}
	if got := UniqueAlias("spec.aws.region", []string{"spec.aws.region", "spec.azure.region"}); got != "awsRegion" {
		t.Fatalf("UniqueAlias = %q, want awsRegion", got)
	}
	if got := goTypeToConsumer("integer(int64)"); got != "*int64" {
		t.Errorf("goTypeToConsumer = %q", got)
	}
	if got := goTypeToConsumer("unsupported"); got != "" {
		t.Errorf("unsupported Go type mapped to %q", got)
	}
	if !IsSupportedConsumerType("map") || IsSupportedConsumerType("struct") {
		t.Fatal("unexpected consumer type support")
	}
	if !reflect.DeepEqual(narrowOperations([]string{"create", "update"}, []string{"update", "delete"}), []string{"update"}) {
		t.Fatal("operation narrowing did not filter unsupported operation")
	}
	if SDKShortType("Cluster") != "Cluster" || PkgAlias("single") != "single" {
		t.Fatal("no-separator helper inputs should be returned unchanged")
	}
	for goType, want := range map[string]string{
		"string":         "string",
		"boolean":        "*bool",
		"integer(int32)": "*int32",
		"integer(int64)": "*int64",
		"array(string)":  "string[]",
		"array":          "string[]",
		"map":            "map",
		"number":         "",
	} {
		if got := goTypeToConsumer(goType); got != want {
			t.Errorf("goTypeToConsumer(%q) = %q, want %q", goType, got, want)
		}
	}
	for _, tc := range []struct{ input, want string }{
		{"issuerURL", "issuer-url"},
		{"AWSPlatform", "aws-platform"},
		{"allocateNodeCIDRs", "allocate-node-cidrs"},
		{"allowedCIDRBlocks", "allowed-cidr-blocks"},
	} {
		if got := ToKebab(tc.input); got != tc.want {
			t.Errorf("ToKebab(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
	if got := UniqueAlias("a.b.c", []string{"a.b.c", "x.b.c", "y.a.c"}); got != "aBC" {
		t.Errorf("UniqueAlias full-path fallback = %q", got)
	}
}

func TestSortedAndUnionKeys(t *testing.T) {
	if got := SortedKeys(map[string]int{"z": 1, "a": 2}); !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("SortedKeys = %v", got)
	}
	got := UnionKeys(map[string]string{"cluster": "Cluster"}, map[string]OverrideResource{"nodePool": {}})
	if !reflect.DeepEqual(got, []string{"cluster", "nodePool"}) {
		t.Fatalf("UnionKeys = %v", got)
	}
}

func boolPtr(v bool) *bool       { return &v }
func stringPtr(v string) *string { return &v }
