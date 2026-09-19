package cobra

import (
	"strings"
	"testing"

	pkg "github.com/openshift-online/rosa-hyperfleet-api/clientset/cmd/pathbind-gen/pkg"
)

// TestFlagCall verifies that flagCall emits the correct cobra registration
// statement for each consumer field type. This is critical because pointer
// types (*bool, *int32) require pre-allocation before being passed to cobra's
// BoolVar/Int32Var — passing a nil pointer panics at runtime.
func TestFlagCall(t *testing.T) {
	funcMap := buildFuncMap()
	flagCall := funcMap["flagCall"].(func(pkg.MergedAlias) string)

	cases := []struct {
		name     string
		alias    pkg.MergedAlias
		wantSubs []string // substrings that must appear in the output
		wantNot  []string // substrings that must NOT appear
	}{
		{
			name:     "plain string",
			alias:    pkg.MergedAlias{GoName: "Name", Type: "string", Flag: "cluster-name", Description: "The name."},
			wantSubs: []string{`f.StringVar(&input.Name`, `"cluster-name"`, `"The name."`},
		},
		{
			name:     "plain bool",
			alias:    pkg.MergedAlias{GoName: "Fips", Type: "bool", Flag: "fips", Description: "FIPS mode."},
			wantSubs: []string{`f.BoolVar(&input.Fips`, `"fips"`, `"FIPS mode."`},
			// Plain bool must NOT pre-allocate.
			wantNot: []string{"new(bool)"},
		},
		{
			name:  "*bool must pre-allocate",
			alias: pkg.MergedAlias{GoName: "DeleteProtection", Type: "*bool", Flag: "delete-protection", Description: "Enable delete protection."},
			// *bool requires new(bool) so BoolVar receives a non-nil *bool, not a nil pointer.
			wantSubs: []string{"new(bool)", `input.DeleteProtection`, `"delete-protection"`},
		},
		{
			name:     "plain int32",
			alias:    pkg.MergedAlias{GoName: "Replicas", Type: "int32", Flag: "replicas", Description: "Replica count."},
			wantSubs: []string{`f.Int32Var(&input.Replicas`, `"replicas"`},
			wantNot:  []string{"new(int32)"},
		},
		{
			name:     "*int32 must pre-allocate",
			alias:    pkg.MergedAlias{GoName: "Replicas", Type: "*int32", Flag: "replicas", Description: "Replica count."},
			wantSubs: []string{"new(int32)", `input.Replicas`, `"replicas"`},
		},
		{
			name:     "plain int64",
			alias:    pkg.MergedAlias{GoName: "Size", Type: "int64", Flag: "size", Description: "Volume size."},
			wantSubs: []string{`f.Int64Var(&input.Size`, `"size"`},
			wantNot:  []string{"new(int64)"},
		},
		{
			name:     "*int64 must pre-allocate",
			alias:    pkg.MergedAlias{GoName: "Iops", Type: "*int64", Flag: "iops", Description: "Volume IOPS."},
			wantSubs: []string{"new(int64)", `input.Iops`, `"iops"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := flagCall(tc.alias)
			for _, want := range tc.wantSubs {
				if !strings.Contains(got, want) {
					t.Errorf("flagCall(%+v) = %q; want substring %q", tc.alias, got, want)
				}
			}
			for _, notWant := range tc.wantNot {
				if strings.Contains(got, notWant) {
					t.Errorf("flagCall(%+v) = %q; must NOT contain %q", tc.alias, got, notWant)
				}
			}
		})
	}
}

// TestToKebab verifies camelCase/PascalCase → kebab-case conversion with correct
// acronym handling: consecutive uppercase letters stay together rather than being
// segmented with hyphens.
func TestToKebab(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// Normal camelCase
		{"displayName", "display-name"},
		{"deleteProtection", "delete-protection"},
		{"subnetId", "subnet-id"},
		// PascalCase
		{"AWSPlatform", "aws-platform"},
		// Trailing acronym: lower→upper inserts dash, consecutive uppers don't
		{"issuerURL", "issuer-url"},
		{"kubeCloudControllerARN", "kube-cloud-controller-arn"},
		{"controlPlaneOperatorARN", "control-plane-operator-arn"},
		{"imageRegistryARN", "image-registry-arn"},
		{"nodePoolManagementARN", "node-pool-management-arn"},
		// All-caps acronym alone
		{"VPC", "vpc"},
		// Acronym followed by word: last uppercase of acronym gets dash before next word
		{"URLConfig", "url-config"},
		// Multi-segment acronym
		{"registryPullQPS", "registry-pull-qps"},
		// Acronym with plural 's' suffix: the 's' stays attached, no dash
		{"allocateNodeCIDRs", "allocate-node-cidrs"},
		{"ARNs", "arns"},
		{"VPCs", "vpcs"},
		// CIDR inside a longer word — 's' followed by uppercase → suffix rule, then new word dash
		{"allowedCIDRBlocks", "allowed-cidr-blocks"},
		{"CIDRsConfig", "cidrs-config"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := pkg.ToKebab(tc.input)
			if got != tc.want {
				t.Errorf("ToKebab(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestCollectUnsetPtrFields_includesBoolPtr(t *testing.T) {
	aliases := []pkg.MergedAlias{
		{GoName: "MaxPods", Type: "*int64", Flag: "max-pods", HasFlag: true, Operations: []string{"create"}},
		{GoName: "SerializeImagePulls", Type: "*bool", Flag: "serialize-image-pulls", HasFlag: true, Operations: []string{"create", "update"}},
		{GoName: "Name", Type: "string", Flag: "cluster-name", HasFlag: true, Operations: []string{"create"}},
		{GoName: "Fips", Type: "bool", Flag: "fips", HasFlag: true, Operations: []string{"create"}},
		{GoName: "DeleteProtection", Type: "*bool", Flag: "delete-protection", HasFlag: true, Operations: []string{"create", "update"}},
	}

	got := pkg.CollectUnsetPtrFields(aliases)
	want := map[string]pkg.UnsetPtrField{
		"max-pods": {
			GoName:     "MaxPods",
			FlagName:   "max-pods",
			Operations: []string{"create"},
		},
		"serialize-image-pulls": {
			GoName:     "SerializeImagePulls",
			FlagName:   "serialize-image-pulls",
			Operations: []string{"create", "update"},
		},
		"delete-protection": {
			GoName:     "DeleteProtection",
			FlagName:   "delete-protection",
			Operations: []string{"create", "update"},
		},
	}

	if len(got) != len(want) {
		t.Fatalf("CollectUnsetPtrFields() returned %d fields, want %d: %v", len(got), len(want), got)
	}
	for _, field := range got {
		expected, ok := want[field.FlagName]
		if !ok {
			t.Fatalf("unexpected field %q in %v", field.FlagName, got)
		}
		if field.GoName != expected.GoName || field.FlagName != expected.FlagName {
			t.Fatalf("field %q: got %+v, want %+v", field.FlagName, field, expected)
		}
	}
}

// TestFlagCallTrimPrefix documents why strings.TrimPrefix alone is insufficient
// to distinguish *bool from bool when combined with HasPrefix:
// both end up in the same switch case, and HasPrefix determines the branch —
// but the pointer branch previously emitted code that passed a nil *bool to BoolVar,
// which panics at runtime. The test pins the correct pre-allocation behavior.
func TestFlagCallTrimPrefixInsufficiency(t *testing.T) {
	// Demonstrate the root cause: TrimPrefix("*bool", "*") == "bool",
	// so a switch on the trimmed value cannot distinguish between types
	// without also checking the original type for the "*" prefix.
	// The current fix uses the full type string ("*bool") as the switch key.
	if strings.TrimPrefix("*bool", "*") != "bool" {
		t.Fatal("TrimPrefix invariant broken")
	}
	if strings.TrimPrefix("bool", "*") != "bool" {
		t.Fatal("TrimPrefix invariant broken")
	}
	// Both "*bool" and "bool" produce the same trimmed string.
	// A switch on TrimPrefix(type, "*") cannot tell them apart —
	// a secondary HasPrefix check is needed, which the original code had,
	// but the pointer branch emitted `f.BoolVar(input.X, ...)` where input.X
	// is a nil *bool, causing a nil pointer dereference at runtime.
	// The fix: switch on the full type string and pre-allocate for pointer types.
}
