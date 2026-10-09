//go:build integration

package authz

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func requirePlatformChart(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("helm"); err != nil {
		t.Skip("Helm is required for mounted-bundle integration checks")
	}
	chart := filepath.Join("..", "..", "..", "..", "rosa-hyperfleet", "argocd", "config", "regional-cluster", "platform-api")
	if _, err := os.Stat(chart); err != nil {
		t.Skip("sibling platform chart is required for mounted-bundle integration checks")
	}
	return chart
}

func renderChartBundle(t *testing.T, source, account, serviceRegion string) string {
	t.Helper()
	chart := requirePlatformChart(t)
	values := map[string]any{
		"global":      map[string]any{"aws_account_id": account, "aws_region": serviceRegion},
		"platformApi": map[string]any{"authz": map[string]any{"config": source}},
	}
	path := configFile(t, encodeBundle(t, values))
	output, err := exec.CommandContext(t.Context(), "helm", "template", "authz-test", chart, "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("Helm rendering failed: %v\n%s", err, output)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(output)))
	for {
		var document struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Data map[string]string `yaml:"data"`
		}
		if err := decoder.Decode(&document); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		if document.Kind == "ConfigMap" && document.Metadata.Name == "authz-config" {
			mounted := document.Data["config.yaml"]
			want := strings.NewReplacer("@@AWS_ACCOUNT_ID@@", account, "@@AWS_REGION@@", serviceRegion).Replace(source)
			if mounted != strings.TrimRight(want, "\n")+"\n" {
				t.Fatalf("chart changed more than runtime tokens:\nwant %q\ngot  %q", want, mounted)
			}
			return mounted
		}
	}
	t.Fatal("authorization ConfigMap missing")
	return ""
}

func TestChartEnvironmentBundles(t *testing.T) {
	requirePlatformChart(t)
	const renderEnvironment = `
import sys
from pathlib import Path
root, environment, account_override, role_override = sys.argv[1:]
root = Path(root)
sys.path.insert(0, str(root / "scripts"))
import render
merged = render.load_yaml(root / "config" / "defaults.yaml")
merged = render.deep_merge(merged, render.load_yaml(root / "config" / environment / "defaults.yaml"))
merged = render.deep_merge(merged, render.load_yaml(root / "config" / environment / "us-east-1.yaml"))
if account_override:
    merged["aws"]["account_id"] = account_override
if role_override:
    merged["aws"]["child_admin_role_name"] = role_override
ctx = render.build_context(merged, environment, "us-east-1", "authz-test" if environment == "ephemeral" else "")
ctx["management_clusters"] = render.build_mc_list(ctx, merged, ctx["eph_prefix"])
applications = render.resolve_templates(ctx["applications"], ctx)
ctx.update(cluster_type="regional-cluster", application_values=applications["regional-cluster"])
sys.stdout.write(render.render_template(root / "config" / "templates" / "argocd-values.yaml.j2", ctx))
`
	for _, tc := range []struct {
		name, environment, account, role, accountOverride, roleOverride, buildID string
		hasCustomerGrant                                                         bool
	}{
		{"default dev", "ephemeral", "599476212575", "OrganizationAccountAccessRole", "599476212575", "", "", false},
		{"CI", "ephemeral", "720644165472", "OrganizationAccountAccessRole", "720644165472", "", "test-ci", true},
		{"custom RC", "ephemeral", "987654321098", "rosa-hyperfleet-account-admin", "987654321098", "rosa-hyperfleet-account-admin", "test-ci", false},
		{"stable stage", "stage", "112233445566", "rosa-hyperfleet-account-admin", "", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BUILD_ID", tc.buildID)
			root := filepath.Join("..", "..", "..", "..", "rosa-hyperfleet")
			output, err := exec.CommandContext(t.Context(), "python3", "-B", "-c", renderEnvironment, root, tc.environment, tc.accountOverride, tc.roleOverride).CombinedOutput()
			if err != nil {
				t.Fatalf("environment rendering failed: %v\n%s", err, output)
			}
			var values struct {
				PlatformAPI struct {
					Authz map[string]string `yaml:"authz"`
				} `yaml:"platformApi"`
			}
			if err := yaml.Unmarshal(output, &values); err != nil {
				t.Fatal(err)
			}
			if len(values.PlatformAPI.Authz) != 1 || values.PlatformAPI.Authz["config"] == "" {
				t.Fatal("environment must supply only a complete authorization config")
			}
			mounted := renderChartBundle(t, values.PlatformAPI.Authz["config"], tc.account, region)
			path := configFile(t, mounted)
			a, err := LoadConfig(path, region)
			if err != nil {
				t.Fatalf("mounted environment rejected: %v\n%s", errors.Unwrap(err), mounted)
			}
			if !a.IsAccountRegistered(t.Context(), tc.account) || a.IsAccountRegistered(t.Context(), "210987654321") {
				t.Fatal("runtime RC enrollment is not exact")
			}
			for _, caller := range []struct {
				name, account, arn, serviceRegion string
				canManage, canReadCustomer        bool
			}{
				{"operator", tc.account, fmt.Sprintf("arn:aws:sts::%s:assumed-role/%s/session", tc.account, tc.role), region, true, tc.hasCustomerGrant},
				{"unrelated role", tc.account, fmt.Sprintf("arn:aws:sts::%s:assumed-role/Unrelated/session", tc.account), region, false, false},
				{"unrelated account", "210987654321", fmt.Sprintf("arn:aws:sts::210987654321:assumed-role/%s/session", tc.role), region, false, false},
				{"unrelated region", tc.account, fmt.Sprintf("arn:aws:sts::%s:assumed-role/%s/session", tc.account, tc.role), "eu-west-1", false, false},
				{"customer", "720644165472", "arn:aws:iam::720644165472:user/e2e", region, false, tc.buildID != ""},
			} {
				t.Run(caller.name, func(t *testing.T) {
					a, err := LoadConfig(path, caller.serviceRegion)
					if err != nil {
						t.Fatal(err)
					}
					prepared, err := a.Prepare(t.Context(), Identity{AccountID: caller.account, CallerARN: caller.arn}, testRequestContext())
					if err != nil {
						t.Fatal(err)
					}
					mc := Resource{Kind: ManagementCluster, ID: "mc", AccountID: "210987654321", RegistrationRegion: "us-west-2"}
					for _, check := range []struct {
						action   Action
						resource Resource
						want     bool
					}{
						{CreateManagementCluster, mc, caller.canManage},
						{ListManagementClusters, Resource{Kind: ServiceCollection}, caller.canManage},
						{DescribeManagementCluster, mc, caller.canManage},
						{DescribeCluster, Resource{Kind: Cluster, ID: "cluster", AccountID: caller.account}, caller.canReadCustomer},
					} {
						d, err := prepared.Check(t.Context(), check.action, check.resource)
						if err != nil || d.Allowed != check.want {
							t.Fatalf("%s allowed=%v want=%v err=%v", check.action, d.Allowed, check.want, err)
						}
					}
				})
			}
		})
	}
}

