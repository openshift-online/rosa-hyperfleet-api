package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"

	pkg "github.com/openshift-online/rosa-hyperfleet-api/clientset/cmd/pathbind-gen/pkg"
)

func TestRunGeneratesBundledTerraformObject(t *testing.T) {
	dir := t.TempDir()
	draft := filepath.Join(dir, "draft.yaml")
	overrides := filepath.Join(dir, "overrides.yaml")
	output := filepath.Join(dir, "generated")
	if err := os.WriteFile(draft, []byte(`resources:
  cluster:
    sdkType: v1alpha1.Cluster
    fields:
      - path: metadata.uid
        goType: string
        operations: [create]
      - path: spec.hostedCluster.networking.networkType
        goType: string
        operations: [create]
      - path: spec.hostedCluster.networking.apiServer.advertiseAddress
        goType: string
        operations: [create]
      - path: spec.hostedCluster.platform.aws.multiArch
        goType: boolean
        operations: [create]
      - path: spec.hostedCluster.networking.apiServer.port
        goType: integer(int32)
        operations: [create]
      - path: spec.hostedCluster.networking.apiServer.allowedCIDRBlocks
        goType: array(string)
        operations: [create]
      - path: spec.properties
        goType: map
        operations: [create]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overrides, []byte(`config:
  package: generated
  tfProviderPkg: example/provider
resources:
  cluster:
    aliases:
      - path: spec.hostedCluster.networking.networkType
        bundle: network
        sensitive: true
      - path: spec.hostedCluster.networking.apiServer.advertiseAddress
        bundle: network
        immutable: true
      - path: spec.hostedCluster.platform.aws.multiArch
        bundle: network
        immutable: true
      - path: spec.hostedCluster.networking.apiServer.port
        bundle: network
        immutable: true
      - path: spec.hostedCluster.networking.apiServer.allowedCIDRBlocks
        bundle: network
        type: string[]
        immutable: true
      - path: spec.properties
        bundle: network
        type: map
        immutable: true
`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Run(draft, overrides, output); err != nil {
		t.Fatal(err)
	}
	native, err := os.ReadFile(filepath.Join(output, "cluster_state_native_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(native), "ClusterNetworkNative") {
		t.Fatalf("generated native state did not contain bundle:\n%s", native)
	}
	state, err := os.ReadFile(filepath.Join(output, "cluster_state_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `Network types.Object `+"`tfsdk:\"network\"`") {
		t.Fatalf("generated Terraform state did not contain network object:\n%s", state)
	}
	resource, err := os.ReadFile(filepath.Join(output, "cluster_resource_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	resourceText := string(resource)
	for _, want := range []string{
		`"network": schema.SingleNestedAttribute`,
		`"network_type": schema.StringAttribute`,
		`objectToNative(tf.Network`,
		`nativeBundleAttributes(native.Network`,
	} {
		if !strings.Contains(resourceText, want) {
			t.Errorf("generated resource missing %q", want)
		}
	}
	if !regexp.MustCompile(`"network_type": schema\.StringAttribute\{[^}]*Sensitive:\s+true,`).MatchString(resourceText) {
		t.Errorf("sensitive bundled field network_type should generate Sensitive: true:\n%s", resourceText)
	}
	if regexp.MustCompile(`"advertise_address": schema\.StringAttribute\{[^}]*Sensitive:`).MatchString(resourceText) {
		t.Errorf("non-sensitive bundled field advertise_address must not generate Sensitive:\n%s", resourceText)
	}
	modifierCases := []struct {
		field           string
		typeName        string
		modifierPackage string
	}{
		{"advertise_address", "String", "stringplanmodifier"},
		{"multi_arch", "Bool", "boolplanmodifier"},
		{"port", "Int64", "int64planmodifier"},
		{"allowed_cidr_blocks", "List", "listplanmodifier"},
		{"properties", "Map", "mapplanmodifier"},
	}
	for _, tc := range modifierCases {
		pattern := `"` + tc.field + `": schema\.[A-Za-z0-9]+Attribute\{[^}]*PlanModifiers:\s+\[\]planmodifier\.` + tc.typeName + `\{[^}]*` + tc.modifierPackage + `\.RequiresReplace\(\)`
		if !regexp.MustCompile(pattern).MatchString(resourceText) {
			t.Errorf("immutable bundle member %q is missing the %s RequiresReplace modifier", tc.field, tc.typeName)
		}
		if !strings.Contains(resourceText, `"github.com/hashicorp/terraform-plugin-framework/resource/schema/`+tc.modifierPackage+`"`) {
			t.Errorf("generated imports missing modifier package %q", tc.modifierPackage)
		}
	}
	utils, err := os.ReadFile(filepath.Join(output, "utils_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(utils), "merged, diags := types.ObjectValue(typesByName, attributes)") ||
		!strings.Contains(string(utils), "if diags.HasError() {\n\t\treturn plan") {
		t.Fatalf("generated object merge helper must preserve plan when object reconstruction has diagnostics:\n%s", utils)
	}
	for _, want := range []string{
		"integerConversionOverflows(source, targetType)",
		"target.OverflowInt(source.Int())",
		"maxInt := (uint64(1) << uint(target.Bits()-1)) - 1",
		"target.OverflowUint(source.Uint())",
	} {
		if !strings.Contains(string(utils), want) {
			t.Errorf("generated bundle conversion is missing integer overflow check %q", want)
		}
	}
	for _, want := range []string{
		"field.Type().Elem().Kind() != reflect.String",
		"field.Type().Key().Kind() != reflect.String",
		"items[i] = field.Index(i).String()",
		"values[iter.Key().String()] = iter.Value().String()",
		"return types.ListNull(types.StringType)",
		"return types.MapNull(types.StringType)",
	} {
		if !strings.Contains(string(utils), want) {
			t.Errorf("generated native collection conversion is missing %q", want)
		}
	}
}

func TestRunGeneratesListObjectAttributeAndConversions(t *testing.T) {
	dir := t.TempDir()
	draftPath := filepath.Join(dir, "draft.yaml")
	overridesPath := filepath.Join(dir, "overrides.yaml")
	outputDir := filepath.Join(dir, "generated")
	draft := `resources:
  nodePool:
    sdkType: v1alpha1.NodePool
    fields:
      - path: metadata.uid
        goType: string
        operations: [read]
      - path: spec.nodePool.config
        goType: array(object)
        operations: [create, update]
      - path: spec.nodePool.taints
        goType: array(object)
        operations: [create, update]
      - path: spec.nodePool.policy
        goType: array(object)
        operations: [create, update]
`
	overrides := `config:
  package: generated
  tfProviderPkg: example/provider
resources:
  nodePool:
    aliases:
      - path: metadata.uid
        alias: id
        type: string
        computed: true
      - path: spec.nodePool.config
        type: list(object)
        element:
          attributes:
            name:
              type: string
              required: true
      - path: spec.nodePool.taints
        type: list(object)
        element:
          attributes:
            key:
              type: string
              required: true
            value:
              type: string
              optional: true
            effect:
              type: string
              required: true
      - path: spec.nodePool.policy
        type: list(object)
        element:
          attributes:
            enabled:
              type: bool
              immutable: true
            generation:
              type: int32
              computed: true
`
	if err := os.WriteFile(draftPath, []byte(draft), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridesPath, []byte(overrides), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(draftPath, overridesPath, outputDir); err != nil {
		t.Fatal(err)
	}

	state, err := os.ReadFile(filepath.Join(outputDir, "nodepool_state_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(state), `Config types.List`) || !strings.Contains(string(state), `tfsdk:"config"`) {
		t.Errorf("generated state should use types.List for config:\n%s", state)
	}
	native, err := os.ReadFile(filepath.Join(outputDir, "nodepool_state_native_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(native), `Config string `+"`hfsdk:\"spec.nodePool.config\"`") {
		t.Errorf("native pathbind state should retain JSON string representation:\n%s", native)
	}
	resource, err := os.ReadFile(filepath.Join(outputDir, "nodepool_resource_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	resourceText := string(resource)
	for _, want := range []string{
		`HyperfleetV1alpha1().NodePools().Create`,
		`HyperfleetV1alpha1().NodePools().Get`,
		`HyperfleetV1alpha1().NodePools().Update`,
		`HyperfleetV1alpha1().NodePools().Delete`,
		`HyperfleetV1alpha1().NodePools().Get(ctx, req.ID`,
	} {
		if !strings.Contains(resourceText, want) {
			t.Errorf("generated NodePool resource missing unnamespaced client call %q", want)
		}
	}
	for _, stale := range []string{"Handler.Namespace", "NodePools(namespace)", "expected namespace/id"} {
		if strings.Contains(resourceText, stale) {
			t.Errorf("generated NodePool resource still contains namespace plumbing %q", stale)
		}
	}
	for _, want := range []string{
		`"config": schema.ListNestedAttribute`,
		`"taints": schema.ListNestedAttribute`,
		`"policy": schema.ListNestedAttribute`,
		`NestedAttributeObject{`,
		`"effect": schema.StringAttribute`,
		`"enabled": schema.BoolAttribute`,
		`boolplanmodifier.RequiresReplace()`,
		`"generation": schema.Int64Attribute`,
		`int64planmodifier.UseStateForUnknown()`,
		`"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"`,
		`"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"`,
		`terraformObjectListToJSON(tf.Config)`,
		`terraformJSONToObjectList(native.Config`,
	} {
		if !strings.Contains(resourceText, want) {
			t.Errorf("generated list(object) resource missing %q", want)
		}
	}
	utils, err := os.ReadFile(filepath.Join(outputDir, "utils_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"func terraformObjectListToJSON", "func terraformJSONToObjectList"} {
		if !strings.Contains(string(utils), want) {
			t.Errorf("generated list(object) conversion helper missing %q", want)
		}
	}
}

func TestBuildBundlesRejectsBundledMetadataUID(t *testing.T) {
	_, _, err := buildBundles([]pkg.MergedAlias{{
		Path:   "metadata.uid",
		Bundle: "identity",
	}})
	if err == nil {
		t.Fatal("expected metadata.uid bundle error")
	}
	if !strings.Contains(err.Error(), "metadata.uid cannot be placed in bundle") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildBundlesCardinality(t *testing.T) {
	aliases := []pkg.MergedAlias{
		{Path: "required", Bundle: "aws", Required: true},
		{Path: "computed", Bundle: "aws", Computed: true},
		{Path: "optional", Bundle: "network"},
		{Path: "standalone"},
	}
	top, bundles, err := buildBundles(aliases)
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || top[0].Path != "standalone" || len(bundles) != 2 {
		t.Fatalf("unexpected bundle grouping: top=%#v bundles=%#v", top, bundles)
	}
	if bundles[0].GoName != "Aws" || !bundles[0].Required || !bundles[0].Computed || !bundles[0].Optional {
		t.Errorf("unexpected required/computed bundle metadata: %#v", bundles[0])
	}
	if bundles[1].GoName != "Network" || bundles[1].Required || bundles[1].Computed || !bundles[1].Optional {
		t.Errorf("unexpected optional bundle metadata: %#v", bundles[1])
	}
}

func TestPlanModifierFieldSupportsAllAttributeTypes(t *testing.T) {
	cases := []struct {
		typeName string
		want     string
	}{
		{"string", "PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},"},
		{"bool", "PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},"},
		{"int32", "PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},"},
		{"int64", "PlanModifiers: []planmodifier.Int64{int64planmodifier.RequiresReplace()},"},
		{"string[]", "PlanModifiers: []planmodifier.List{listplanmodifier.RequiresReplace()},"},
		{"map", "PlanModifiers: []planmodifier.Map{mapplanmodifier.RequiresReplace()},"},
	}
	for _, tc := range cases {
		t.Run(tc.typeName, func(t *testing.T) {
			got := planModifierField(pkg.MergedAlias{Type: tc.typeName, Immutable: true})
			if got != tc.want {
				t.Errorf("planModifierField(%q) = %q, want %q", tc.typeName, got, tc.want)
			}
		})
	}
}

func TestTerraformTypeFor(t *testing.T) {
	for _, typ := range []string{"string", "*string", "bool", "*bool", "int32", "*int32", "int64", "*int64", "string[]", "map"} {
		if _, err := terraformTypeFor(typ); err != nil {
			t.Errorf("terraformTypeFor(%q): %v", typ, err)
		}
	}
	if _, err := terraformTypeFor("object"); err == nil {
		t.Fatal("expected unsupported Terraform type error")
	}
}

func TestLoadOverridesErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadOverrides(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Fatal("expected error for missing override file")
	}
	malformed := filepath.Join(dir, "malformed.yaml")
	if err := os.WriteFile(malformed, []byte("config: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOverrides(malformed); err == nil {
		t.Fatal("expected parse error for malformed overrides")
	}
}

func TestEmitFileErrorPaths(t *testing.T) {
	t.Run("template execution", func(t *testing.T) {
		tmpl, err := template.New("execute-error").Parse(`{{.Missing.Value}}`)
		if err != nil {
			t.Fatal(err)
		}
		if err := emitFile(tmpl, pkg.TFTemplateData{}, filepath.Join(t.TempDir(), "out.go")); err == nil || !strings.Contains(err.Error(), "rendering") {
			t.Fatalf("expected render error, got %v", err)
		}
	})

	t.Run("formatting", func(t *testing.T) {
		dir := t.TempDir()
		tmpl := template.Must(template.New("format-error").Parse("not valid Go source"))
		path := filepath.Join(dir, "out.go")
		if err := emitFile(tmpl, pkg.TFTemplateData{}, path); err == nil || !strings.Contains(err.Error(), "formatting") {
			t.Fatalf("expected format error, got %v", err)
		}
		if _, err := os.Stat(path + ".debug"); err != nil {
			t.Fatalf("expected unformatted debug output: %v", err)
		}
	})

	t.Run("write", func(t *testing.T) {
		dir := t.TempDir()
		tmpl := template.Must(template.New("write-error").Parse("package generated"))
		if err := emitFile(tmpl, pkg.TFTemplateData{}, dir); err == nil || !strings.Contains(err.Error(), "writing") {
			t.Fatalf("expected write error, got %v", err)
		}
	})
}

func TestRunValidationErrors(t *testing.T) {
	validDraft := `resources:
  cluster:
    sdkType: v1alpha1.Cluster
    fields:
      - path: metadata.uid
        goType: string
        operations: [read]
      - path: spec.displayName
        goType: string
        operations: [create, update]
`

	tests := []struct {
		name      string
		draft     string
		overrides string
		want      string
	}{
		{
			name:      "missing provider configuration",
			draft:     validDraft,
			overrides: "config:\n  package: generated\n",
			want:      "must set package and tfProviderPkg",
		},
		{
			name:      "missing metadata uid",
			draft:     "resources:\n  cluster:\n    sdkType: v1alpha1.Cluster\n    fields:\n      - path: metadata.name\n        goType: string\n        operations: [create]\n",
			overrides: "config:\n  package: generated\n  tfProviderPkg: example/provider\n",
			want:      "metadata.uid field is missing",
		},
		{
			name:      "unsupported draft leaf type",
			draft:     strings.Replace(validDraft, "goType: string\n        operations: [create, update]", "goType: array(object)\n        operations: [create, update]", 1),
			overrides: "config:\n  package: generated\n  tfProviderPkg: example/provider\n",
			want:      "unsupported draft type",
		},
		{
			name:      "unsupported Terraform type override",
			draft:     validDraft,
			overrides: "config:\n  package: generated\n  tfProviderPkg: example/provider\nresources:\n  cluster:\n    aliases:\n      - path: spec.displayName\n        type: object\n",
			want:      "unsupported Terraform consumer type",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			draftPath := filepath.Join(dir, "draft.yaml")
			overridesPath := filepath.Join(dir, "overrides.yaml")
			if err := os.WriteFile(draftPath, []byte(tc.draft), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(overridesPath, []byte(tc.overrides), 0o600); err != nil {
				t.Fatal(err)
			}
			err := Run(draftPath, overridesPath, filepath.Join(dir, "generated"))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Run error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestBuildBundlesRejectsUIDAndPreservesBundleCardinality(t *testing.T) {
	if _, _, err := buildBundles([]pkg.MergedAlias{{Path: "metadata.uid", Bundle: "identity"}}); err == nil {
		t.Fatal("expected metadata.uid bundle error")
	}
	top, bundles, err := buildBundles([]pkg.MergedAlias{
		{Path: "metadata.name"},
		{Path: "spec.required", Bundle: "aws", Required: true},
		{Path: "spec.computed", Bundle: "aws", Computed: true},
		{Path: "spec.optional", Bundle: "network"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(top) != 1 || len(bundles) != 2 || !bundles[0].Required || !bundles[0].Computed || !bundles[0].Optional || !bundles[1].Optional {
		t.Fatalf("unexpected grouping/cardinality top=%#v bundles=%#v", top, bundles)
	}
}

func TestBuildFuncMapHelpers(t *testing.T) {
	functions := buildFuncMap()
	stringAlias := pkg.MergedAlias{GoName: "myField", Type: "string", Immutable: true, Computed: true}
	for name, input := range map[string]pkg.MergedAlias{"tfType": stringAlias, "tfTypeValue": stringAlias, "schemaType": stringAlias} {
		function := functions[name].(func(pkg.MergedAlias) (string, error))
		if _, err := function(input); err != nil {
			t.Errorf("%s returned error: %v", name, err)
		}
		if _, err := function(pkg.MergedAlias{Type: "unsupported"}); err == nil {
			t.Errorf("%s should reject unsupported type", name)
		}
	}
	if got := functions["attrName"].(func(pkg.MergedAlias) string)(stringAlias); got != "my_field" {
		t.Errorf("attrName = %q", got)
	}
	if got := functions["planModifiers"].(func(pkg.MergedAlias) string)(stringAlias); got != "stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()" {
		t.Errorf("planModifiers = %q", got)
	}
	if got := functions["planModifiers"].(func(pkg.MergedAlias) string)(pkg.MergedAlias{}); got != "" {
		t.Errorf("planModifiers for mutable field = %q, want empty", got)
	}
	if got := functions["isConsumerOnly"].(func(pkg.MergedAlias) bool)(pkg.MergedAlias{Path: "-"}); !got {
		t.Error("expected unmapped field to be consumer-only")
	}
	if got := functions["isConsumerOnly"].(func(pkg.MergedAlias) bool)(pkg.MergedAlias{Path: "-", Operations: []string{"create"}}); got {
		t.Error("operation-scoped unmapped field should not be hidden")
	}
	if got := functions["hfsdkTag"].(func(pkg.MergedAlias) string)(pkg.MergedAlias{Path: "-"}); got != "-" {
		t.Errorf("hfsdkTag = %q", got)
	}
	if got := functions["hfsdkTag"].(func(pkg.MergedAlias) string)(pkg.MergedAlias{Path: "spec.displayName"}); got != "spec.displayName" {
		t.Errorf("hfsdkTag mapped path = %q", got)
	}
	if got := functions["pluralize"].(func(string) string)("Clusters"); got != "Clusterses" {
		t.Errorf("pluralize = %q", got)
	}
	if got := functions["pluralize"].(func(string) string)("NodePool"); got != "NodePools" {
		t.Errorf("pluralize non-s suffix = %q", got)
	}
	if got := functions["sdkPackage"].(func(string) string)("v1alpha1.Cluster"); got != "v1alpha1" {
		t.Errorf("sdkPackage = %q", got)
	}
	if got := functions["sdkPackage"].(func(string) string)("v1alpha1"); got != "v1alpha1" {
		t.Errorf("sdkPackage without type suffix = %q", got)
	}
	contains := functions["contains"].(func([]string, string) bool)
	if !contains([]string{"create", "update"}, "update") || contains([]string{"create"}, "delete") {
		t.Error("contains returned incorrect membership")
	}
	for _, tc := range []struct {
		alias pkg.MergedAlias
		want  string
	}{
		{pkg.MergedAlias{Type: "bool", Immutable: true}, "PlanModifiers: []planmodifier.Bool{boolplanmodifier.RequiresReplace()},"},
		{pkg.MergedAlias{Type: "int32", Computed: true}, "PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},"},
		{pkg.MergedAlias{Type: "unsupported", Immutable: true}, ""},
		{pkg.MergedAlias{Type: "string"}, ""},
	} {
		if got := planModifierField(tc.alias); got != tc.want {
			t.Errorf("planModifierField(%#v) = %q, want %q", tc.alias, got, tc.want)
		}
	}
}

func TestRunRejectsOutputDirectoryCreationFailure(t *testing.T) {
	dir := t.TempDir()
	draft := filepath.Join(dir, "draft.yaml")
	overrides := filepath.Join(dir, "overrides.yaml")
	if err := os.WriteFile(draft, []byte(`resources:
  cluster:
    sdkType: v1alpha1.Cluster
    fields:
      - path: metadata.uid
        goType: string
        operations: [read]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overrides, []byte(`config:
  package: generated
  tfProviderPkg: example/provider
`), 0o600); err != nil {
		t.Fatal(err)
	}
	parentFile := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Run(draft, overrides, filepath.Join(parentFile, "generated")); err == nil || !strings.Contains(err.Error(), "creating output dir") {
		t.Fatalf("expected output directory error, got %v", err)
	}
}
