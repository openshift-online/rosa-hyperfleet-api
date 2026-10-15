package authz

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	cedar "github.com/cedar-policy/cedar-go"
)

// grant is one policy attached to one principal. Empty policy reuses the ID,
// empty region is global, and empty owner is the test account.
type grant struct{ id, policy, content, principal, region, owner string }

func grants(list ...grant) ([]map[string]any, []map[string]any) {
	policies, attachments := []map[string]any{}, []map[string]any{}
	seen := map[string]bool{}
	for _, g := range list {
		policyID := g.policy
		if policyID == "" {
			policyID = g.id
		}
		owner := g.owner
		if owner == "" {
			owner = accountID
		}
		if !seen[policyID] {
			seen[policyID] = true
			policies = append(policies, map[string]any{"id": policyID, "ownerAccountID": owner, "content": g.content})
		}
		attachment := map[string]any{"id": g.id, "policyID": policyID, "principalARN": g.principal, "scope": "global"}
		if g.region != "" {
			attachment["scope"], attachment["region"] = "regional", g.region
		}
		attachments = append(attachments, attachment)
	}
	return policies, attachments
}

func grantBundle(customer, service []grant) map[string]any {
	b := map[string]any{"formatVersion": 1, "registeredAccounts": []string{accountID}}
	b["policies"], b["attachments"] = grants(customer...)
	if service != nil {
		b["serviceOperatorPolicies"], b["serviceOperatorAttachments"] = grants(service...)
	}
	return b
}

