package authz

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	accountID       = "123456789012"
	region          = "us-east-1"
	roleARN         = "arn:aws:iam::123456789012:role/platform/readers"
	sessionARN      = "arn:aws:sts::123456789012:assumed-role/readers/session-a"
	otherSessionARN = "arn:aws:sts::123456789012:assumed-role/readers/session-b"
	userARN         = "arn:aws:iam::123456789012:user/alice"
	readPermit      = `permit(principal, action in Seaplane::Action::"ReadOnly", resource);`
	labelPermit     = `@description("stored labels, not spec tags")
permit(principal, action == Seaplane::Action::"DescribeCluster", resource is Seaplane::Cluster)
when { resource.hasTag("example.com/team") && resource.getTag("example.com/team") == "blue \"team\"" }
unless { resource.hasTag("example.com/blocked") };`
)

func bundleFixture() map[string]any {
	return map[string]any{
		"formatVersion":      1,
		"registeredAccounts": []string{accountID, "210987654321"},
		"policies":           []map[string]any{{"id": "read", "ownerAccountID": accountID, "content": readPermit}},
		"attachments":        []map[string]any{{"id": "role-read", "policyID": "read", "principalARN": roleARN, "scope": "global"}},
	}
}

func encodeBundle(t *testing.T, bundle map[string]any) string {
	t.Helper()
	content, err := yaml.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func configFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func fixtureAuthorizer(t *testing.T, bundle map[string]any) *Authorizer {
	t.Helper()
	authorizer, err := LoadConfig(configFile(t, encodeBundle(t, bundle)), region)
	if err != nil {
		t.Fatal(err)
	}
	return authorizer
}

func testIdentity(arn string) Identity {
	return Identity{AccountID: accountID, CallerARN: arn}
}

func testRequestContext() RequestContext {
	return RequestContext{SourceIP: "192.0.2.1", UserAgent: "authz-test", RequestTime: time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)}
}

func testCluster() Resource {
	return Resource{Kind: Cluster, ID: "4610b27e-cf07-4a18-b1a1-7e8f52e95701", AccountID: accountID, Labels: map[string]string{"example.com/team": `blue "team"`}}
}

func testCollection() Resource {
	return Resource{Kind: Collection, CollectionKind: Cluster, AccountID: accountID}
}

func checkFailure(t *testing.T, err error, stage Stage) *Failure {
	t.Helper()
	var failure *Failure
	if !errors.As(err, &failure) || failure.Stage != stage || failure.Err == nil || err.Error() != "authorization failed" {
		t.Fatalf("want safe %s failure, got %v (%+v)", stage, err, failure)
	}
	return failure
}

