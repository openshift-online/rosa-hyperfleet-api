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

func testPrincipal(t *testing.T, arn string) principalARN {
	t.Helper()
	principal, err := parsePrincipal(arn)
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

func TestResolution(t *testing.T) {
	bundle := bundleFixture()
	bundle["policies"] = append(bundle["policies"].([]map[string]any), map[string]any{"id": "block", "ownerAccountID": accountID, "content": `forbid(principal, action in HyperFleet::Action::"ReadOnly", resource);`})
	bundle["attachments"] = append(bundle["attachments"].([]map[string]any),
		map[string]any{"id": "session-block", "policyID": "block", "principalARN": sessionARN, "scope": "global"},
		map[string]any{"id": "role-local", "policyID": "read", "principalARN": roleARN, "scope": "regional", "region": region},
		map[string]any{"id": "role-west", "policyID": "block", "principalARN": roleARN, "scope": "regional", "region": "us-west-2"},
		map[string]any{"id": "user-read", "policyID": "read", "principalARN": userARN, "scope": "global"},
		map[string]any{"id": "foreign-role", "policyID": "foreign", "principalARN": "arn:aws:iam::210987654321:role/readers", "scope": "global"},
	)
	bundle["policies"] = append(bundle["policies"].([]map[string]any), map[string]any{"id": "foreign", "ownerAccountID": "210987654321", "content": readPermit})
	content := encodeBundle(t, bundle)
	path := configFile(t, content)
	authorizer, err := LoadConfig(path, region)
	if err != nil {
		t.Fatal(err)
	}
	revision := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	for _, tc := range []struct {
		name          string
		caller        string
		serviceRegion string
		want          []string
	}{
		{"session and parent role", sessionARN, region, []string{"role-read", "session-block", "role-local"}},
		{"another session", otherSessionARN, region, []string{"role-read", "role-local"}},
		{"exact user", userARN, region, []string{"user-read"}},
		{"empty grants", "arn:aws:iam::123456789012:user/bob", region, []string{}},
		{"unrelated role", "arn:aws:sts::123456789012:assumed-role/writers/s", region, []string{}},
		{"off-region keeps globals", sessionARN, "eu-west-1", []string{"role-read", "session-block"}},
		{"another account", "arn:aws:sts::210987654321:assumed-role/readers/s", region, []string{"foreign-role"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := LoadConfig(path, tc.serviceRegion)
			if err != nil {
				t.Fatal(err)
			}
			bindings, err := a.source.resolve(context.Background(), testPrincipal(t, tc.caller))
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			ids := map[string]bool{}
			for _, b := range bindings.customer {
				got = append(got, b.AttachmentID)
				if b.PolicyRevision != revision || b.AttachmentRevision != revision || b.policy == nil || b.principal.original != b.PrincipalARN || ids[b.DiagnosticID] {
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
			if len(bindings.customer) > 0 {
				original := bindings.customer[0]
				bindings.customer[0].policy = nil
				bindings.customer[0].PrincipalARN = "corrupted"
				again, err := a.source.resolve(t.Context(), testPrincipal(t, tc.caller))
				if err != nil || len(again.customer) != len(bindings.customer) || again.customer[0] != original {
					t.Fatalf("source attachment changed through returned copies: %+v %v", again, err)
				}
			}
		})
	}
	if !authorizer.IsAccountRegistered(context.Background(), accountID) || authorizer.IsAccountRegistered(context.Background(), "999999999999") || authorizer.IsAccountRegistered(context.Background(), "") {
		t.Fatal("enrollment is not exact membership")
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	bindings, err := authorizer.source.resolve(context.Background(), testPrincipal(t, sessionARN))
	if err != nil || len(bindings.customer) != 3 || bindings.customer[0].policy == nil || bindings.customer[0].PrincipalARN != roleARN {
		t.Fatalf("loaded snapshot changed: %v %+v", err, bindings)
	}
	if replacement, err := LoadConfig(path, region); err == nil || replacement != nil {
		t.Fatal("invalid replacement accepted")
	}
}

func TestLoadConfigRejectsV2(t *testing.T) {
	source := encodeBundle(t, bundleFixture())
	if _, err := LoadConfig(configFile(t, source), region); err != nil {
		t.Fatal(err)
	}
	service := serviceBundle()
	wrapper := map[string]any{
		"formatVersion": 2, "sourceConfig": source, "registeredAccount": accountID,
		"serviceOperatorPolicies":    service["serviceOperatorPolicies"],
		"serviceOperatorAttachments": service["serviceOperatorAttachments"],
	}
	if a, err := LoadConfig(configFile(t, encodeBundle(t, wrapper)), region); err == nil || a != nil {
		t.Fatal("LoadConfig accepted a valid version-2 wrapper")
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
		{"unknown scope", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["scope"] = "all" }},
		{"regional needs region", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["scope"] = "regional" }},
		{"global forbids region", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["region"] = region }},
		{"bad region", func(b map[string]any) {
			a := b["attachments"].([]map[string]any)[0]
			a["scope"] = "regional"
			a["region"] = " US EAST "
		}},
		{"ambiguous role alias", func(b map[string]any) {
			b["attachments"] = append(b["attachments"].([]map[string]any), map[string]any{"id": "alias", "policyID": "read", "principalARN": "arn:aws:iam::123456789012:role/other/readers", "scope": "regional", "region": "us-west-2"})
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
			a["principalARN"] = "invalid"
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
			r, err := LoadConfig(configFile(t, encodeBundle(t, b)), region)
			if err == nil || r != nil {
				t.Fatalf("accepted malformed bundle: %v %+v", err, r)
			}
		})
	}
	valid := encodeBundle(t, bundleFixture())
	for name, content := range map[string]string{
		"known-field anchor":   strings.Replace(valid, "scope: global", "scope: &scope global", 1),
		"known-field alias":    strings.Replace(strings.Replace(valid, "scope: global", "scope: *scope", 1), "id: role-read", "id: &scope role-read", 1),
		"nested duplicate key": strings.Replace(valid, "scope: global", "scope: global\n      scope: global", 1),
		"timestamp scalar":     strings.Replace(valid, "scope: global", "scope: 2026-01-01", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if r, err := LoadConfig(configFile(t, content), region); err == nil || r != nil {
				t.Fatalf("accepted malformed YAML: %v %+v", err, r)
			}
		})
	}
	for name, content := range map[string]string{
		"empty": "", "syntax": "[", "nonobject": "[]", "second document": valid + "\n---\n{}\n", "empty trailing document": valid + "\n---\n", "trailing junk": valid + "\nnot yaml", "duplicate YAML key": valid + "\nformatVersion: 1\n", "alias": strings.Replace(valid, "scope: global", "scope: &s global", 1) + "\nextra: *s\n", "custom tag": strings.Replace(valid, "scope: global", "scope: !custom global", 1), "merge key": valid + "\n<<: {}\n", "numeric account": strings.Replace(valid, `"123456789012"`, "123456789012", 1),
	} {
		t.Run(name, func(t *testing.T) {
			r, err := LoadConfig(configFile(t, content), region)
			if err == nil || r != nil {
				t.Fatalf("accepted malformed YAML: %v %+v", err, r)
			}
		})
	}
	if r, err := LoadConfig(filepath.Join(t.TempDir(), "missing"), region); err == nil || r != nil {
		t.Fatal("missing file accepted")
	}
	empty := map[string]any{"formatVersion": 1, "registeredAccounts": []string{}, "policies": []any{}, "attachments": []any{}}
	r := fixtureAuthorizer(t, empty)
	bindings, err := r.source.resolve(context.Background(), testPrincipal(t, userARN))
	if err != nil || len(bindings.customer) != 0 || len(bindings.serviceOperator) != 0 || r.IsAccountRegistered(context.Background(), accountID) {
		t.Fatalf("empty deny bundle failed: %v %+v", err, bindings)
	}
}