func TestDecisions(t *testing.T) {
	const (
		forbidRead    = `forbid(principal, action in HyperFleet::Action::"ReadOnly", resource);`
		serviceGroup  = `permit(principal, action in HyperFleet::Action::"ServiceOperator", resource);`
		everything    = `permit(principal, action, resource);`
		westOnly      = `permit(principal, action, resource) when { context.region == "us-west-2" && resource.region == "us-west-2" };`
		otherAccount  = "arn:aws:sts::210987654321:assumed-role/readers/s"
		otherRoleARN  = "arn:aws:iam::210987654321:role/readers"
		authoredLimit = `permit(principal == HyperFleet::Principal::"arn:aws:sts::123456789012:assumed-role/readers/session-a", action == HyperFleet::Action::"DescribeCluster", resource in HyperFleet::Collection::"123456789012/us-east-1/clusters")
when { resource.hasTag("example.com/team") && resource.getTag("example.com/team") == "blue \"team\"" }
unless { resource.hasTag("example.com/blocked") };`
	)
	read := grant{id: "role-read", policy: "read", content: readPermit, principal: roleARN}
	mc := Resource{Kind: ManagementCluster, ID: "mc", AccountID: "210987654321", RegistrationRegion: "us-west-2"}
	services := Resource{Kind: ServiceCollection}
	pool := func(parent string) Resource {
		return Resource{Kind: NodePool, ID: "workers", AccountID: accountID, ParentCluster: &ParentCluster{ID: parent, AccountID: accountID, Labels: map[string]string{"team": "blue"}}}
	}
	labeled := func(labels map[string]string) Resource { r := testCluster(); r.Labels = labels; return r }

	for _, tc := range []struct {
		name            string
		customer        []grant
		service         []grant
		serviceRegion   string
		caller, account string
		action          Action
		resource        Resource
		allowed         bool
		determining     []string
	}{
		// Actions, groups, labels, and context.
		{name: "role grant allows describe", customer: []grant{read}, allowed: true, determining: []string{"role-read"}},
		{name: "role grant allows list", customer: []grant{read}, action: ListClusters, resource: testCollection(), allowed: true},
		{name: "no grants deny", customer: []grant{}},
		{name: "list only cannot describe", customer: []grant{{id: "g", content: `permit(principal, action == HyperFleet::Action::"ListClusters", resource);`, principal: roleARN}}},
		{name: "describe only cannot list", customer: []grant{{id: "g", content: `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource);`, principal: roleARN}}, action: ListClusters, resource: testCollection()},
		{name: "stored label matches", customer: []grant{{id: "g", content: labelPermit, principal: roleARN}}, allowed: true},
		{name: "absent labels deny", customer: []grant{{id: "g", content: labelPermit, principal: roleARN}}, resource: labeled(nil)},
		{name: "trusted context", customer: []grant{{id: "g", content: `permit(principal, action in HyperFleet::Action::"ReadOnly", resource) when { context.accountId == "123456789012" && context.region == "us-east-1" && context.principalArn == "arn:aws:sts::123456789012:assumed-role/readers/session-a" };`, principal: roleARN}}, allowed: true},
		{name: "role membership", customer: []grant{{id: "g", content: `permit(principal in HyperFleet::Role::"arn:aws:iam::123456789012:role/platform/readers", action in HyperFleet::Action::"ReadOnly", resource);`, principal: roleARN}}, allowed: true},
		{name: "role equality stays restrictive", customer: []grant{{id: "g", content: `permit(principal == HyperFleet::Role::"arn:aws:iam::123456789012:role/platform/readers", action in HyperFleet::Action::"ReadOnly", resource);`, principal: roleARN}}},
		{name: "two attachments one policy", customer: []grant{read, {id: "exact-read", policy: "read", content: readPermit, principal: sessionARN}}, allowed: true, determining: []string{"exact-read", "role-read"}},

		// Principal matching and forbid precedence.
		{name: "session forbid beats role permit", customer: []grant{read, {id: "session-block", content: forbidRead, principal: sessionARN}}, determining: []string{"session-block"}},
		{name: "sibling session unaffected", customer: []grant{read, {id: "session-block", content: forbidRead, principal: sessionARN}}, caller: otherSessionARN, allowed: true, determining: []string{"role-read"}},
		{name: "regional permit cannot erase global forbid", customer: []grant{{id: "local-read", policy: "read", content: readPermit, principal: roleARN, region: region}, {id: "session-block", content: forbidRead, principal: sessionARN}}},
		{name: "exact user", customer: []grant{read, {id: "user-read", policy: "read", content: readPermit, principal: userARN}}, caller: userARN, allowed: true, determining: []string{"user-read"}},
		{name: "unrelated user", customer: []grant{read}, caller: "arn:aws:iam::123456789012:user/bob"},
		{name: "unrelated role session", customer: []grant{read}, caller: "arn:aws:sts::123456789012:assumed-role/writers/s"},
		{name: "role in another account", customer: []grant{read, {id: "foreign", content: readPermit, principal: otherRoleARN, owner: "210987654321"}}, caller: otherAccount, account: "210987654321", resource: Resource{Kind: Cluster, ID: "c", AccountID: "210987654321"}, allowed: true, determining: []string{"foreign"}},

		// Global and regional attachments in a fixed service region.
		{name: "regional attachment in region", customer: []grant{{id: "east", policy: "read", content: readPermit, principal: roleARN, region: region}}, allowed: true, determining: []string{"east"}},
		{name: "regional attachment off region", customer: []grant{{id: "west", policy: "read", content: readPermit, principal: roleARN, region: "us-west-2"}}},
		{name: "global attachment off region", customer: []grant{read}, serviceRegion: "eu-west-1", allowed: true},
		{name: "regional attachment other service region", customer: []grant{{id: "east", policy: "read", content: readPermit, principal: roleARN, region: region}}, serviceRegion: "us-west-2"},

		// Authored restrictions are not rewritten by attachment selection.
		{name: "authored session matches", customer: []grant{{id: "g", content: authoredLimit, principal: roleARN}}, allowed: true},
		{name: "authored session excludes sibling", customer: []grant{{id: "g", content: authoredLimit, principal: roleARN}}, caller: otherSessionARN},
		{name: "authored action excludes update", customer: []grant{{id: "g", content: authoredLimit, principal: roleARN}}, action: UpdateCluster},
		{name: "authored label differs", customer: []grant{{id: "g", content: authoredLimit, principal: roleARN}}, resource: labeled(map[string]string{"example.com/team": "red"})},
		{name: "authored unless blocks", customer: []grant{{id: "g", content: authoredLimit, principal: roleARN}}, resource: labeled(map[string]string{"example.com/team": `blue "team"`, "example.com/blocked": "yes"})},

		// Customer and service authority are disjoint.
		{name: "customer wildcard cannot list MCs", customer: []grant{{id: "g", content: everything, principal: roleARN}}, action: ListManagementClusters, resource: services},
		{name: "customer wildcard cannot describe MC", customer: []grant{{id: "g", content: everything, principal: roleARN}}, action: DescribeManagementCluster, resource: mc},
		{name: "customer wildcard cannot create MC", customer: []grant{{id: "g", content: everything, principal: roleARN}}, action: CreateManagementCluster, resource: mc},
		{name: "customer service group cannot create MC", customer: []grant{{id: "g", content: serviceGroup, principal: roleARN}}, action: CreateManagementCluster, resource: mc},
		{name: "customer AllActions covers clusters", customer: []grant{{id: "g", content: `permit(principal, action in HyperFleet::Action::"AllActions", resource);`, principal: roleARN}}, allowed: true},
		{name: "customer AllActions excludes MCs", customer: []grant{{id: "g", content: `permit(principal, action in HyperFleet::Action::"AllActions", resource);`, principal: roleARN}}, action: CreateManagementCluster, resource: mc},
		{name: "service wildcard cannot read clusters", service: []grant{{id: "svc", content: everything, principal: roleARN}}},
		{name: "service wildcard creates MC", service: []grant{{id: "svc", content: everything, principal: roleARN}}, action: CreateManagementCluster, resource: mc, allowed: true, determining: []string{"svc"}},
		{name: "service wildcard lists MCs", service: []grant{{id: "svc", content: everything, principal: roleARN}}, action: ListManagementClusters, resource: services, allowed: true},
		{name: "customer forbid cannot cancel service permit", customer: []grant{{id: "g", content: `forbid(principal, action, resource);`, principal: roleARN}}, service: []grant{{id: "svc", content: serviceGroup, principal: roleARN}}, action: CreateManagementCluster, resource: mc, allowed: true},
		{name: "service forbid on one action", service: []grant{{id: "svc", content: serviceGroup, principal: roleARN}, {id: "no-describe", content: `forbid(principal, action == HyperFleet::Action::"DescribeManagementCluster", resource);`, principal: roleARN}}, action: DescribeManagementCluster, resource: mc},
		{name: "service forbid leaves other actions", service: []grant{{id: "svc", content: serviceGroup, principal: roleARN}, {id: "no-describe", content: `forbid(principal, action == HyperFleet::Action::"DescribeManagementCluster", resource);`, principal: roleARN}}, action: CreateManagementCluster, resource: mc, allowed: true},
		{name: "service session forbid", service: []grant{{id: "svc", content: serviceGroup, principal: roleARN}, {id: "block", content: `forbid(principal, action in HyperFleet::Action::"ServiceOperator", resource);`, principal: sessionARN}}, action: ListManagementClusters, resource: services},
		{name: "service sibling session allowed", service: []grant{{id: "svc", content: serviceGroup, principal: roleARN}, {id: "block", content: `forbid(principal, action in HyperFleet::Action::"ServiceOperator", resource);`, principal: sessionARN}}, caller: otherSessionARN, action: ListManagementClusters, resource: services, allowed: true},

		// Node pools use their stored parent.
		{name: "node pool under permitted parent", customer: []grant{{id: "g", content: `permit(principal, action == HyperFleet::Action::"DescribeNodePool", resource in HyperFleet::Cluster::"123456789012/us-east-1/parent-a");`, principal: roleARN}}, action: DescribeNodePool, resource: pool("parent-a"), allowed: true},
		{name: "same-name node pool under other parent", customer: []grant{{id: "g", content: `permit(principal, action == HyperFleet::Action::"DescribeNodePool", resource in HyperFleet::Cluster::"123456789012/us-east-1/parent-a");`, principal: roleARN}}, action: DescribeNodePool, resource: pool("parent-b")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.serviceRegion == "" {
				tc.serviceRegion = region
			}
			if tc.caller == "" {
				tc.caller = sessionARN
			}
			if tc.account == "" {
				tc.account = accountID
			}
			if tc.action == "" {
				tc.action = DescribeCluster
			}
			if tc.resource.Kind == "" {
				tc.resource = testCluster()
			}
			a, err := LoadConfig(configFile(t, encodeBundle(t, grantBundle(tc.customer, tc.service))), tc.serviceRegion)
			if err != nil {
				t.Fatal(err)
			}
			d := decide(t, a, Identity{AccountID: tc.account, CallerARN: tc.caller}, tc.action, tc.resource)
			if d.Allowed != tc.allowed {
				t.Fatalf("allowed = %v, want %v: %+v", d.Allowed, tc.allowed, d)
			}
			if tc.determining == nil {
				return
			}
			got := []string{}
			for _, p := range d.Provenance {
				got = append(got, p.AttachmentID)
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.determining) {
				t.Fatalf("determining attachments = %v, want %v", got, tc.determining)
			}
		})
	}
}

