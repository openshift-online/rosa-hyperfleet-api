package authz

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const readPermit = `permit(principal, action in HyperFleet::Action::"ReadOnly", resource);`

func bundleFixture() map[string]any {
	return map[string]any{
		"formatVersion":      1,
		"registeredAccounts": []string{accountID, "210987654321"},
		"policies":           []map[string]any{{"id": "read", "ownerAccountID": accountID, "content": readPermit}},
		"attachments":        []map[string]any{{"id": "role-read", "policyID": "read", "principalARN": roleARN, "bindingMode": "role-membership", "scope": "global"}},
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

func fixtureResolver(t *testing.T, bundle map[string]any) *ConfigResolver {
	t.Helper()
	resolver, err := LoadConfig(configFile(t, encodeBundle(t, bundle)))
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func testIdentity(arn string) Identity {
	return Identity{AccountID: accountID, CallerARN: arn, Region: region}
}

func TestResolution(t *testing.T) {
	bundle := bundleFixture()
	bundle["policies"] = append(bundle["policies"].([]map[string]any), map[string]any{"id": "block", "ownerAccountID": accountID, "content": `forbid(principal, action in HyperFleet::Action::"ReadOnly", resource);`})
	bundle["attachments"] = append(bundle["attachments"].([]map[string]any),
		map[string]any{"id": "session-block", "policyID": "block", "principalARN": sessionARN, "bindingMode": "exact-principal", "scope": "global"},
		map[string]any{"id": "role-local", "policyID": "read", "principalARN": roleARN, "bindingMode": "role-membership", "scope": "regional", "region": region},
		map[string]any{"id": "role-west", "policyID": "block", "principalARN": roleARN, "bindingMode": "role-membership", "scope": "regional", "region": "us-west-2"},
		map[string]any{"id": "user-read", "policyID": "read", "principalARN": userARN, "bindingMode": "exact-principal", "scope": "global"},
		map[string]any{"id": "foreign-role", "policyID": "foreign", "principalARN": "arn:aws:iam::210987654321:role/readers", "bindingMode": "role-membership", "scope": "global"},
	)
	bundle["policies"] = append(bundle["policies"].([]map[string]any), map[string]any{"id": "foreign", "ownerAccountID": "210987654321", "content": readPermit})
	content := encodeBundle(t, bundle)
	path := configFile(t, content)
	resolver, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	revision := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	for _, tc := range []struct {
		name     string
		identity Identity
		want     []string
	}{
		{"session and parent role", testIdentity(sessionARN), []string{"role-read", "session-block", "role-local"}},
		{"another session", testIdentity(otherSessionARN), []string{"role-read", "role-local"}},
		{"exact user", testIdentity(userARN), []string{"user-read"}},
		{"empty grants", testIdentity("arn:aws:iam::123456789012:user/bob"), []string{}},
		{"off-region keeps globals", Identity{accountID, sessionARN, "eu-west-1"}, []string{"role-read", "session-block"}},
		{"another account", Identity{"210987654321", "arn:aws:sts::210987654321:assumed-role/readers/s", region}, []string{"foreign-role"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindings, err := resolver.Resolve(context.Background(), tc.identity)
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			ids := map[string]bool{}
			for _, b := range bindings {
				got = append(got, b.AttachmentID)
				if b.PolicyRevision != revision || b.AttachmentRevision != revision || b.PolicyContent == "" || b.Caller != tc.identity || b.PolicyID == "" || b.PrincipalARN == "" || b.Scope == "" || b.BindingMode == "" || b.DiagnosticID == "" || ids[b.DiagnosticID] {
					t.Fatalf("incomplete or duplicated provenance: %+v", b)
				}
				ids[b.DiagnosticID] = true
				if strings.HasPrefix(b.AttachmentID, "role-") && b.PrincipalARN != roleARN {
					t.Fatalf("role path lost: %+v", b)
				}
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("want %v, got %v", tc.want, got)
			}
			if len(bindings) > 0 {
				bindings[0].PolicyContent = "corrupted"
				bindings[0].PrincipalARN = "corrupted"
			}
		})
	}
	if !resolver.IsAccountRegistered(context.Background(), accountID) || resolver.IsAccountRegistered(context.Background(), "999999999999") || resolver.IsAccountRegistered(context.Background(), "") {
		t.Fatal("enrollment is not exact membership")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	bindings, err := resolver.Resolve(context.Background(), testIdentity(sessionARN))
	if err != nil || len(bindings) != 3 || bindings[0].PolicyContent != readPermit {
		t.Fatalf("loaded snapshot changed: %v %+v", err, bindings)
	}
	if replacement, err := LoadConfig(path); err == nil || replacement != nil {
		t.Fatal("invalid replacement accepted")
	}
}

func TestDuplicateYAMLKeys(t *testing.T) {
	valid := encodeBundle(t, bundleFixture())
	// Let the YAML decoder report duplicate keys at both bundle and record level.
	for name, content := range map[string]string{
		"bundle": valid + "\nformatVersion: 1\n",
		"record": strings.Replace(valid, "scope: global", "scope: global\n      scope: global", 1),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeBundle([]byte(content))
			var yamlErr *yaml.TypeError
			if !errors.As(err, &yamlErr) || !strings.Contains(err.Error(), "already defined") {
				t.Fatalf("expected decoder duplicate-key error, got %v", err)
			}
		})
	}
}

func TestMalformedBundles(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"unsupported version", func(b map[string]any) { b["formatVersion"] = 2 }},
		{"unknown field", func(b map[string]any) { b["allowAll"] = true }},
		{"unknown policy field", func(b map[string]any) { b["policies"].([]map[string]any)[0]["extra"] = true }},
		{"unknown attachment field", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["extra"] = true }},
		{"bad account", func(b map[string]any) { b["registeredAccounts"] = []string{"123"} }},
		{"account whitespace", func(b map[string]any) { b["registeredAccounts"] = []string{" " + accountID} }},
		{"duplicate enrollment", func(b map[string]any) { b["registeredAccounts"] = []string{accountID, accountID} }},
		{"bad owner", func(b map[string]any) { b["policies"].([]map[string]any)[0]["ownerAccountID"] = "123" }},
		{"duplicate policy", func(b map[string]any) { p := b["policies"].([]map[string]any); b["policies"] = append(p, p[0]) }},
		{"duplicate attachment", func(b map[string]any) { a := b["attachments"].([]map[string]any); b["attachments"] = append(a, a[0]) }},
		{"empty policy ID", func(b map[string]any) { b["policies"].([]map[string]any)[0]["id"] = "" }},
		{"empty attachment ID", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["id"] = "" }},
		{"whitespace ID", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["id"] = " role " }},
		{"dangling policy", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["policyID"] = "absent" }},
		{"cross account", func(b map[string]any) {
			b["attachments"].([]map[string]any)[0]["principalARN"] = "arn:aws:iam::210987654321:role/readers"
		}},
		{"bad ARN", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["principalARN"] = "readers" }},
		{"GovCloud role rejected", func(b map[string]any) {
			b["attachments"].([]map[string]any)[0]["principalARN"] = "arn:aws-us-gov:iam::123456789012:role/readers"
		}},
		{"China role rejected", func(b map[string]any) {
			b["attachments"].([]map[string]any)[0]["principalARN"] = "arn:aws-cn:iam::123456789012:role/readers"
		}},
		{"bad mode", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["bindingMode"] = "implicit" }},
		{"exact role rejected", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["bindingMode"] = "exact-principal" }},
		{"session membership rejected", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["principalARN"] = sessionARN }},
		{"user membership rejected", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["principalARN"] = userARN }},
		{"unknown scope", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["scope"] = "all" }},
		{"regional needs region", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["scope"] = "regional" }},
		{"global forbids region", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["region"] = region }},
		{"bad region", func(b map[string]any) {
			a := b["attachments"].([]map[string]any)[0]
			a["scope"] = "regional"
			a["region"] = " US EAST "
		}},
		{"ambiguous role alias", func(b map[string]any) {
			b["attachments"] = append(b["attachments"].([]map[string]any), map[string]any{"id": "alias", "policyID": "read", "principalARN": "arn:aws:iam::123456789012:role/other/readers", "bindingMode": "role-membership", "scope": "regional", "region": "us-west-2"})
		}},
		{"malformed permit", func(b map[string]any) { b["policies"].([]map[string]any)[0]["content"] = "permit(" }},
		{"malformed forbid off-region", func(b map[string]any) {
			b["policies"].([]map[string]any)[0]["content"] = "forbid("
			a := b["attachments"].([]map[string]any)[0]
			a["scope"] = "regional"
			a["region"] = "us-west-2"
		}},
		{"schema invalid", func(b map[string]any) {
			b["policies"].([]map[string]any)[0]["content"] = `permit(principal, action, resource) when { context.untrusted == "yes" };`
		}},
		{"unguarded tag", func(b map[string]any) {
			b["policies"].([]map[string]any)[0]["content"] = `forbid(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.getTag("x") == "y" };`
		}},
		{"template rejected", func(b map[string]any) {
			b["policies"].([]map[string]any)[0]["content"] = `permit(principal == ?principal, action, resource);`
		}},
		{"multiple statements", func(b map[string]any) { b["policies"].([]map[string]any)[0]["content"] = readPermit + readPermit }},
		{"trailing Cedar junk", func(b map[string]any) { b["policies"].([]map[string]any)[0]["content"] = readPermit + " garbage" }},
		{"empty policy", func(b map[string]any) { b["policies"].([]map[string]any)[0]["content"] = "" }},
		{"off-region invalid binding", func(b map[string]any) {
			a := b["attachments"].([]map[string]any)[0]
			a["scope"] = "regional"
			a["region"] = "us-west-2"
			a["bindingMode"] = "implicit"
		}},
		{"off-region schema invalid forbid", func(b map[string]any) {
			b["policies"].([]map[string]any)[0]["content"] = `forbid(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.missing == "blocked" };`
			a := b["attachments"].([]map[string]any)[0]
			a["scope"] = "regional"
			a["region"] = "us-west-2"
		}},
		{"unattached invalid policy", func(b map[string]any) {
			b["policies"] = append(b["policies"].([]map[string]any), map[string]any{"id": "unused", "ownerAccountID": accountID, "content": "forbid("})
		}},
	}
	for _, key := range []string{"formatVersion", "registeredAccounts", "policies", "attachments"} {
		cases = append(cases, struct {
			name   string
			mutate func(map[string]any)
		}{"missing " + key, func(b map[string]any) { delete(b, key) }}, struct {
			name   string
			mutate func(map[string]any)
		}{"null " + key, func(b map[string]any) { b[key] = nil }})
	}
	for _, record := range []string{"policies", "attachments"} {
		for key := range bundleFixture()[record].([]map[string]any)[0] {
			for _, value := range []any{nil, ""} {
				cases = append(cases, struct {
					name   string
					mutate func(map[string]any)
				}{fmt.Sprintf("%s %s %v", record, key, value), func(b map[string]any) { b[record].([]map[string]any)[0][key] = value }})
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := bundleFixture()
			tc.mutate(b)
			r, err := LoadConfig(configFile(t, encodeBundle(t, b)))
			if err == nil || r != nil {
				t.Fatalf("accepted malformed bundle: %v %+v", err, r)
			}
		})
	}
	valid := encodeBundle(t, bundleFixture())
	for name, content := range map[string]string{
		"known-field anchor":   strings.Replace(valid, "scope: global", "scope: &scope global", 1),
		"known-field alias":    strings.Replace(strings.Replace(valid, "scope: global", "scope: *mode", 1), "bindingMode: role-membership", "bindingMode: &mode role-membership", 1),
		"nested duplicate key": strings.Replace(valid, "scope: global", "scope: global\n      scope: global", 1),
		"timestamp scalar":     strings.Replace(valid, "scope: global", "scope: 2026-01-01", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if r, err := LoadConfig(configFile(t, content)); err == nil || r != nil {
				t.Fatalf("accepted malformed YAML: %v %+v", err, r)
			}
		})
	}
	for name, content := range map[string]string{
		"empty": "", "syntax": "[", "nonobject": "[]", "second document": valid + "\n---\n{}\n", "empty trailing document": valid + "\n---\n", "trailing junk": valid + "\nnot yaml", "duplicate YAML key": valid + "\nformatVersion: 1\n", "alias": strings.Replace(valid, "scope: global", "scope: &s global", 1) + "\nextra: *s\n", "custom tag": strings.Replace(valid, "scope: global", "scope: !custom global", 1), "merge key": valid + "\n<<: {}\n", "numeric account": strings.Replace(valid, `"123456789012"`, "123456789012", 1),
	} {
		t.Run(name, func(t *testing.T) {
			r, err := LoadConfig(configFile(t, content))
			if err == nil || r != nil {
				t.Fatalf("accepted malformed YAML: %v %+v", err, r)
			}
		})
	}
	if r, err := LoadConfig(filepath.Join(t.TempDir(), "missing")); err == nil || r != nil {
		t.Fatal("missing file accepted")
	}
	empty := map[string]any{"formatVersion": 1, "registeredAccounts": []string{}, "policies": []any{}, "attachments": []any{}}
	r := fixtureResolver(t, empty)
	bindings, err := r.Resolve(context.Background(), testIdentity(userARN))
	if err != nil || len(bindings) != 0 || r.IsAccountRegistered(context.Background(), accountID) {
		t.Fatalf("empty deny bundle failed: %v %+v", err, bindings)
	}
}

