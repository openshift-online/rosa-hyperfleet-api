package validation

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/markers"
	"github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen/pkg/registry"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

type actionObject struct {
	Version string `json:"version,omitempty"`
	Count   int64  `json:"count,omitempty"`
	Locked  string `json:"locked,omitempty"`
	Managed string `json:"managed,omitempty"`
	Gated   string `json:"gated,omitempty"`
}
type actionSpec struct {
	Object      *actionObject     `json:"object,omitempty"`
	Other       *actionObject     `json:"other,omitempty"`
	Items       []actionObject    `json:"items,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	DisplayName string            `json:"displayName,omitempty"`
}

func actionValidator(t *testing.T) *FieldValidator {
	t.Helper()
	entries, err := markers.LoadTypedRegistryFromJSONBytes([]byte(`[
 {"ownerType":"Test","fieldPath":"spec.object","writeMode":"mutable","updateAction":"UpdateCluster"},
 {"ownerType":"Test","fieldPath":"spec.object.version","writeMode":"mutable","updateAction":"UpdateClusterVersion"},
 {"ownerType":"Test","fieldPath":"spec.object.count","writeMode":"mutable","updateAction":"ScaleNodePool"},
 {"ownerType":"Test","fieldPath":"spec.object.locked","writeMode":"immutable"},
 {"ownerType":"Test","fieldPath":"spec.object.managed","writeMode":"service-set"},
 {"ownerType":"Test","fieldPath":"spec.object.gated","writeMode":"mutable","featureGate":"HyperFleetAutoScaling"},
 {"ownerType":"Test","fieldPath":"spec.other","writeMode":"mutable","updateAction":"UpdateClusterConfig"},
 {"ownerType":"Test","fieldPath":"spec.items.version","writeMode":"mutable","updateAction":"UpdateNodePoolVersion"},
 {"ownerType":"Test","fieldPath":"spec.labels","writeMode":"mutable","updateAction":"UpdateClusterConfig"},
 {"ownerType":"Test","fieldPath":"spec.displayName","writeMode":"mutable","updateAction":"UpdateCluster"}
 ]`))
	if err != nil {
		t.Fatal(err)
	}
	return &FieldValidator{typedRegistry: entries, resourceType: "Test"}
}

func mergedActionSpec(t *testing.T, old actionSpec, raw string) actionSpec {
	t.Helper()
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatal(err)
	}
	var candidate actionSpec
	if err := json.Unmarshal(data, &candidate); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
		t.Fatal(err)
	}
	return candidate
}

func TestJSONTagFallback(t *testing.T) {
	// Runtime construction lets staticcheck keep rejecting malformed source declarations.
	target := reflect.StructOf([]reflect.StructField{{Name: "Value", Type: reflect.TypeOf(""), Tag: `json:"bad\\key"`}})
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.Value": {WriteMode: registry.Mutable, UpdateAction: "UpdateClusterConfig"},
	})
	old := reflect.New(target)
	old.Elem().Field(0).SetString("old")
	candidate := reflect.New(target)
	candidate.Elem().Set(old.Elem())
	raw := json.RawMessage(`{"Value":"new"}`)
	if err := json.Unmarshal(raw, candidate.Interface()); err != nil {
		t.Fatal(err)
	}
	actions, errs := v.AnalyzeUpdate(raw, old.Interface(), candidate.Interface(), featuregate.Default)
	if errs != nil {
		t.Fatal(errs)
	}
	if !reflect.DeepEqual(actions, []string{"UpdateClusterConfig"}) {
		t.Fatalf("invalid tag lost Go-name fallback protection: %v", actions)
	}
}

func TestUpdateChecksTrustBundle(t *testing.T) {
	v := NewFieldValidator("Cluster")
	old := v1alpha1.ClusterSpec{}
	for _, raw := range []string{
		`{"additionalTrustBundle":"not a certificate"}`,
		`{"ADDITIONALTRUSTBUNDLE":"not a certificate"}`,
	} {
		candidate := old.DeepCopy()
		if err := json.Unmarshal([]byte(raw), candidate); err != nil {
			t.Fatal(err)
		}
		_, errs := v.AnalyzeUpdate(json.RawMessage(raw), &old, candidate, featuregate.Default)
		if len(errs) != 1 || errs[0].Field != "spec.additionalTrustBundle" {
			t.Errorf("AnalyzeUpdate(%s) errors = %v, want invalid trust bundle", raw, errs)
		}
	}
}

func TestRequiredUpdateActions_SubmittedEffectiveChanges(t *testing.T) {
	v := actionValidator(t)
	old := actionSpec{Object: &actionObject{Version: "old", Count: 9007199254740992}, Other: &actionObject{Version: "config"}, Items: []actionObject{{Version: "old"}}, Labels: map[string]string{"Env": "prod", "env": "dev"}, DisplayName: "old"}
	tests := []struct {
		name, raw string
		want      []string
	}{
		{"omitted", `{}`, nil},
		{"echo", `{"object":{"version":"old"}}`, nil},
		{"empty partial object", `{"object":{}}`, nil},
		{"null nonpointer ignored", `{"object":{"count":null}}`, nil},
		{"unknown ignored", `{"unknown":{"version":"new"}}`, nil},
		{"case alias canonicalized", `{"OBJECT":{"VERSION":"new"}}`, []string{"UpdateCluster", "UpdateClusterVersion"}},
		{"zero reset", `{"object":{"count":0}}`, []string{"ScaleNodePool", "UpdateCluster"}},
		{"precise int64 change", `{"object":{"count":9007199254740993}}`, []string{"ScaleNodePool", "UpdateCluster"}},
		{"parent clear all descendants", `{"object":null}`, []string{"ScaleNodePool", "UpdateCluster", "UpdateClusterVersion"}},
		{"all categories", `{"object":{"version":"new","count":0},"other":null,"items":[]}`, []string{"ScaleNodePool", "UpdateCluster", "UpdateClusterConfig", "UpdateClusterVersion", "UpdateNodePoolVersion"}},
		{"array replacement", `{"items":[{"version":"new"}]}`, []string{"UpdateNodePoolVersion"}},
		{"array echo", `{"items":[{"version":"old"}]}`, nil},
		{"map partial merge", `{"labels":{"env":"dev"}}`, nil},
		{"map case distinct", `{"labels":{"ENV":"qa"}}`, []string{"UpdateClusterConfig"}},
		{"map clear", `{"labels":null}`, []string{"UpdateClusterConfig"}},
		{"ordinary base annotation", `{"displayName":"new"}`, []string{"UpdateCluster"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			candidate := mergedActionSpec(t, old, tt.raw)
			got, errs := v.AnalyzeUpdate(json.RawMessage(tt.raw), &old, &candidate, featuregate.Default)
			if errs != nil {
				t.Fatal(errs)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("actions = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRequiredUpdateActions_RejectsAmbiguousRawJSON(t *testing.T) {
	v := actionValidator(t)
	for _, raw := range []string{
		`{"object":{"version":"new"},"object":{}}`,
		`{"object":{"Version":"new","version":"old"}}`,
		`{"OBJECT":{},"object":{}}`,
		`{"ob\u006aect":{},"object":{}}`,
		`{"labels":{"env":"a","env":"b"}}`,
		`{"items":[{"version":"a","VERSION":"b"}]}`,
		`{"unknown":{"a":1,"a":2}}`,
		`[]`, `null`, `{"object":`, `{} {}`,
	} {
		t.Run(raw, func(t *testing.T) {
			old := actionSpec{}
			var candidate actionSpec
			if json.Valid([]byte(raw)) {
				_ = json.Unmarshal([]byte(raw), &candidate)
			}
			_, errs := v.AnalyzeUpdate(json.RawMessage(raw), &old, &candidate, featuregate.Default)
			if errs == nil {
				t.Fatal("expected raw ambiguity/shape error")
			}
		})
	}
	old := actionSpec{}
	raw := `{"labels":{"Env":"prod","env":"dev"}}`
	candidate := mergedActionSpec(t, old, raw)
	if _, errs := v.AnalyzeUpdate(json.RawMessage(raw), &old, &candidate, featuregate.Default); errs != nil {
		t.Fatalf("case-distinct dynamic map keys rejected: %v", errs)
	}
}

func TestValidateUpdateJSON_AffectedProtections(t *testing.T) {
	v := actionValidator(t)
	old := actionSpec{Object: &actionObject{Version: "old", Count: 9007199254740992, Locked: "locked", Managed: "managed", Gated: "on"}}
	for _, raw := range []string{`{"object":null}`, `{"object":{"locked":""}}`, `{"object":{"managed":""}}`, `{"object":{"gated":"on"}}`} {
		t.Run(raw, func(t *testing.T) {
			candidate := mergedActionSpec(t, old, raw)
			_, errs := v.AnalyzeUpdate(json.RawMessage(raw), &old, &candidate, featuregate.Default)
			if len(errs) == 0 {
				t.Fatal("expected write-mode/gate validation error")
			}
			if raw == `{"object":null}` {
				for _, field := range []string{"spec.object.locked", "spec.object.managed", "spec.object.gated"} {
					if !strings.Contains(errs.Error(), field) {
						t.Errorf("reset bypassed %s: %v", field, errs)
					}
				}
			}
		})
	}
	for _, raw := range []string{`{}`, `{"object":{}}`, `{"object":{"version":"old"}}`, `{"object":{"count":9007199254740993}}`} {
		candidate := mergedActionSpec(t, old, raw)
		if _, errs := v.AnalyzeUpdate(json.RawMessage(raw), &old, &candidate, featuregate.Default); errs != nil {
			t.Errorf("partial merge %s falsely rejected: %v", raw, errs)
		}
	}
	raw := `{"object":{"gated":"on"}}`
	candidate := mergedActionSpec(t, old, raw)
	if _, errs := v.AnalyzeUpdate(json.RawMessage(raw), &old, &candidate, featuregate.TechPreviewNoUpgrade); errs != nil {
		t.Fatalf("enabled gate rejected: %v", errs)
	}
}

func TestValidateUpdateJSON_CollectionDescendantGatedEcho(t *testing.T) {
	type spec struct {
		Items   []actionObject          `json:"items,omitempty"`
		Objects map[string]actionObject `json:"objects,omitempty"`
	}
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.items.gated":   {WriteMode: registry.Mutable, FeatureGate: "HyperFleetAutoScaling"},
		"spec.objects.gated": {WriteMode: registry.Mutable, FeatureGate: "HyperFleetAutoScaling"},
	})
	old := spec{Items: []actionObject{{Gated: "on"}}, Objects: map[string]actionObject{"Env": {Gated: "on"}}}
	for _, raw := range []string{`{"items":[{"gated":"on"}]}`, `{"objects":{"Env":{"gated":"on"}}}`} {
		data, err := json.Marshal(old)
		if err != nil {
			t.Fatal(err)
		}
		var candidate spec
		if err := json.Unmarshal(data, &candidate); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
			t.Fatal(err)
		}
		if _, errs := v.AnalyzeUpdate(json.RawMessage(raw), &old, &candidate, featuregate.Default); errs == nil {
			t.Fatalf("submitted collection gate echo bypassed: %s", raw)
		}
		if _, errs := v.AnalyzeUpdate(json.RawMessage(raw), &old, &candidate, featuregate.TechPreviewNoUpgrade); errs != nil {
			t.Fatalf("enabled collection gate rejected: %v", errs)
		}
	}
}

func TestValidateUpdateJSON_ExactWriteModesAndZeroEcho(t *testing.T) {
	type spec struct {
		Object *actionObject `json:"object,omitempty"`
		Count  int64         `json:"count,omitempty"`
	}
	v := newTestValidator(map[string]registry.FieldMeta{
		"spec.object": {WriteMode: registry.ServiceSet},
		"spec.count":  {WriteMode: registry.Immutable},
	})
	old := spec{Count: 9007199254740992}
	for _, raw := range []string{`{"object":{}}`, `{"count":9007199254740993}`} {
		candidate := old
		if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
			t.Fatal(err)
		}
		_, errs := v.AnalyzeUpdate(json.RawMessage(raw), &old, &candidate, featuregate.Default)
		if strings.Contains(raw, "object") && errs != nil {
			t.Fatalf("both unset service-set object rejected: %v", errs)
		}
		if strings.Contains(raw, "count") && len(errs) == 0 {
			t.Fatal("int64 precision loss bypassed immutable validation")
		}
	}
}

func TestRequiredUpdateActions_RealClusterAndNodePool(t *testing.T) {
	cluster := v1alpha1.Cluster{}
	cluster.Spec.DisplayName = "old"
	cluster.Spec.HostedCluster.Release = hypershiftv1beta1.Release{Image: "old"}
	cluster.Spec.ControlPlaneUpgradePolicy = &v1alpha1.ControlPlaneUpgradePolicySpec{ScheduleType: v1alpha1.ManualSchedule}
	v := NewFieldValidator("Cluster")
	for _, tt := range []struct {
		raw  string
		want []string
	}{
		{`{"hostedCluster":{"release":{"image":"old"}}}`, nil},
		{`{"hostedCluster":{"release":{"image":"new"}},"controlPlaneUpgradePolicy":null}`, []string{"UpdateClusterVersion"}},
		{`{"displayName":"new","hostedCluster":{"release":{"image":"new"}}}`, []string{"UpdateCluster", "UpdateClusterVersion"}},
		{`{"hostedCluster":{"configuration":{"proxy":{"httpProxy":"http://proxy"}}}}`, []string{"UpdateClusterConfig"}},
	} {
		candidate := cluster.DeepCopy()
		if err := json.Unmarshal([]byte(tt.raw), &candidate.Spec); err != nil {
			t.Fatal(err)
		}
		actions, errs := v.AnalyzeUpdate(json.RawMessage(tt.raw), &cluster.Spec, &candidate.Spec, featuregate.Default)
		if errs != nil {
			t.Fatalf("Cluster %s rejected: %v", tt.raw, errs)
		}
		if !reflect.DeepEqual(actions, tt.want) {
			t.Errorf("Cluster %s = %v, want %v", tt.raw, actions, tt.want)
		}
	}
	replicas := int32(3)
	minimum := int32(1)
	pool := v1alpha1.NodePool{Spec: v1alpha1.NodePoolSpec{NodePool: v1alpha1.NodePoolSpecPassthrough{Replicas: &replicas, Release: hypershiftv1beta1.Release{Image: "old"}, AutoScaling: &hypershiftv1beta1.NodePoolAutoScaling{Min: &minimum, Max: 5}}}}
	npValidator := NewFieldValidator("NodePool")
	for _, tt := range []struct {
		raw  string
		want []string
	}{
		{`{"nodePool":{}}`, nil},
		{`{"nodePool":{"replicas":3,"autoScaling":{"max":5}}}`, nil},
		{`{"nodePool":{"replicas":0,"autoScaling":null,"release":{"image":"new"}}}`, []string{"ScaleNodePool", "UpdateNodePoolVersion"}},
		{`{"nodePool":{"replicas":null}}`, []string{"ScaleNodePool"}},
	} {
		candidate := pool.DeepCopy()
		if err := json.Unmarshal([]byte(tt.raw), &candidate.Spec); err != nil {
			t.Fatal(err)
		}
		actions, errs := npValidator.AnalyzeUpdate(json.RawMessage(tt.raw), &pool.Spec, &candidate.Spec, featuregate.Default)
		if errs != nil {
			t.Fatalf("NodePool %s rejected: %v", tt.raw, errs)
		}
		if !reflect.DeepEqual(actions, tt.want) {
			t.Errorf("NodePool %s = %v, want %v", tt.raw, actions, tt.want)
		}
	}
	if *pool.Spec.NodePool.Replicas != 3 || pool.Spec.NodePool.AutoScaling.Max != 5 || cluster.Spec.ControlPlaneUpgradePolicy == nil {
		t.Fatal("analysis or candidate merge mutated original")
	}
}

func TestValidateUpdateJSON_RealConfigurationRestrictions(t *testing.T) {
	old := v1alpha1.Cluster{}
	initial := `{"hostedCluster":{"configuration":{"proxy":{"httpProxy":"http://old","httpsProxy":"http://old"}}}}`
	if err := json.Unmarshal([]byte(initial), &old.Spec); err != nil {
		t.Fatal(err)
	}
	v := NewFieldValidator("Cluster")
	for _, test := range []struct {
		raw    string
		want   []string
		denied bool
	}{
		{`{"hostedCluster":{"configuration":{"proxy":{"httpProxy":"http://old"}}}}`, nil, false},
		{`{"hostedCluster":{"configuration":{"proxy":{"httpProxy":"http://new"}}}}`, []string{"UpdateClusterConfig"}, false},
		{`{"hostedCluster":{"configuration":null}}`, []string{"UpdateClusterConfig"}, false},
		// These synthetic/public fields do not exist in the pinned persisted type.
		// Unknown JSON must not manufacture effective changes or actions.
		{`{"hostedCluster":{"configuration":{"kubelet":{"maxPods":100}}}}`, nil, false},
	} {
		candidate := old.DeepCopy()
		if err := json.Unmarshal([]byte(test.raw), &candidate.Spec); err != nil {
			t.Fatal(err)
		}
		actions, errs := v.AnalyzeUpdate(json.RawMessage(test.raw), &old.Spec, &candidate.Spec, featuregate.Default)
		if (errs != nil) != test.denied {
			t.Fatalf("configuration %s errs = %v, want denial %v", test.raw, errs, test.denied)
		}
		if !reflect.DeepEqual(actions, test.want) {
			t.Errorf("configuration %s actions = %v, want %v", test.raw, actions, test.want)
		}
	}
}

func TestReducedContainerUpdates(t *testing.T) {
	for _, tc := range []struct {
		kind, initial, raw, action string
		shouldDeny                 bool
	}{
		{"Cluster", `{"hostedCluster":{"platform":{"type":"AWS","aws":{"region":"us-east-1"}}}}`, `{"hostedCluster":{"platform":{"aws":{"region":"us-west-2"}}}}`, "UpdateCluster", false},
		{"NodePool", `{"nodePool":{"platform":{"type":"AWS","aws":{"instanceType":"m5.large"}}}}`, `{"nodePool":{"platform":{"aws":{"instanceType":"m5.xlarge"}}}}`, "UpdateNodePool", false},
		{"NodePool", `{"nodePool":{"platform":{"type":"AWS"}}}`, `{"nodePool":{"platform":{"type":"None"}}}`, "", true},
		{"Cluster", `{"hostedCluster":{"configuration":{"proxy":{"trustedCA":{"name":"managed"}}}}}`, `{"hostedCluster":{"configuration":null}}`, "UpdateClusterConfig", true},
	} {
		t.Run(tc.kind+tc.raw, func(t *testing.T) {
			var old any = &v1alpha1.ClusterSpec{}
			if tc.kind == "NodePool" {
				old = &v1alpha1.NodePoolSpec{}
			}
			candidate := reflect.New(reflect.TypeOf(old).Elem()).Interface()
			for _, target := range []any{old, candidate} {
				if err := json.Unmarshal([]byte(tc.initial), target); err != nil {
					t.Fatal(err)
				}
			}
			if err := json.Unmarshal([]byte(tc.raw), candidate); err != nil {
				t.Fatal(err)
			}
			actions, errs := NewFieldValidator(tc.kind).AnalyzeUpdate(json.RawMessage(tc.raw), old, candidate, featuregate.Default)
			if (errs != nil) != tc.shouldDeny {
				t.Fatalf("errors = %v, want denial %t", errs, tc.shouldDeny)
			}
			if tc.action != "" && !reflect.DeepEqual(actions, []string{tc.action}) {
				t.Fatalf("actions = %v, want [%s]", actions, tc.action)
			}
		})
	}
}

func TestRequiredUpdateActions_GeneratedResourceMappings(t *testing.T) {
	for kind, paths := range map[string]map[string]string{
		"Cluster":  {"spec.hostedCluster.release": "UpdateClusterVersion", "spec.controlPlaneUpgradePolicy": "UpdateClusterVersion", "spec.hostedCluster.configuration.kubelet.maxPods": "UpdateClusterConfig", "spec.displayName": "UpdateCluster"},
		"NodePool": {"spec.nodePool.replicas": "ScaleNodePool", "spec.nodePool.autoScaling": "ScaleNodePool", "spec.nodePool.release": "UpdateNodePoolVersion", "spec.displayName": "UpdateNodePool"},
	} {
		v := NewFieldValidator(kind)
		for path, want := range paths {
			if got := v.typedRegistry[kind][path].UpdateAction; got != want {
				t.Errorf("%s.%s action = %v, want %s", kind, path, got, want)
			}
		}
	}
}
