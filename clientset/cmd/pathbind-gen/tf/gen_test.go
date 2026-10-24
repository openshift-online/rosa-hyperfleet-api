package tf

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"

	pkg "github.com/openshift-online/rosa-seaplane-api/clientset/cmd/pathbind-gen/pkg"
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
      - path: metadata.name
        goType: string
        operations: [create]
      - path: metadata.uid
        goType: string
        operations: [read]
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
      - path: spec.tags
        goType: map
        operations: [create, update]
      - path: spec.resourceTags
        goType: string
        operations: [create, update]
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
      - path: spec.tags
        type: map
        description: "Customer AWS tags update in place; reserved system tags use part of the AWS tag budget."
      - path: spec.resourceTags
        type: string
        flag: ""
      - alias: no_cni
        type: bool
        optional: true
        computed: true
        immutable: true
        operations: [create]
      - alias: enabled
        type: bool
        bundle: autoscaling
        operations: [create, update]
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
	if !strings.Contains(string(state), `tfsdk:"network"`) {
		t.Fatalf("generated Terraform state did not contain network object:\n%s", state)
	}
	if strings.Contains(string(state), `tfsdk:"resource_tags"`) {
		t.Fatalf("an alias with an empty flag should not be added to Terraform state:\n%s", state)
	}
	if strings.Contains(string(native), `hfsdk:"spec.resourceTags"`) {
		t.Fatalf("an alias with an empty flag should not be pathbound from Terraform:\n%s", native)
	}
	resource, err := os.ReadFile(filepath.Join(output, "cluster_resource_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	resourceText := string(resource)
	for _, want := range []string{
		`"network": schema.SingleNestedAttribute`,
		`"autoscaling": schema.SingleNestedAttribute`,
		`"network_type": schema.StringAttribute`,
		`"enabled": schema.BoolAttribute`,
		`MarkdownDescription: "Customer AWS tags update in place; reserved system tags use part of the AWS tag budget."`,
		`"no_cni": schema.BoolAttribute`,
		`Clusters().Get(ctx, state.Name.ValueString(), hfwrappers.GetOptions{})`,
		`Clusters().Get(ctx, plan.Name.ValueString(), hfwrappers.GetOptions{})`,
		`Clusters().Delete(ctx, state.Name.ValueString(), hfwrappers.DeleteOptions{})`,
		`objectToNative(tf.Network`,
		`nativeBundleAttributes(native.Network`,
	} {
		if !strings.Contains(resourceText, want) {
			t.Errorf("generated resource missing %q", want)
		}
	}
	noCNIPattern := regexp.MustCompile(`"no_cni": schema\.BoolAttribute\{[^}]*Optional:\s+true,[^}]*Computed:\s+true,[^}]*PlanModifiers:\s+\[\]planmodifier\.Bool\{\s*boolplanmodifier\.UseStateForUnknown\(\),\s*boolplanmodifier\.RequiresReplace\(\),`)
	if !noCNIPattern.MatchString(resourceText) {
		t.Errorf("optional-computed immutable no_cni should retain its input and resolve state before replacement checks:\n%s", resourceText)
	}
	if strings.Contains(resourceText, `"resource_tags": schema.`) {
		t.Errorf("an alias with an empty flag should not appear in the Terraform schema:\n%s", resourceText)
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

func TestRunRoutesByMetadataNameWhenFieldAliasesCollide(t *testing.T) {
	dir := t.TempDir()
	draftPath := filepath.Join(dir, "draft.yaml")
	overridesPath := filepath.Join(dir, "overrides.yaml")
	outputDir := filepath.Join(dir, "generated")
	draft := `resources:
  cluster:
    sdkType: v1alpha1.Cluster
    fields:
      - path: metadata.name
        goType: string
        operations: [create]
      - path: metadata.uid
        goType: string
        operations: [read]
      - path: spec.configuration.name
        goType: string
        operations: [create, update]
`
	overrides := `config:
  package: generated
  tfProviderPkg: example/provider
resources:
  cluster:
    aliases:
      - path: metadata.name
        required: true
        immutable: true
      - path: metadata.uid
        alias: id
        goName: ID
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

	state, err := os.ReadFile(filepath.Join(outputDir, "cluster_state_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	stateText := string(state)
	if !strings.Contains(stateText, "MetadataName") || !strings.Contains(stateText, `tfsdk:"metadata_name"`) ||
		!strings.Contains(stateText, "ID") || !strings.Contains(stateText, `tfsdk:"id"`) {
		t.Fatalf("generated state must retain both the route name and UID identity fields:\n%s", state)
	}

	resource, err := os.ReadFile(filepath.Join(outputDir, "cluster_resource_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	resourceText := string(resource)
	for _, want := range []string{
		"Get(ctx, state.MetadataName.ValueString(), hfwrappers.GetOptions{})",
		"Get(ctx, plan.MetadataName.ValueString(), hfwrappers.GetOptions{})",
		"Delete(ctx, state.MetadataName.ValueString(), hfwrappers.DeleteOptions{})",
	} {
		if !strings.Contains(resourceText, want) {
			t.Errorf("generated API route is missing metadata.name lookup %q", want)
		}
	}
	if strings.Contains(resourceText, "state.ID.ValueString()") || strings.Contains(resourceText, "plan.ID.ValueString()") {
		t.Error("generated API routes must not use the server UID as the path identifier")
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
      - path: metadata.name
        goType: string
        operations: [create]
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
		`SeaplaneV1alpha1().NodePools().Create`,
		`SeaplaneV1alpha1().NodePools().Get`,
		`SeaplaneV1alpha1().NodePools().Update`,
		`SeaplaneV1alpha1().NodePools().Delete`,
		`SeaplaneV1alpha1().NodePools().Get(ctx, req.ID`,
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

func TestRunGeneratesPartialUpdateResource(t *testing.T) {
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
      - path: metadata.name
        goType: string
        operations: [read]
      - path: spec.autoRepair
        goType: boolean
        operations: [create, update]
      - path: spec.serverComputed
        goType: string
        operations: [create, update]
      - path: spec.maxNodesTotal
        goType: integer(int32)
        operations: [create, update]
      - path: spec.createOnly
        goType: string
        operations: [create]
      - path: spec.labels
        goType: map
        operations: [create, update]
      - path: spec.nodePool.taints
        goType: array(object)
        operations: [create, update]
`
	overrides := `config:
  package: generated
  tfProviderPkg: example/provider
resources:
  nodePool:
    partialUpdate: true
    aliases:
      - path: metadata.uid
        alias: id
        type: string
        computed: true
      - path: metadata.name
        type: string
      - path: spec.autoRepair
        type: bool
      - path: spec.serverComputed
        type: string
        computed: true
      - path: spec.maxNodesTotal
        type: "*int32"
      - path: spec.createOnly
        type: string
      - path: spec.labels
        type: map
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

	resource, err := os.ReadFile(filepath.Join(outputDir, "nodepool_resource_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	resourceText := string(resource)
	for _, want := range []string{
		`"encoding/json"`,
		`partialUpdateDocument(`,
		`RESTClient().`,
		`Resource("nodepools")`,
		`Name(plan.Name.ValueString())`,
		`Body(body)`,
		`PostPartialExpand(ctx context.Context, input *NodePoolState, document map[string]any) diag.Diagnostics`,
		`r.Handler.PostPartialExpand(ctx, &plan, updateDocument)`,
		`var NodePoolPartialUpdatePaths = map[string]bool{`,
		`"spec.autoRepair":`,
		`"spec.maxNodesTotal":`,
		`var NodePoolClearablePartialUpdatePaths = map[string]bool{`,
		`"spec.maxNodesTotal":`,
		`r.Client.SeaplaneV1alpha1().RESTClient()`,
		`pathbind.Flatten(ctx, updated, &nativeState)`,
		`nativeNodePoolToTerraform(&nativeState)`,
		`mergeResponseNodePool(&plan, responseState)`,
		`mergeResponseNodePool(&plan, &priorState)`,
	} {
		if !strings.Contains(resourceText, want) {
			t.Errorf("generated partial-update resource missing %q:\n%s", want, resourceText)
		}
	}
	utils, err := os.ReadFile(filepath.Join(outputDir, "utils_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"func partialUpdateDocument",
		"func appendPartialUpdatePath",
		"func setPartialUpdateValue",
		`setPartialUpdateValue(document, path, nil)`,
		"field %s cannot be removed because the API does not support clearing it",
	} {
		if !strings.Contains(string(utils), want) {
			t.Errorf("generated partial-update utilities missing %q:\n%s", want, utils)
		}
	}
	partialPaths := resourceText[strings.Index(resourceText, "var NodePoolPartialUpdatePaths"):strings.Index(resourceText, "var NodePoolClearablePartialUpdatePaths")]
	if strings.Contains(partialPaths, `"spec.createOnly":`) {
		t.Errorf("generated partial-update paths must exclude create-only fields:\n%s", partialPaths)
	}
	if strings.Contains(partialPaths, `"spec.serverComputed":`) {
		t.Errorf("generated partial-update paths must exclude computed fields:\n%s", partialPaths)
	}
}

func TestPartialUpdatePathPoliciesRejectsNonSpecPaths(t *testing.T) {
	_, _, err := partialUpdatePathPolicies([]pkg.MergedAlias{{Path: "metadata.labels"}}, true)
	if err == nil || !strings.Contains(err.Error(), "partialUpdate supports only spec.* paths, got metadata.labels") {
		t.Fatalf("partialUpdatePathPolicies() error = %v, want non-spec path rejection", err)
	}
}

func TestRunGeneratesPartialUpdateResourceWithBundle(t *testing.T) {
	dir := t.TempDir()
	draftPath := filepath.Join(dir, "draft.yaml")
	overridesPath := filepath.Join(dir, "overrides.yaml")
	outputDir := filepath.Join(dir, "generated")
	draft := `resources:
  nodePool:
    sdkType: v1alpha1.NodePool
    fields:
      - path: metadata.name
        goType: string
        operations: [create]
      - path: metadata.uid
        goType: string
        operations: [read]
      - path: spec.nodePool.autoScaling.min
        goType: integer(int32)
        operations: [create, update]
      - path: spec.nodePool.autoScaling.max
        goType: integer(int32)
        operations: [create, update]
`
	overrides := `config:
  package: generated
  tfProviderPkg: example/provider
resources:
  nodePool:
    partialUpdate: true
    aliases:
      - path: metadata.uid
        alias: id
        type: string
        computed: true
      - path: metadata.name
        type: string
      - path: spec.nodePool.autoScaling.min
        alias: min_replicas
        bundle: autoscaling
        type: "*int32"
      - path: spec.nodePool.autoScaling.max
        alias: max_replicas
        bundle: autoscaling
        type: "*int32"
      - alias: enabled
        bundle: autoscaling
        type: bool
        operations: [create, update]
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

	resource, err := os.ReadFile(filepath.Join(outputDir, "nodepool_resource_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	resourceText := string(resource)
	for _, want := range []string{
		`var NodePoolAutoscalingPartialUpdateAttributes = map[string]string{`,
		`"Min_replicas": "min_replicas"`,
		`"Max_replicas": "max_replicas"`,
		`appendPartialUpdateBundle(`,
		`reflect.TypeOf(NodePoolAutoscalingNative{})`,
		`"spec.nodePool.autoScaling.min":`,
		`"spec.nodePool.autoScaling.max":`,
	} {
		if !strings.Contains(resourceText, want) {
			t.Errorf("generated bundled partial-update resource missing %q:\n%s", want, resourceText)
		}
	}
	partialPaths := resourceText[strings.Index(resourceText, "var NodePoolPartialUpdatePaths"):strings.Index(resourceText, "var NodePoolAutoscalingPartialUpdateAttributes")]
	if strings.Contains(partialPaths, `"":`) {
		t.Errorf("generated partial-update paths must exclude consumer-only bundle fields:\n%s", partialPaths)
	}

	utils, err := os.ReadFile(filepath.Join(outputDir, "utils_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(utils), "func appendPartialUpdateBundle") {
		t.Errorf("generated partial-update utilities missing bundle support:\n%s", utils)
	}

	compileGeneratedPartialUpdateUtils(t, outputDir)
}

func compileGeneratedPartialUpdateUtils(t *testing.T, outputDir string) {
	t.Helper()

	compileDir := t.TempDir()
	utils, err := os.ReadFile(filepath.Join(outputDir, "utils_gen.go"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(compileDir, "utils_gen.go"), utils, 0o600); err != nil {
		t.Fatal(err)
	}
	generatedTest := `package generated

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAppendPartialUpdateBundleHandlesNullObjects(t *testing.T) {
	bundleType := reflect.StructOf([]reflect.StructField{{
		Name: "Min",
		Type: reflect.TypeOf((*int32)(nil)),
		Tag:  reflect.StructTag("hfsdk:\"spec.nodePool.autoScaling.min\""),
	}})
	paths := map[string]bool{"spec.nodePool.autoScaling.min": true}
	clearablePaths := map[string]bool{"spec.nodePool.autoScaling.min": true}

	cases := []struct {
		name     string
		planNull bool
		stateNull bool
		wantSet  bool
		wantValue any
	}{
		{name: "both null", planNull: true, stateNull: true},
		{name: "plan set", wantSet: true, wantValue: int64(2)},
		{name: "plan null", planNull: true, wantSet: true},
		{name: "state null", stateNull: true, wantSet: true, wantValue: int64(2)},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			planObject := objectValue(t, testCase.planNull, 2)
			stateObject := objectValue(t, testCase.stateNull, 1)
			document := map[string]any{}
			var diagnostics diag.Diagnostics
			appendPartialUpdateBundle(
				context.Background(),
				planObject,
				stateObject,
				bundleType,
				map[string]string{"Min": "min_replicas"},
				paths,
				clearablePaths,
				document,
				&diagnostics,
			)
			if diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %+v", diagnostics)
			}

			value, ok := document["min"]
			if testCase.wantSet {
				current, exists := document["spec"].(map[string]any)
				if exists {
					current, exists = current["nodePool"].(map[string]any)
				}
				if exists {
					current, exists = current["autoScaling"].(map[string]any)
				}
				if exists {
					value, exists = current["min"]
				}
				ok = exists
			}
			if ok != testCase.wantSet || value != testCase.wantValue {
				t.Fatalf("document value = (%#v, %v), want (%#v, %v)", value, ok, testCase.wantValue, testCase.wantSet)
			}
		})
	}
}

func TestAppendPartialUpdateBundleSkipsUnknownWithoutUpdatableField(t *testing.T) {
	attributeTypes := map[string]attr.Type{"labels": types.MapType{ElemType: types.StringType}}
	bundleType := reflect.StructOf([]reflect.StructField{{
		Name: "Labels",
		Type: reflect.TypeOf(map[string]string(nil)),
		Tag:  reflect.StructTag("hfsdk:\"metadata.labels\""),
	}})
	document := map[string]any{}
	var diagnostics diag.Diagnostics
	appendPartialUpdateBundle(
		context.Background(),
		types.ObjectUnknown(attributeTypes),
		types.ObjectNull(attributeTypes),
		bundleType,
		map[string]string{"Labels": "labels"},
		map[string]bool{"spec.nodePool.autoScaling.min": true},
		nil,
		document,
		&diagnostics,
	)
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %+v", diagnostics)
	}
	if len(document) != 0 {
		t.Fatalf("unexpected document: %#v", document)
	}
}

func objectValue(t *testing.T, null bool, value int64) types.Object {
	t.Helper()
	if null {
		return types.ObjectNull(map[string]attr.Type{"min_replicas": types.Int64Type})
	}
	return types.ObjectValueMust(
		map[string]attr.Type{"min_replicas": types.Int64Type},
		map[string]attr.Value{"min_replicas": types.Int64Value(value)},
	)
}
`
	if err := os.WriteFile(filepath.Join(compileDir, "utils_gen_test.go"), []byte(generatedTest), 0o600); err != nil {
		t.Fatal(err)
	}
	goMod := `module generated

go 1.24

require github.com/hashicorp/terraform-plugin-framework v1.19.0
`
	if err := os.WriteFile(filepath.Join(compileDir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}

	goCommand := exec.Command("go", "test", "-mod=mod", "./...")
	goCommand.Dir = compileDir
	goCommand.Env = goTestEnvironment(t, compileDir)
	if output, err := goCommand.CombinedOutput(); err != nil {
		t.Fatalf("go test generated utils: %v\n%s", err, output)
	}
}

func goTestEnvironment(t *testing.T, cacheDir string) []string {
	t.Helper()

	environment := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GOROOT=") || strings.HasPrefix(entry, "GOWORK=") {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment,
		"GOCACHE="+filepath.Join(cacheDir, "gocache"),
		"GOWORK=off",
	)
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
      - path: metadata.name
        goType: string
        operations: [create]
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
			name:      "missing metadata name",
			draft:     "resources:\n  cluster:\n    sdkType: v1alpha1.Cluster\n    fields:\n      - path: metadata.uid\n        goType: string\n        operations: [read]\n",
			overrides: "config:\n  package: generated\n  tfProviderPkg: example/provider\n",
			want:      "metadata.name field is missing",
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
		{
			name:      "partial update JSON encoded field",
			draft:     validDraft,
			overrides: "config:\n  package: generated\n  tfProviderPkg: example/provider\nresources:\n  cluster:\n    partialUpdate: true\n    aliases:\n      - path: spec.displayName\n        json_encoded: true\n",
			want:      "partialUpdate does not support json_encoded field spec.displayName",
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
	if _, _, err := buildBundles([]pkg.MergedAlias{{Path: "metadata.name", Bundle: "identity"}}); err == nil {
		t.Fatal("expected metadata.name bundle error")
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
	if got := functions["planModifiers"].(func(pkg.MergedAlias) string)(stringAlias); got != "stringplanmodifier.UseStateForUnknown(), stringplanmodifier.RequiresReplace()" {
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
		{pkg.MergedAlias{Type: "bool", Immutable: true, Computed: true}, "PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown(), boolplanmodifier.RequiresReplace()},"},
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