func TestIdentityARNBoundary(t *testing.T) {
	authorizer := fixtureAuthorizer(t, bundleFixture())
	for _, arn := range []string{
		"arn:aws-us-gov:iam::123456789012:user/alice",
		"arn:aws-us-gov:sts::123456789012:assumed-role/readers/session",
		"arn:aws-cn:iam::123456789012:user/alice",
		"arn:aws-cn:sts::123456789012:assumed-role/readers/session",
		"", "arn:aws:iam::210987654321:user/alice", "arn:aws:s3::123456789012:user/alice", "arn:aws:iam:us-east-1:123456789012:user/alice", "arn:aws:iam::123:user/alice", "arn:aws:iam::123456789012:root", "arn:aws:iam::123456789012:role/readers", "arn:aws:sts::123456789012:assumed-role/path/readers/session", "arn:aws:sts::123456789012:assumed-role/readers/", "arn:aws:iam::123456789012:user//alice", "arn:aws:iam::123456789012:user/../alice", "arn:aws:iam::123456789012:user/alice ", "arn:unknown:iam::123456789012:user/alice", "arn:aws:iam::123456789012:user/alice:extra",
	} {
		t.Run(arn, func(t *testing.T) {
			b, err := authorizer.Prepare(context.Background(), testIdentity(arn), testRequestContext())
			if err == nil || b != nil {
				t.Fatalf("bad caller accepted: %v %+v", err, b)
			}
		})
	}
	for _, id := range []Identity{{"123", userARN}, {"", userARN}} {
		if b, err := authorizer.Prepare(context.Background(), id, testRequestContext()); err == nil || b != nil {
			t.Fatalf("bad identity accepted: %v %+v", err, b)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if b, err := authorizer.Prepare(ctx, testIdentity(userARN), testRequestContext()); err == nil || b != nil {
		t.Fatal("canceled resolution succeeded")
	}
}