func TestChartNoImplicitGrants(t *testing.T) {
	for _, tc := range []struct {
		name                                   string
		isEnrolled, canManage, canReadCustomer bool
	}{
		{"empty", false, false, false},
		{"enrollment only", true, false, false},
		{"customer only", true, false, true},
		{"operator only", true, true, false},
		{"unenrolled operator", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := serviceBundle()
			if !tc.isEnrolled {
				bundle["registeredAccounts"] = []string{}
			}
			if !tc.canManage {
				bundle["serviceOperatorPolicies"], bundle["serviceOperatorAttachments"] = []any{}, []any{}
			}
			if !tc.canReadCustomer {
				bundle["policies"], bundle["attachments"] = []any{}, []any{}
			} else {
				bundle["policies"].([]map[string]any)[0]["content"] = `permit(principal, action, resource);`
			}
			source := "# café \"quote\" \\ <>&\n" + strings.ReplaceAll(encodeBundle(t, bundle), accountID, "@@AWS_ACCOUNT_ID@@")
			mounted := renderChartBundle(t, source, accountID, region)
			a, err := LoadConfig(configFile(t, mounted), region)
			if err != nil {
				t.Fatal(err)
			}
			if a.IsAccountRegistered(t.Context(), accountID) != tc.isEnrolled {
				t.Fatal("chart inferred enrollment")
			}
			prepared, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []struct {
				action   Action
				resource Resource
				want     bool
			}{
				{ListManagementClusters, Resource{Kind: ServiceCollection}, tc.canManage},
				{DescribeCluster, testCluster(), tc.canReadCustomer},
			} {
				d, err := prepared.Check(t.Context(), check.action, check.resource)
				if err != nil || d.Allowed != check.want {
					t.Fatalf("%s allowed=%v want=%v err=%v", check.action, d.Allowed, check.want, err)
				}
			}
		})
	}
}

func TestChartRejectsMalformedBundles(t *testing.T) {
	bundle := serviceBundle()
	attachment := bundle["serviceOperatorAttachments"].([]map[string]any)[0]
	attachment["scope"], attachment["region"] = "regional", "@@AWS_REGION@@"
	valid := strings.ReplaceAll(encodeBundle(t, bundle), accountID, "@@AWS_ACCOUNT_ID@@")
	wrapper := encodeBundle(t, map[string]any{
		"formatVersion": 2, "sourceConfig": valid, "registeredAccount": "@@AWS_ACCOUNT_ID@@",
		"serviceOperatorPolicies": []any{}, "serviceOperatorAttachments": []any{},
	})
	for name, source := range map[string]string{
		"root duplicate":       valid + "\nformatVersion: 1\n",
		"policy duplicate":     strings.Replace(valid, "content: "+readPermit, "content: "+readPermit+"\n      content: "+readPermit, 1),
		"attachment duplicate": strings.Replace(valid, "scope: regional", "scope: regional\n      scope: global", 1),
		"version-2 wrapper":    wrapper,
	} {
		t.Run(name, func(t *testing.T) {
			mounted := renderChartBundle(t, source, accountID, region)
			a, err := LoadConfig(configFile(t, mounted), region)
			if err == nil || a != nil {
				t.Fatal("mounted malformed configuration reached authorization startup")
			}
			if strings.Contains(name, "duplicate") {
				var duplicate *yaml.TypeError
				if !errors.As(err, &duplicate) || !strings.Contains(duplicate.Error(), "already defined") {
					t.Fatalf("expected duplicate-key rejection, got %v", errors.Unwrap(err))
				}
			}
		})
	}
}