func TestPrepareRejects(t *testing.T) {
	a := fixtureAuthorizer(t, bundleFixture())
	identities := []Identity{{"123", userARN}, {"", userARN}, {accountID, "arn:aws:iam::210987654321:user/alice"}}
	for _, arn := range []string{
		"", "arn:aws-us-gov:iam::123456789012:user/alice", "arn:aws-us-gov:sts::123456789012:assumed-role/readers/session",
		"arn:aws-cn:iam::123456789012:user/alice", "arn:aws-cn:sts::123456789012:assumed-role/readers/session",
		"arn:aws:s3::123456789012:user/alice", "arn:aws:iam:us-east-1:123456789012:user/alice", "arn:aws:iam::123:user/alice",
		"arn:aws:iam::123456789012:root", "arn:aws:iam::123456789012:role/readers", "arn:aws:sts::123456789012:assumed-role/path/readers/session",
		"arn:aws:sts::123456789012:assumed-role/readers/", "arn:aws:iam::123456789012:user//alice", "arn:aws:iam::123456789012:user/../alice",
		"arn:aws:iam::123456789012:user/alice ", "arn:unknown:iam::123456789012:user/alice", "arn:aws:iam::123456789012:user/alice:extra",
	} {
		identities = append(identities, testIdentity(arn))
	}
	for _, id := range identities {
		t.Run(id.AccountID+" "+id.CallerARN, func(t *testing.T) {
			if p, err := a.Prepare(t.Context(), id, testRequestContext()); err == nil || p != nil {
				t.Fatalf("bad caller accepted: %v", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if p, err := a.Prepare(canceled, testIdentity(userARN), testRequestContext()); p != nil {
		t.Fatal("canceled preparation succeeded")
	} else {
		_ = checkFailure(t, err, StageResolution)
	}
	if p, err := a.Prepare(t.Context(), testIdentity(sessionARN), RequestContext{}); err == nil || p != nil {
		t.Fatal("missing request time accepted")
	}
}

func TestCheckRejectsResource(t *testing.T) {
	a := fixtureAuthorizer(t, bundleFixture())
	p, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	foreignParent := Resource{Kind: NodePool, ID: "workers", AccountID: accountID, ParentCluster: &ParentCluster{ID: "parent", AccountID: "210987654321"}}
	for _, tc := range []struct {
		name     string
		action   Action
		resource Resource
	}{
		{"foreign account", DescribeCluster, Resource{Kind: Cluster, ID: "id", AccountID: "210987654321"}},
		{"missing owner", DescribeCluster, Resource{Kind: Cluster, ID: "id"}},
		{"missing ID", DescribeCluster, Resource{Kind: Cluster, AccountID: accountID}},
		{"bad ID", DescribeCluster, Resource{Kind: Cluster, ID: "../id", AccountID: accountID}},
		{"wrong kind", DescribeCluster, Resource{Kind: NodePool, ID: "id", AccountID: accountID}},
		{"wrong action kind", ListClusters, testCluster()},
		{"unknown action", Action("UnimplementedAction"), testCluster()},
		{"collection labels", ListClusters, Resource{Kind: Collection, AccountID: accountID, Labels: map[string]string{"team": "blue"}}},
		{"collection ID", ListClusters, Resource{Kind: Collection, ID: "other", AccountID: accountID}},
		{"contradictory account label", DescribeCluster, Resource{Kind: Cluster, ID: "id", AccountID: accountID, Labels: map[string]string{"hyperfleet.io/account-id": "210987654321"}}},
		{"foreign node pool parent", DescribeNodePool, foreignParent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d, err := p.Check(t.Context(), tc.action, tc.resource)
			if d.Allowed || len(d.Provenance) != 0 {
				t.Fatalf("failure produced usable decision: %+v", d)
			}
			_ = checkFailure(t, err, StageEntityValidation)
		})
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	d, err := p.Check(canceled, DescribeCluster, testCluster())
	if d.Allowed {
		t.Fatal("canceled check allowed")
	}
	_ = checkFailure(t, err, StageEvaluation)

	principal := p.entities[entityUID("Principal", sessionARN)]
	principal.Attributes = cedar.NewRecord(nil)
	p.entities[principal.UID] = principal
	d, err = p.Check(t.Context(), DescribeCluster, testCluster())
	if d.Allowed {
		t.Fatal("invalid principal allowed")
	}
	_ = checkFailure(t, err, StageEntityValidation)
}

// A failing forbid beside a valid permit must stop the operation, and every
// diagnostic must map to a known attachment.
func TestEvaluationFailures(t *testing.T) {
	bundle := grantBundle([]grant{
		{id: "role-read", policy: "read", content: readPermit, principal: roleARN},
		{id: "forbid-a", policy: "forbid", content: `forbid(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.account == "blocked" };`, principal: sessionARN},
		{id: "forbid-b", policy: "forbid", principal: roleARN},
	}, nil)
	for _, name := range []string{"evaluator error", "allow plus failing forbid", "deny plus diagnostics", "unknown diagnostic", "unknown reason", "unknown and known diagnostics"} {
		t.Run(name, func(t *testing.T) {
			a := fixtureAuthorizer(t, bundle)
			var native cedar.Diagnostic
			a.evaluate = evaluationScenario(t, name, &native)
			p, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
			if err != nil {
				t.Fatal(err)
			}
			before := p.provenance["attachment/forbid-b"]
			d, err := p.Check(t.Context(), DescribeCluster, testCluster())
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
}

func evaluationScenario(t *testing.T, name string, native *cedar.Diagnostic) evaluator {
	t.Helper()
	return func(set *cedar.PolicySet, entities cedar.EntityMap, req cedar.Request) (cedar.Decision, cedar.Diagnostic, error) {
		switch name {
		case "evaluator error":
			return cedar.Allow, cedar.Diagnostic{}, errors.New("engine failure")
		case "unknown diagnostic":
			return cedar.Allow, cedar.Diagnostic{Errors: []cedar.DiagnosticError{{PolicyID: "not-in-set", Message: "bad"}}}, nil
		case "unknown reason":
			return cedar.Allow, cedar.Diagnostic{Reasons: []cedar.DiagnosticReason{{PolicyID: "not-in-set"}}}, nil
		case "unknown and known diagnostics":
			return cedar.Allow, cedar.Diagnostic{Errors: []cedar.DiagnosticError{{PolicyID: "-unknown", Message: "bad"}, {PolicyID: "attachment/forbid-a", Message: "known"}}}, nil
		}
		// Remove the account attribute so forbid-a fails at runtime while the permit still allows.
		broken := entities[req.Resource]
		broken.Attributes = cedar.NewRecord(cedar.RecordMap{"region": cedar.String(region)})
		entities[req.Resource] = broken
		// Point forbid-b at another session so exactly one attachment fails.
		injected := cedar.NewPolicySet()
		for id, policy := range set.All() {
			injected.Add(id, policy)
		}
		injected.Add("attachment/forbid-b", parsePolicy(t, `forbid(principal == HyperFleet::Principal::"arn:aws:sts::123456789012:assumed-role/readers/session-b", action == HyperFleet::Action::"DescribeCluster", resource) when { resource.account == "blocked" };`))
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

func TestPreparedRequest(t *testing.T) {
	a := fixtureAuthorizer(t, serviceBundle())

	t.Run("context is frozen in UTC", func(t *testing.T) {
		frozen := fixtureAuthorizer(t, bundleFixture())
		var observed []cedar.Record
		frozen.evaluate = func(_ *cedar.PolicySet, _ cedar.EntityMap, req cedar.Request) (cedar.Decision, cedar.Diagnostic, error) {
			observed = append(observed, req.Context)
			return cedar.Deny, cedar.Diagnostic{}, nil
		}
		req := RequestContext{SourceIP: "192.0.2.5", UserAgent: "customer agent", RequestTime: time.Date(2026, 1, 4, 23, 30, 0, 0, time.FixedZone("caller", -3600))}
		p, err := frozen.Prepare(t.Context(), testIdentity(sessionARN), req)
		if err != nil {
			t.Fatal(err)
		}
		req.SourceIP, req.UserAgent, req.RequestTime = "forged", "changed", time.Now()
		for range 3 {
			if _, err := p.Check(t.Context(), DescribeCluster, testCluster()); err != nil {
				t.Fatal(err)
			}
		}
		want := cedar.NewRecord(cedar.RecordMap{"accountId": cedar.String(accountID), "region": cedar.String(region), "principalArn": cedar.String(sessionARN), "sourceIp": cedar.String("192.0.2.5"), "userAgent": cedar.String("customer agent"), "requestTime": cedar.NewRecord(cedar.RecordMap{"unixSeconds": cedar.Long(time.Date(2026, 1, 5, 0, 30, 0, 0, time.UTC).Unix()), "dayOfWeek": cedar.Long(1), "hour": cedar.Long(0)})})
		for i, c := range observed {
			if !reflect.DeepEqual(c, want) {
				t.Fatalf("check %d context = %v, want %v", i, c, want)
			}
		}
	})

	t.Run("compiled policies are shared but sets are request-local", func(t *testing.T) {
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
		}{{a.customer[0], first.policies, second.policies}, {a.service[0], first.servicePolicies, second.servicePolicies}} {
			id := cedar.PolicyID(domain.binding.DiagnosticID)
			if domain.first.Get(id) != domain.binding.policy || domain.second.Get(id) != domain.binding.policy {
				t.Fatal("preparation did not reuse the startup-compiled policy")
			}
			domain.first.Remove(id)
			if domain.second.Get(id) != domain.binding.policy {
				t.Fatal("per-request policy sets share mutable state")
			}
		}
		if d, err := second.Check(t.Context(), ListManagementClusters, Resource{Kind: ServiceCollection}); err != nil || !d.Allowed {
			t.Fatalf("reused service policy failed: %+v %v", d, err)
		}
	})

	t.Run("concurrent requests", func(t *testing.T) {
		for range 16 {
			t.Run("request", func(t *testing.T) {
				t.Parallel()
				for range 8 {
					if !decide(t, a, testIdentity(sessionARN), DescribeCluster, testCluster()).Allowed {
						t.Error("concurrent check denied")
					}
				}
			})
		}
	})
}

func TestResourceEntities(t *testing.T) {
	a, err := LoadConfig(configFile(t, encodeBundle(t, bundleFixture())), "us-west-2")
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		action   Action
		resource Resource
		uid      string
	}{
		{ListClusters, testCollection(), "123456789012/us-west-2/clusters"},
		{DescribeCluster, Resource{Kind: Cluster, ID: "cluster", AccountID: accountID}, "123456789012/us-west-2/cluster"},
		{DescribeNodePool, Resource{Kind: NodePool, ID: "workers", AccountID: accountID, ParentCluster: &ParentCluster{ID: "parent", AccountID: accountID}}, "123456789012/us-west-2/parent/workers"},
		{DescribeOIDCConfig, Resource{Kind: OIDCConfig, ID: "oidc", AccountID: accountID}, "123456789012/us-west-2/oidc"},
		{ListDNSReservations, Resource{Kind: Collection, CollectionKind: DNSReservation, AccountID: accountID}, "123456789012/us-west-2/dns_reservations"},
		{DescribeDNSReservation, Resource{Kind: DNSReservation, ID: "zone", AccountID: accountID}, "123456789012/us-west-2/zone"},
		{ListManagementClusters, Resource{Kind: ServiceCollection}, "us-west-2/management_clusters"},
		{DescribeManagementCluster, Resource{Kind: ManagementCluster, ID: "mc", AccountID: "210987654321", RegistrationRegion: "us-east-1"}, "us-west-2/mc"},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			entities, uid, err := p.resourceEntities(tc.action, tc.resource)
			if err != nil || string(uid.ID) != tc.uid {
				t.Fatalf("resource UID = %s, want %s: %v", uid, tc.uid, err)
			}
			if tc.resource.Kind == ManagementCluster {
				if got, _ := entities[uid].Attributes.Get("registrationRegion"); got != cedar.String("us-east-1") {
					t.Fatalf("registration region changed: %v", got)
				}
			}
			if tc.resource.Kind == NodePool {
				if _, exists := entities[entityUID("Cluster", "123456789012/us-west-2/parent")]; !exists {
					t.Fatal("parent did not use the service region")
				}
			}
		})
	}
}
