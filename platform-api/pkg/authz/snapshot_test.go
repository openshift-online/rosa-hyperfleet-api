package authz

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

func TestReloadSnapshot(t *testing.T) {
	original := encodeBundle(t, bundleFixture())
	path := configFile(t, original)
	resolver, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	first, err := resolver.Resolve(t.Context(), testIdentity(sessionARN))
	if err != nil {
		t.Fatal(err)
	}
	replacement := bundleFixture()
	replacement["registeredAccounts"] = []string{}
	replacement["attachments"] = []any{}
	changed := encodeBundle(t, replacement)
	if err := os.WriteFile(path, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	old, err := resolver.Resolve(t.Context(), testIdentity(sessionARN))
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := loaded.Resolve(t.Context(), testIdentity(sessionARN))
	if err != nil {
		t.Fatal(err)
	}
	if len(old) != 1 || len(fresh) != 0 || old[0].PolicyRevision != first[0].PolicyRevision || !resolver.IsAccountRegistered(t.Context(), accountID) || loaded.IsAccountRegistered(t.Context(), accountID) {
		t.Fatalf("snapshot or reload semantics changed: old=%+v fresh=%+v", old, fresh)
	}
	whitespace := original + "\n"
	if err := os.WriteFile(path, []byte(whitespace), 0600); err != nil {
		t.Fatal(err)
	}
	next, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	bindings, err := next.Resolve(t.Context(), testIdentity(sessionARN))
	if err != nil {
		t.Fatal(err)
	}
	if bindings[0].PolicyRevision == first[0].PolicyRevision || bindings[0].PolicyRevision != fmt.Sprintf("%x", sha256.Sum256([]byte(whitespace))) {
		t.Fatalf("revision does not identify exact bytes: %+v", bindings)
	}
	// Enrollment does not manufacture a grant or restrict policy resolution itself.
	unenrolled := bundleFixture()
	unenrolled["registeredAccounts"] = []string{}
	independent := fixtureResolver(t, unenrolled)
	applicable, err := independent.Resolve(t.Context(), testIdentity(sessionARN))
	if err != nil || len(applicable) != 1 || independent.IsAccountRegistered(t.Context(), accountID) {
		t.Fatalf("enrollment and resolution were coupled: %+v %v", applicable, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if independent.IsAccountRegistered(ctx, accountID) {
		t.Fatal("canceled enrollment succeeded")
	}
}

func TestStartupProvenance(t *testing.T) {
	bundle := bundleFixture()
	bundle["policies"].([]map[string]any)[0]["content"] = "forbid( secret"
	if resolver, err := LoadConfig(configFile(t, encodeBundle(t, bundle))); resolver != nil || err == nil {
		t.Fatal("bad policy accepted")
	} else {
		failure := checkFailure(t, err, StageParsing)
		if len(failure.Provenance) != 1 || failure.Provenance[0].PolicyID != "read" || failure.Provenance[0].PolicyRevision == "" {
			t.Fatalf("lost startup policy provenance: %+v", failure.Provenance)
		}
	}
	bundle = bundleFixture()
	bundle["attachments"].([]map[string]any)[0]["bindingMode"] = "unsupported"
	if resolver, err := LoadConfig(configFile(t, encodeBundle(t, bundle))); resolver != nil || err == nil {
		t.Fatal("bad binding accepted")
	} else {
		failure := checkFailure(t, err, StageBinding)
		if len(failure.Provenance) != 1 || failure.Provenance[0].AttachmentID != "role-read" || failure.Provenance[0].PrincipalARN != roleARN || failure.Provenance[0].AttachmentRevision == "" {
			t.Fatalf("lost startup binding provenance: %+v", failure.Provenance)
		}
	}
}
