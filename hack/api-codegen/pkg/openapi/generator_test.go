package openapi

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestFilterHiddenFieldsRetainsMutableNodePoolLabelsAndTaints(t *testing.T) {
	definitions := map[string]apiextensionsv1.JSONSchemaProps{
		"NodePoolSpecPassthrough": {
			Type: "object",
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"nodeLabels":              {Type: "object"},
				"taints":                  {Type: "array"},
				"nodeVolumeDetachTimeout": {Type: "string"},
			},
		},
	}

	if err := filterHiddenFields(definitions); err != nil {
		t.Fatalf("filterHiddenFields(): %v", err)
	}
	properties := definitions["NodePoolSpecPassthrough"].Properties
	for _, visible := range []string{"nodeLabels", "taints"} {
		if _, ok := properties[visible]; !ok {
			t.Errorf("mutable field %q was filtered from the public schema", visible)
		}
	}
	if _, ok := properties["nodeVolumeDetachTimeout"]; ok {
		t.Error("service-set field nodeVolumeDetachTimeout was not filtered from the public schema")
	}
}

func TestRetainsReducedContainers(t *testing.T) {
	definitions := map[string]apiextensionsv1.JSONSchemaProps{
		"HostedClusterSpecPassthrough": {
			Type: "object",
			Properties: map[string]apiextensionsv1.JSONSchemaProps{
				"configuration": {Type: "object"},
				"dns":           {Type: "object"},
				"networking":    {Type: "object"},
				"platform":      {Type: "object"},
			},
			Required: []string{"networking", "platform"},
		},
	}
	if err := filterHiddenFields(definitions); err != nil {
		t.Fatal(err)
	}
	schema := definitions["HostedClusterSpecPassthrough"]
	for _, name := range []string{"configuration", "dns", "networking", "platform"} {
		if _, ok := schema.Properties[name]; !ok {
			t.Errorf("reduced container %q missing from public schema", name)
		}
	}
	if !reflect.DeepEqual(schema.Required, []string{"networking", "platform"}) {
		t.Errorf("required = %v, want [networking platform]", schema.Required)
	}
}

func TestConfigurationUsesLocalType(t *testing.T) {
	output := generateConfigurationSchema(t)

	t.Run("cluster configuration", func(t *testing.T) { assertClusterConfiguration(t, output) })
	t.Run("ingress configuration", func(t *testing.T) { assertIngressConfiguration(t, output) })
	t.Run("scheduler configuration", func(t *testing.T) { assertSchedulerConfiguration(t, output) })
	t.Run("proxy configuration", func(t *testing.T) { assertProxyConfiguration(t, output) })
	t.Run("kubelet configuration", func(t *testing.T) { assertKubeletConfiguration(t, output) })
	t.Run("cluster autoscaling", func(t *testing.T) { assertClusterAutoscaling(t, output) })
}

func generateConfigurationSchema(t *testing.T) schemaOutput {
	t.Helper()
	tmpFile := t.TempDir() + "/openapi.json"

	// Resolve the v1alpha1 package relative to this test file's location
	// (hack/api-codegen/pkg/openapi/) → ../../../../api/v1alpha1
	v2alpha1Dir := "../../../../api/v1alpha1"
	if _, err := os.Stat(v2alpha1Dir); err != nil {
		t.Skipf("v1alpha1 source not available: %v", err)
	}

	gen := NewGenerator([]string{v2alpha1Dir}, tmpFile)
	gen.Title = "Test"
	gen.Version = "v1"

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Read output: %v", err)
	}

	var output schemaOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return output
}

func assertClusterConfiguration(t *testing.T, output schemaOutput) {
	t.Helper()
	// ClusterConfiguration must exist as its own definition (from the local type)
	cc, ok := output.Definitions["ClusterConfiguration"]
	if !ok {
		t.Fatal("ClusterConfiguration definition not found")
	}

	// The local type's markers expose ingress, proxy, scheduler, kubelet, and machineConfig.
	// If the upstream hypershiftv1beta1.ClusterConfiguration were used instead,
	// all 10 sub-config fields would be present (no hidden markers).
	for _, visible := range []string{"ingress", "proxy", "scheduler", "kubelet", "machineConfig"} {
		if _, found := cc.Properties[visible]; !found {
			t.Errorf("expected visible property %q in ClusterConfiguration", visible)
		}
	}
	for _, hidden := range []string{"apiServer", "authentication", "featureGate", "image", "network", "oauth"} {
		if _, found := cc.Properties[hidden]; found {
			t.Errorf("property %q should be hidden in ClusterConfiguration (local markers not applied?)", hidden)
		}
	}
}

