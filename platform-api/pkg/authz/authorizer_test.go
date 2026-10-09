package authz

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
)

type sourceFunc func(context.Context, principalARN) (resolvedPolicies, error)

func (f sourceFunc) resolve(ctx context.Context, caller principalARN) (resolvedPolicies, error) {
	return f(ctx, caller)
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

func TestPreparedDecisions(t *testing.T) {
	for _, tc := range []struct {
		name, content string
		action        Action
		resource      Resource
		allowed       bool
	}{
		{"allow describe", readPermit, DescribeCluster, testCluster(), true},
		{"allow collection", readPermit, ListClusters, testCollection(), true},
		{"deny", `forbid(principal, action in HyperFleet::Action::"ReadOnly", resource);`, DescribeCluster, testCluster(), false},
		{"list only cannot describe", `permit(principal, action == HyperFleet::Action::"ListClusters", resource);`, DescribeCluster, testCluster(), false},
		{"describe only cannot list", `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource);`, ListClusters, testCollection(), false},
		{"matching label", labelPermit, DescribeCluster, testCluster(), true},
		{"absent labels", labelPermit, DescribeCluster, Resource{Kind: Cluster, ID: "id", AccountID: accountID}, false},
		{"trusted context", `permit(principal, action in HyperFleet::Action::"ReadOnly", resource) when { context.accountId == "123456789012" && context.region == "us-east-1" && context.principalArn == "arn:aws:sts::123456789012:assumed-role/readers/session-a" };`, DescribeCluster, testCluster(), true},
		{"membership retains role graph", `permit(principal in HyperFleet::Role::"arn:aws:iam::123456789012:role/platform/readers", action in HyperFleet::Action::"ReadOnly", resource);`, DescribeCluster, testCluster(), true},
		{"equality remains restrictive", `permit(principal == HyperFleet::Role::"arn:aws:iam::123456789012:role/platform/readers", action in HyperFleet::Action::"ReadOnly", resource);`, DescribeCluster, testCluster(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bundle := bundleFixture()
			bundle["policies"].([]map[string]any)[0]["content"] = tc.content
			a := fixtureAuthorizer(t, bundle)
			p, err := a.Prepare(context.Background(), testIdentity(sessionARN), testRequestContext())
			if err != nil || p == nil {
				t.Fatalf("prepare: %v", err)
			}
			decision, err := p.Check(context.Background(), tc.action, tc.resource)
			if err != nil || decision.Allowed != tc.allowed {
				t.Fatalf("want allowed %v, got %+v %v", tc.allowed, decision, err)
			}
			if tc.allowed && (len(decision.Provenance) != 1 || decision.Provenance[0].AttachmentID != "role-read" || decision.Provenance[0].PrincipalARN != roleARN) {
				t.Fatalf("missing determining provenance: %+v", decision)
			}
		})
	}
	t.Run("resolve once and no grants", func(t *testing.T) {
		var calls atomic.Int32
		a := fixtureAuthorizer(t, bundleFixture())
		a.source = sourceFunc(func(context.Context, principalARN) (resolvedPolicies, error) {
			calls.Add(1)
			return resolvedPolicies{}, nil
		})
		p, err := a.Prepare(context.Background(), testIdentity(sessionARN), testRequestContext())
		if err != nil {
			t.Fatal(err)
		}
		for range 4 {
			d, err := p.Check(context.Background(), DescribeCluster, testCluster())
			if err != nil || d.Allowed {
				t.Fatalf("empty set did not deny: %+v %v", d, err)
			}
		}
		if calls.Load() != 1 {
			t.Fatalf("source called %d times", calls.Load())
		}
	})
}

func TestForbidResolution(t *testing.T) {
	bundle := bundleFixture()
	bundle["policies"] = append(bundle["policies"].([]map[string]any), map[string]any{"id": "forbid", "ownerAccountID": accountID, "content": `forbid(principal, action == HyperFleet::Action::"DescribeCluster", resource);`})
	bundle["attachments"] = append(bundle["attachments"].([]map[string]any), map[string]any{"id": "session-forbid", "policyID": "forbid", "principalARN": sessionARN, "scope": "global"})
	for _, tc := range []struct {
		name, caller string
		allowed      bool
		reason       string
	}{{"forbidden session", sessionARN, false, "session-forbid"}, {"another session", otherSessionARN, true, "role-read"}} {
		t.Run(tc.name, func(t *testing.T) {
			a := fixtureAuthorizer(t, bundle)
			p, err := a.Prepare(context.Background(), testIdentity(tc.caller), testRequestContext())
			if err != nil {
				t.Fatal(err)
			}
			d, err := p.Check(context.Background(), DescribeCluster, testCluster())
			if err != nil || d.Allowed != tc.allowed || len(d.Provenance) != 1 || d.Provenance[0].AttachmentID != tc.reason {
				t.Fatalf("bad combined decision: %+v %v", d, err)
			}
		})
	}
	bundle["attachments"].([]map[string]any)[0]["scope"] = "regional"
	bundle["attachments"].([]map[string]any)[0]["region"] = region
	a := fixtureAuthorizer(t, bundle)
	p, err := a.Prepare(context.Background(), testIdentity(sessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	d, err := p.Check(context.Background(), DescribeCluster, testCluster())
	if err != nil || d.Allowed {
		t.Fatalf("regional permit erased global forbid: %+v %v", d, err)
	}
}

func TestPreparationFailures(t *testing.T) {
	a := fixtureAuthorizer(t, serviceBundle())
	good, err := a.source.resolve(t.Context(), testPrincipal(t, sessionARN))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("source failure with partial material", func(t *testing.T) {
		broken := *a
		broken.source = sourceFunc(func(context.Context, principalARN) (resolvedPolicies, error) {
			return good, errors.New("store details secret")
		})
		p, err := broken.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
		if p != nil {
			t.Fatal("source failure returned usable prepared set")
		}
		_ = checkFailure(t, err, StageResolution)
	})
	t.Run("source cancels after resolution", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		broken := *a
		broken.source = sourceFunc(func(context.Context, principalARN) (resolvedPolicies, error) {
			cancel()
			return good, nil
		})
		p, err := broken.Prepare(ctx, testIdentity(sessionARN), testRequestContext())
		if p != nil {
			t.Fatal("canceled source returned usable prepared set")
		}
		_ = checkFailure(t, err, StageResolution)
	})
	if p, err := a.Prepare(context.Background(), Identity{accountID, "arn:aws:iam::210987654321:user/alice"}, testRequestContext()); p != nil || err == nil {
		t.Fatal("identity mismatch produced prepared set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if p, err := a.Prepare(ctx, testIdentity(sessionARN), testRequestContext()); p != nil || err == nil {
		t.Fatal("canceled preparation succeeded")
	}
}

func TestResourceValidation(t *testing.T) {
	a := fixtureAuthorizer(t, bundleFixture())
	p, err := a.Prepare(context.Background(), testIdentity(sessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		action   Action
		resource Resource
	}{
		{"foreign account", DescribeCluster, Resource{Kind: Cluster, ID: "id", AccountID: "210987654321"}},
		{"missing owner", DescribeCluster, Resource{Kind: Cluster, ID: "id"}},
		{"missing ID", DescribeCluster, Resource{Kind: Cluster, AccountID: accountID}},
		{"bad ID", DescribeCluster, Resource{Kind: Cluster, ID: "../id", AccountID: accountID}},
		{"invalid kind", DescribeCluster, Resource{Kind: ResourceKind("NodePool"), ID: "id", AccountID: accountID}},
		{"wrong action kind", ListClusters, testCluster()},
		{"unknown action", Action("UnimplementedAction"), testCluster()},
		{"collection labels", ListClusters, Resource{Kind: Collection, AccountID: accountID, Labels: map[string]string{"team": "blue"}}},
		{"collection ID", ListClusters, Resource{Kind: Collection, ID: "other", AccountID: accountID}},
		{"contradictory account label", DescribeCluster, Resource{Kind: Cluster, ID: "id", AccountID: accountID, Labels: map[string]string{"hyperfleet.io/account-id": "210987654321"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := p.Check(context.Background(), tc.action, tc.resource)
			if d.Allowed || len(d.Provenance) != 0 {
				t.Fatalf("failure produced usable decision: %+v", d)
			}
			_ = checkFailure(t, err, StageEntityValidation)
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d, err := p.Check(ctx, DescribeCluster, testCluster())
	if d.Allowed {
		t.Fatal("canceled check allowed")
	}
	_ = checkFailure(t, err, StageEvaluation)
	t.Run("native validation", func(t *testing.T) {
		principal := p.entities[uid("Principal", sessionARN)]
		principal.Attributes = cedar.NewRecord(nil)
		p.entities[principal.UID] = principal
		d, err := p.Check(context.Background(), DescribeCluster, testCluster())
		if d.Allowed {
			t.Fatal("invalid principal allowed")
		}
		_ = checkFailure(t, err, StageEntityValidation)
	})
}

func TestEvaluationFailures(t *testing.T) {
	bundle := bundleFixture()
	bundle["policies"] = append(bundle["policies"].([]map[string]any), map[string]any{"id": "forbid", "ownerAccountID": accountID, "content": `forbid(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.account == "blocked" };`})
	bundle["attachments"] = append(bundle["attachments"].([]map[string]any),
		map[string]any{"id": "forbid-a", "policyID": "forbid", "principalARN": sessionARN, "scope": "global"},
		map[string]any{"id": "forbid-b", "policyID": "forbid", "principalARN": roleARN, "scope": "global"},
	)
	for _, name := range []string{"evaluator error", "allow plus failing forbid", "deny plus diagnostics", "unknown diagnostic", "unknown reason", "unknown and known diagnostics"} {
		t.Run(name, func(t *testing.T) {
			a := fixtureAuthorizer(t, bundle)
			var native cedar.Diagnostic
			a.evaluate = evaluationScenario(t, name, &native)
			p, err := a.Prepare(context.Background(), testIdentity(sessionARN), testRequestContext())
			if err != nil {
				t.Fatal(err)
			}
			before := p.provenance["attachment/forbid-b"]
			d, err := p.Check(context.Background(), DescribeCluster, testCluster())
			if d.Allowed || len(d.Provenance) != 0 {
				t.Fatalf("diagnostics produced usable decision: %+v", d)
			}
			f := checkFailure(t, err, StageEvaluation)
			if len(native.Errors) > 0 && (len(f.Provenance) != 1 || f.Provenance[0].AttachmentID != "forbid-a" || f.Provenance[0].PolicyID != "forbid" || len(f.Diagnostics) != 1 || f.Diagnostics[0].PolicyID != native.Errors[0].PolicyID) {
				t.Fatalf("bad diagnostic mapping: %+v", f)
			}
			if name == "unknown and known diagnostics" && (len(f.Provenance) != 1 || f.Provenance[0].AttachmentID != "forbid-a" || len(f.Diagnostics) != 2) {
				t.Fatalf("known provenance lost beside unknown diagnostic: %+v", f)
			}
			if !reflect.DeepEqual(before, p.provenance["attachment/forbid-b"]) {
				t.Fatal("another attachment's provenance changed")
			}
		})
	}
	t.Run("two reasons one policy", func(t *testing.T) {
		b := bundleFixture()
		b["attachments"] = append(b["attachments"].([]map[string]any), map[string]any{"id": "exact-read", "policyID": "read", "principalARN": sessionARN, "scope": "global"})
		a := fixtureAuthorizer(t, b)
		p, err := a.Prepare(context.Background(), testIdentity(sessionARN), testRequestContext())
		if err != nil {
			t.Fatal(err)
		}
		d, err := p.Check(context.Background(), DescribeCluster, testCluster())
		if err != nil || !d.Allowed || len(d.Provenance) != 2 || d.Provenance[0].DiagnosticID == d.Provenance[1].DiagnosticID || d.Provenance[0].PolicyID != d.Provenance[1].PolicyID {
			t.Fatalf("bad shared-policy mapping: %+v %v", d, err)
		}
	})
}

func evaluationScenario(t *testing.T, name string, native *cedar.Diagnostic) evaluator {
	t.Helper()
	return func(set *cedar.PolicySet, entities cedar.EntityMap, req cedar.Request) (cedar.Decision, cedar.Diagnostic, error) {
		if name == "evaluator error" {
			return cedar.Allow, cedar.Diagnostic{}, errors.New("engine failure")
		}
		if name == "unknown diagnostic" {
			return cedar.Allow, cedar.Diagnostic{Errors: []cedar.DiagnosticError{{PolicyID: "not-in-set", Message: "bad"}}}, nil
		}
		if name == "unknown reason" {
			return cedar.Allow, cedar.Diagnostic{Reasons: []cedar.DiagnosticReason{{PolicyID: "not-in-set"}}}, nil
		}
		if name == "unknown and known diagnostics" {
			return cedar.Allow, cedar.Diagnostic{Errors: []cedar.DiagnosticError{{PolicyID: "-unknown", Message: "bad"}, {PolicyID: "attachment/forbid-a", Message: "known"}}}, nil
		}
		broken := entities[req.Resource]
		broken.Attributes = cedar.NewRecord(cedar.RecordMap{"region": cedar.String(region)})
		entities[req.Resource] = broken
		// Inject a failure in one attachment without changing the prepared ASTs.
		injected := cedar.NewPolicySet()
		for id, policy := range set.All() {
			injected.Add(id, policy)
		}
		unrelated := parsePolicy(t, `forbid(principal == HyperFleet::Principal::"arn:aws:sts::123456789012:assumed-role/readers/session-b", action == HyperFleet::Action::"DescribeCluster", resource) when { resource.account == "blocked" };`)
		injected.Add("attachment/forbid-b", unrelated)
		decision, diagnostics := cedar.Authorize(injected, entities, req)
		*native = diagnostics
		if decision != cedar.Allow || len(diagnostics.Errors) != 1 {
			t.Fatalf("native diagnostic fixture changed: %s %+v", decision, diagnostics)
		}
		if name == "deny plus diagnostics" {
			decision = cedar.Deny
		}
		return decision, diagnostics, nil
	}
}

func TestCompiledPolicyReuse(t *testing.T) {
	a := fixtureAuthorizer(t, serviceBundle())
	loaded, err := a.source.resolve(t.Context(), testPrincipal(t, sessionARN))
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.customer) != 1 || len(loaded.serviceOperator) != 1 {
		t.Fatalf("missing loaded attachments: %+v", loaded)
	}
	first, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.Prepare(t.Context(), testIdentity(otherSessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, domain := range []struct {
		binding       policyAttachment
		first, second *cedar.PolicySet
	}{
		{loaded.customer[0], first.policies, second.policies},
		{loaded.serviceOperator[0], first.servicePolicies, second.servicePolicies},
	} {
		id := cedar.PolicyID(domain.binding.DiagnosticID)
		if domain.binding.policy == nil || domain.first.Get(id) != domain.binding.policy || domain.second.Get(id) != domain.binding.policy {
			t.Fatal("preparation did not reuse the startup-compiled policy")
		}
		domain.first.Remove(id)
		if domain.second.Get(id) != domain.binding.policy {
			t.Fatal("per-request policy sets share mutable state")
		}
	}
	for _, check := range []struct {
		action   Action
		resource Resource
	}{
		{DescribeCluster, testCluster()},
		{ListManagementClusters, Resource{Kind: ServiceCollection}},
	} {
		d, err := second.Check(t.Context(), check.action, check.resource)
		if err != nil || !d.Allowed {
			t.Fatalf("reused policy failed %s: %+v %v", check.action, d, err)
		}
	}
}

func TestConcurrentPreparation(t *testing.T) {
	a := fixtureAuthorizer(t, bundleFixture())
	for range 16 {
		t.Run("request", func(t *testing.T) {
			t.Parallel()
			p, err := a.Prepare(context.Background(), testIdentity(sessionARN), testRequestContext())
			if err != nil {
				t.Fatal(err)
			}
			for range 8 {
				d, err := p.Check(context.Background(), DescribeCluster, testCluster())
				if err != nil || !d.Allowed {
					t.Fatalf("concurrent check: %+v %v", d, err)
				}
			}
		})
	}
}