func TestIdentityARNBoundary(t *testing.T) {
	resolver := fixtureResolver(t, bundleFixture())
	for _, arn := range []string{
		"arn:aws-us-gov:iam::123456789012:user/alice",
		"arn:aws-us-gov:sts::123456789012:assumed-role/readers/session",
		"arn:aws-cn:iam::123456789012:user/alice",
		"arn:aws-cn:sts::123456789012:assumed-role/readers/session",
		"", "arn:aws:iam::210987654321:user/alice", "arn:aws:s3::123456789012:user/alice", "arn:aws:iam:us-east-1:123456789012:user/alice", "arn:aws:iam::123:user/alice", "arn:aws:iam::123456789012:root", "arn:aws:iam::123456789012:role/readers", "arn:aws:sts::123456789012:assumed-role/path/readers/session", "arn:aws:sts::123456789012:assumed-role/readers/", "arn:aws:iam::123456789012:user//alice", "arn:aws:iam::123456789012:user/../alice", "arn:aws:iam::123456789012:user/alice ", "arn:unknown:iam::123456789012:user/alice", "arn:aws:iam::123456789012:user/alice:extra",
	} {
		t.Run(arn, func(t *testing.T) {
			b, err := resolver.Resolve(context.Background(), testIdentity(arn))
			if err == nil || b != nil {
				t.Fatalf("bad caller accepted: %v %+v", err, b)
			}
		})
	}
	for _, id := range []Identity{{"123", userARN, region}, {accountID, userARN, ""}, {accountID, userARN, "bad region"}} {
		if b, err := resolver.Resolve(context.Background(), id); err == nil || b != nil {
			t.Fatalf("bad identity accepted: %v %+v", err, b)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := resolver.Resolve(ctx, testIdentity(userARN)); err == nil || b != nil {
		t.Fatal("canceled resolution succeeded")
	}
}