func assertIngressConfiguration(t *testing.T, output schemaOutput) {
	t.Helper()
	// IngressConfiguration exposes only customer-configurable component routes.
	ic, ok := output.Definitions["IngressConfiguration"]
	if !ok {
		t.Fatal("IngressConfiguration definition not found")
	}
	componentRoutes, found := ic.Properties["componentRoutes"]
	if !found {
		t.Error("componentRoutes property not found in IngressConfiguration")
	} else {
		if componentRoutes.MaxItems == nil || *componentRoutes.MaxItems != 2 {
			t.Errorf("componentRoutes maxItems = %v, want 2", componentRoutes.MaxItems)
		}
		if componentRoutes.XListType == nil || *componentRoutes.XListType != "map" {
			t.Errorf("componentRoutes list type = %v, want map", componentRoutes.XListType)
		}
		if got, want := componentRoutes.XListMapKeys, []string{"namespace", "name"}; !reflect.DeepEqual(got, want) {
			t.Errorf("componentRoutes list map keys = %v, want %v", got, want)
		}
	}
	crc, ok := output.Definitions["ComponentRouteConfiguration"]
	if !ok {
		t.Fatal("ComponentRouteConfiguration definition not found")
	}
	for _, visible := range []string{"namespace", "name", "hostname", "servingCertKeyPairSecret"} {
		if _, found := crc.Properties[visible]; !found {
			t.Errorf("expected visible property %q in ComponentRouteConfiguration", visible)
		}
	}
	if _, found := crc.Properties["labels"]; found {
		t.Error("labels should not be exposed in ComponentRouteConfiguration")
	}
	for field, allowedValues := range map[string]map[string]bool{
		"namespace": {`"openshift-console"`: true},
		"name": {
			`"console"`:   true,
			`"downloads"`: true,
		},
	} {
		property := crc.Properties[field]
		for _, value := range property.Enum {
			delete(allowedValues, string(value.Raw))
		}
		if len(allowedValues) != 0 {
			t.Errorf("%s enum is missing values: %v", field, allowedValues)
		}
	}
}

func assertSchedulerConfiguration(t *testing.T, output schemaOutput) {
	t.Helper()
	// SchedulerConfiguration exposes only the supported scheduler profile choice.
	sc, ok := output.Definitions["SchedulerConfiguration"]
	if !ok {
		t.Fatal("SchedulerConfiguration definition not found")
	}
	profile, ok := sc.Properties["profile"]
	if !ok {
		t.Fatal("profile property not found in SchedulerConfiguration")
	}
	wantProfiles := map[string]bool{
		`"LowNodeUtilization"`:  true,
		`"HighNodeUtilization"`: true,
		`"NoScoring"`:           true,
	}
	for _, value := range profile.Enum {
		delete(wantProfiles, string(value.Raw))
	}
	if len(wantProfiles) != 0 {
		t.Errorf("profile enum is missing values: %v", wantProfiles)
	}
}

func assertProxyConfiguration(t *testing.T, output schemaOutput) {
	t.Helper()
	// ProxyConfiguration exposes user-settable proxy fields but keeps the
	// service-managed fields out of the generated schema.
	pc, ok := output.Definitions["ProxyConfiguration"]
	if !ok {
		t.Fatal("ProxyConfiguration definition not found")
	}
	for _, visible := range []string{"httpProxy", "httpsProxy", "noProxy"} {
		if _, found := pc.Properties[visible]; !found {
			t.Errorf("expected visible property %q in ProxyConfiguration", visible)
		}
	}
	for _, hidden := range []string{"trustedCA", "readinessEndpoints"} {
		if _, found := pc.Properties[hidden]; found {
			t.Errorf("property %q should be hidden in ProxyConfiguration", hidden)
		}
	}
}

func assertKubeletConfiguration(t *testing.T, output schemaOutput) {
	t.Helper()
	// KubeletConfig must retain its visible fields (nested path test)
	kc, ok := output.Definitions["KubeletConfig"]
	if !ok {
		t.Fatal("KubeletConfig definition not found")
	}
	for _, visible := range []string{"podPidsLimit", "maxPods", "containerLogMaxFiles"} {
		if _, found := kc.Properties[visible]; !found {
			t.Errorf("expected visible property %q in KubeletConfig", visible)
		}
	}
	for _, hidden := range []string{"evictionHard", "cpuManagerPolicy", "topologyManagerPolicy"} {
		if _, found := kc.Properties[hidden]; found {
			t.Errorf("property %q should be hidden in KubeletConfig", hidden)
		}
	}
}

func assertClusterAutoscaling(t *testing.T, output schemaOutput) {
	t.Helper()
	// Cluster autoscaling is exposed from the HostedCluster passthrough.
	hc, ok := output.Definitions["HostedClusterSpecPassthrough"]
	if !ok {
		t.Fatal("HostedClusterSpecPassthrough definition not found")
	}
	if _, found := hc.Properties["autoscaling"]; !found {
		t.Error("autoscaling property not found in HostedClusterSpecPassthrough")
	}
}

func TestGenerateMinimal(t *testing.T) {
	tmpFile := "/tmp/openapi-test.json"
	defer func() { _ = os.Remove(tmpFile) }()

	gen := NewGenerator(nil, tmpFile)
	gen.Title = "Test API"
	gen.Version = "v1"

	if err := gen.Generate(); err != nil {
		t.Fatalf("Generate failed: %v", err)
	}

	if _, err := os.Stat(tmpFile); err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("Failed to read output: %v", err)
	}

	var output schemaOutput
	if err := json.Unmarshal(data, &output); err != nil {
		t.Fatalf("Output is not valid JSON: %v", err)
	}

	if output.OpenAPI != "3.0.0" {
		t.Errorf("Expected OpenAPI 3.0.0, got %s", output.OpenAPI)
	}
	if output.Info.Title != "Test API" {
		t.Errorf("Expected title 'Test API', got %s", output.Info.Title)
	}
	if output.Info.Version != "v1" {
		t.Errorf("Expected version 'v1', got %s", output.Info.Version)
	}
	if len(output.Definitions) != 0 {
		t.Errorf("Expected 0 definitions in minimal mode, got %d", len(output.Definitions))
	}
}