// decide prepares one request and checks a single action.
func decide(t *testing.T, a *Authorizer, id Identity, action Action, resource Resource) Decision {
	t.Helper()
	p, err := a.Prepare(t.Context(), id, testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	d, err := p.Check(t.Context(), action, resource)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestLoadConfigRejects(t *testing.T) {
	type mutation struct {
		name   string
		base   func() map[string]any
		mutate func(map[string]any)
	}
	policy := func(b map[string]any, key string) map[string]any { return b[key].([]map[string]any)[0] }
	attach := func(b map[string]any) map[string]any { return policy(b, "attachments") }
	offRegion := func(a map[string]any) { a["scope"], a["region"] = "regional", "us-west-2" }
	cases := []mutation{
		// One case per rejection rule; customer and service records share the same validation loop.
		{"unsupported version", bundleFixture, func(b map[string]any) { b["formatVersion"] = 2 }},
		{"unknown field", bundleFixture, func(b map[string]any) { b["allowAll"] = true }},
		{"unknown attachment field", bundleFixture, func(b map[string]any) { attach(b)["extra"] = true }},
		{"missing attachments", bundleFixture, func(b map[string]any) { delete(b, "attachments") }},
		{"null policies", bundleFixture, func(b map[string]any) { b["policies"] = nil }},
		{"bad account", bundleFixture, func(b map[string]any) { b["registeredAccounts"] = []string{"123"} }},
		{"duplicate enrollment", bundleFixture, func(b map[string]any) { b["registeredAccounts"] = []string{accountID, accountID} }},
		{"bad owner", bundleFixture, func(b map[string]any) { policy(b, "policies")["ownerAccountID"] = "123" }},
		{"duplicate policy", bundleFixture, func(b map[string]any) { p := b["policies"].([]map[string]any); b["policies"] = append(p, p[0]) }},
		{"duplicate attachment", bundleFixture, func(b map[string]any) { a := b["attachments"].([]map[string]any); b["attachments"] = append(a, a[0]) }},
		{"empty attachment ID", bundleFixture, func(b map[string]any) { attach(b)["id"] = "" }},
		{"dangling policy", bundleFixture, func(b map[string]any) { attach(b)["policyID"] = "absent" }},
		{"cross account", bundleFixture, func(b map[string]any) { attach(b)["principalARN"] = "arn:aws:iam::210987654321:role/readers" }},
		{"bad ARN", bundleFixture, func(b map[string]any) { attach(b)["principalARN"] = "readers" }},
		{"GovCloud role", bundleFixture, func(b map[string]any) { attach(b)["principalARN"] = "arn:aws-us-gov:iam::123456789012:role/readers" }},
		{"regional needs region", bundleFixture, func(b map[string]any) { attach(b)["scope"] = "regional" }},
		{"global forbids region", bundleFixture, func(b map[string]any) { attach(b)["region"] = region }},
		{"ambiguous role alias", bundleFixture, func(b map[string]any) {
			b["attachments"] = append(b["attachments"].([]map[string]any), map[string]any{"id": "alias", "policyID": "read", "principalARN": "arn:aws:iam::123456789012:role/other/readers", "scope": "regional", "region": "us-west-2"})
		}},
		{"malformed permit", bundleFixture, func(b map[string]any) { policy(b, "policies")["content"] = "permit(" }},
		{"schema invalid", bundleFixture, func(b map[string]any) {
			policy(b, "policies")["content"] = `permit(principal, action, resource) when { context.untrusted == "yes" };`
		}},
		{"unguarded tag", bundleFixture, func(b map[string]any) {
			policy(b, "policies")["content"] = `forbid(principal, action == Seaplane::Action::"DescribeCluster", resource) when { resource.getTag("x") == "y" };`
		}},
		{"template", bundleFixture, func(b map[string]any) {
			policy(b, "policies")["content"] = `permit(principal == ?principal, action, resource);`
		}},
		{"multiple statements", bundleFixture, func(b map[string]any) { policy(b, "policies")["content"] = readPermit + readPermit }},
		{"off-region invalid binding", bundleFixture, func(b map[string]any) { offRegion(attach(b)); attach(b)["principalARN"] = "invalid" }},
		{"unattached invalid policy", bundleFixture, func(b map[string]any) {
			b["policies"] = append(b["policies"].([]map[string]any), map[string]any{"id": "unused", "ownerAccountID": accountID, "content": "forbid("})
		}},
		{"service operator domain", bundleFixture, func(b map[string]any) { b["serviceOperatorPolicies"] = []any{} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.base()
			tc.mutate(b)
			if a, err := LoadConfig(configFile(t, encodeBundle(t, b)), region); err == nil || a != nil {
				t.Fatalf("accepted invalid bundle: %v", err)
			}
		})
	}

	valid := encodeBundle(t, bundleFixture())
	yamlCases := map[string]string{
		"syntax": "[", "nonobject": "[]",
		"second document":       valid + "\n---\n{}\n",
		"duplicate key":         valid + "\nformatVersion: 1\n",
		"alias":                 strings.Replace(strings.Replace(valid, "scope: global", "scope: *scope", 1), "id: role-read", "id: &scope role-read", 1),
		"custom tag":            strings.Replace(valid, "scope: global", "scope: !custom global", 1),
		"merge key":             valid + "\n<<: {}\n",
		"numeric account":       strings.Replace(valid, `"123456789012"`, "123456789012", 1),
		"string format version": strings.Replace(valid, "formatVersion: 1", `formatVersion: "1"`, 1),
	}
	for name, content := range yamlCases {
		t.Run(name, func(t *testing.T) {
			if a, err := LoadConfig(configFile(t, content), region); err == nil || a != nil {
				t.Fatalf("accepted malformed YAML: %v", err)
			}
		})
	}

	path := configFile(t, valid)
	for _, invalid := range []string{"", "invalid"} {
		if a, err := LoadConfig(path, invalid); err == nil || a != nil {
			t.Fatalf("invalid service region %q accepted", invalid)
		}
	}
	if a, err := LoadConfig(filepath.Join(t.TempDir(), "missing"), region); err == nil || a != nil {
		t.Fatal("missing file accepted")
	}
}

func TestStartupFailureProvenance(t *testing.T) {
	bundle := bundleFixture()
	bundle["policies"].([]map[string]any)[0]["content"] = "forbid( secret"
	_, err := LoadConfig(configFile(t, encodeBundle(t, bundle)), region)
	if f := checkFailure(t, err, StageParsing); len(f.Provenance) != 1 || f.Provenance[0].PolicyID != "read" || f.Provenance[0].BundleHash == "" {
		t.Fatalf("lost startup policy provenance: %+v", f.Provenance)
	}
	bundle = bundleFixture()
	bundle["attachments"].([]map[string]any)[0]["scope"] = "unsupported"
	_, err = LoadConfig(configFile(t, encodeBundle(t, bundle)), region)
	if f := checkFailure(t, err, StageBinding); len(f.Provenance) != 1 || f.Provenance[0].AttachmentID != "role-read" || f.Provenance[0].BundleHash == "" {
		t.Fatalf("lost startup binding provenance: %+v", f.Provenance)
	}
}

func TestStartupSnapshot(t *testing.T) {
	original := encodeBundle(t, bundleFixture())
	path := configFile(t, original)
	authorizer, err := LoadConfig(path, region)
	if err != nil {
		t.Fatal(err)
	}
	revision := func(a *Authorizer) string {
		t.Helper()
		d := decide(t, a, testIdentity(sessionARN), DescribeCluster, testCluster())
		if !d.Allowed || len(d.Provenance) != 1 || a.BundleHash() != d.Provenance[0].BundleHash {
			t.Fatalf("fixture did not allow or bundle hash differs: %+v", d)
		}
		return d.Provenance[0].BundleHash
	}
	if revision(authorizer) != fmt.Sprintf("%x", sha256.Sum256([]byte(original))) {
		t.Fatal("revision does not hash the file bytes")
	}

	// Replacing the file affects only later loads; whitespace changes the revision.
	if err := os.WriteFile(path, []byte(original+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadConfig(path, region)
	if err != nil {
		t.Fatal(err)
	}
	if revision(reloaded) == revision(authorizer) {
		t.Fatal("whitespace change kept the revision")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if a, err := LoadConfig(path, region); err == nil || a != nil {
		t.Fatal("invalid replacement accepted")
	}
	revision(authorizer)

	// Enrollment is exact membership and independent of grants.
	if !authorizer.IsAccountRegistered(t.Context(), accountID) || authorizer.IsAccountRegistered(t.Context(), "999999999999") || authorizer.IsAccountRegistered(t.Context(), "") {
		t.Fatal("enrollment is not exact membership")
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if authorizer.IsAccountRegistered(canceled, accountID) {
		t.Fatal("canceled enrollment succeeded")
	}
	unenrolled := bundleFixture()
	unenrolled["registeredAccounts"] = []string{}
	independent := fixtureAuthorizer(t, unenrolled)
	if independent.IsAccountRegistered(t.Context(), accountID) || !decide(t, independent, testIdentity(sessionARN), DescribeCluster, testCluster()).Allowed {
		t.Fatal("enrollment and grants were coupled")
	}
	empty := fixtureAuthorizer(t, map[string]any{"formatVersion": 1, "registeredAccounts": []string{}, "policies": []any{}, "attachments": []any{}})
	if empty.IsAccountRegistered(t.Context(), accountID) || decide(t, empty, testIdentity(userARN), DescribeCluster, testCluster()).Allowed {
		t.Fatal("empty bundle granted access")
	}

}
