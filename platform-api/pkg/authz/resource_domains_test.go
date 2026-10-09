package authz

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	cedar "github.com/cedar-policy/cedar-go"
)

func TestCustomerOnlyDeniesService(t *testing.T) {
	b := bundleFixture()
	b["policies"].([]map[string]any)[0]["content"] = `permit(principal,action,resource);`
	a := fixtureAuthorizer(t, b)
	p, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		action   Action
		resource Resource
	}{
		{ListManagementClusters, Resource{Kind: ServiceCollection}},
		{DescribeManagementCluster, Resource{Kind: ManagementCluster, ID: "mc", AccountID: "210987654321", RegistrationRegion: "us-west-2"}},
		{CreateManagementCluster, Resource{Kind: ManagementCluster, ID: "mc", AccountID: "210987654321", RegistrationRegion: "us-west-2"}},
	} {
		d, err := p.Check(t.Context(), check.action, check.resource)
		if err != nil || d.Allowed {
			t.Fatalf("customer wildcard authorized service: %+v %v", d, err)
		}
	}
}

func TestServiceAuthorityIsDisjoint(t *testing.T) {
	mc := Resource{Kind: ManagementCluster, ID: "mc", AccountID: "210987654321", RegistrationRegion: "us-west-2"}
	for _, tc := range []struct {
		name, customer, service     string
		customerAllow, serviceAllow bool
	}{
		{"customer wildcard cannot grant service", `permit(principal,action,resource);`, "", true, false},
		{"customer service group cannot grant service", `permit(principal,action in HyperFleet::Action::"ServiceOperator",resource);`, "", false, false},
		{"customer AllActions excludes service", `permit(principal,action in HyperFleet::Action::"AllActions",resource);`, "", true, false},
		{"service wildcard cannot grant customer", "", `permit(principal,action,resource);`, false, true},
		{"customer forbid cannot override service permit", `forbid(principal,action,resource);`, `permit(principal,action in HyperFleet::Action::"ServiceOperator",resource);`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := serviceBundle()
			b["policies"].([]map[string]any)[0]["content"] = tc.customer
			if tc.customer == "" {
				b["policies"] = []any{}
				b["attachments"] = []any{}
			}
			b["serviceOperatorPolicies"].([]map[string]any)[0]["content"] = tc.service
			if tc.service == "" {
				b["serviceOperatorPolicies"] = []any{}
				b["serviceOperatorAttachments"] = []any{}
			}
			a := fixtureAuthorizer(t, b)
			p, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
			if err != nil {
				t.Fatal(err)
			}
			for _, check := range []struct {
				action   Action
				resource Resource
				want     bool
			}{{DescribeCluster, testCluster(), tc.customerAllow}, {CreateManagementCluster, mc, tc.serviceAllow}, {ListManagementClusters, Resource{Kind: ServiceCollection}, tc.serviceAllow}} {
				d, err := p.Check(t.Context(), check.action, check.resource)
				if err != nil || d.Allowed != check.want {
					t.Fatalf("%s want=%v decision=%+v err=%v", check.action, check.want, d, err)
				}
				if check.want && check.action == CreateManagementCluster && !strings.HasPrefix(d.Provenance[0].DiagnosticID, "service-operator/") {
					t.Fatal("lost service provenance")
				}
			}
		})
	}
}
func TestServiceBundleStrictValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"policies without attachments", func(b map[string]any) { delete(b, "serviceOperatorAttachments") }},
		{"attachments without policies", func(b map[string]any) { delete(b, "serviceOperatorPolicies") }},
		{"null policies", func(b map[string]any) { b["serviceOperatorPolicies"] = nil }},
		{"null attachments", func(b map[string]any) { b["serviceOperatorAttachments"] = nil }},
		{"map policies", func(b map[string]any) { b["serviceOperatorPolicies"] = map[string]any{} }},
		{"customer reference cannot resolve service", func(b map[string]any) { b["attachments"].([]map[string]any)[0]["policyID"] = "service" }},
		{"service reference cannot resolve customer", func(b map[string]any) { b["serviceOperatorAttachments"].([]map[string]any)[0]["policyID"] = "read" }},
		{"duplicate service policies", func(b map[string]any) {
			p := b["serviceOperatorPolicies"].([]map[string]any)
			b["serviceOperatorPolicies"] = append(p, p[0])
		}},
		{"duplicate service attachments", func(b map[string]any) {
			p := b["serviceOperatorAttachments"].([]map[string]any)
			b["serviceOperatorAttachments"] = append(p, p[0])
		}},
		{"cross domain role ambiguity", func(b map[string]any) {
			b["serviceOperatorAttachments"].([]map[string]any)[0]["principalARN"] = "arn:aws:iam::123456789012:role/other/readers"
		}},
		{"unattached malformed service policy", func(b map[string]any) {
			b["serviceOperatorAttachments"] = []any{}
			b["serviceOperatorPolicies"].([]map[string]any)[0]["content"] = "forbid("
		}},
		{"off region invalid service binding", func(b map[string]any) {
			a := b["serviceOperatorAttachments"].([]map[string]any)[0]
			a["scope"] = "regional"
			a["region"] = "us-west-2"
			a["principalARN"] = "bad"
		}},
		{"unknown nested service field", func(b map[string]any) { b["serviceOperatorPolicies"].([]map[string]any)[0]["extra"] = "bad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := serviceBundle()
			tc.mutate(b)
			if r, err := LoadConfig(configFile(t, encodeBundle(t, b)), region); err == nil || r != nil {
				t.Fatal("invalid service configuration accepted")
			}
		})
	}
	// IDs may coincide across domains; their references and provenance remain local.
	b := serviceBundle()
	b["serviceOperatorPolicies"].([]map[string]any)[0]["id"] = "read"
	a := b["serviceOperatorAttachments"].([]map[string]any)[0]
	a["policyID"] = "read"
	a["id"] = "role-read"
	r := fixtureAuthorizer(t, b)
	resolved, err := r.source.resolve(t.Context(), testPrincipal(t, sessionARN))
	if err != nil || len(resolved.customer) != 1 || len(resolved.serviceOperator) != 1 {
		t.Fatalf("domain-local IDs: %+v %v", resolved, err)
	}
}
func TestNodePoolIdentityAndTrustedParent(t *testing.T) {
	b := bundleFixture()
	b["policies"].([]map[string]any)[0]["content"] = `permit(principal, action == HyperFleet::Action::"DescribeNodePool", resource in HyperFleet::Cluster::"123456789012/us-east-1/parent-a");`
	a := fixtureAuthorizer(t, b)
	p, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"parent-a", "parent-b"} {
		np := Resource{Kind: NodePool, ID: "workers", AccountID: accountID, ParentCluster: &ParentCluster{ID: id, AccountID: accountID, Labels: map[string]string{"team": "blue"}}}
		entities, uid, err := p.resourceEntities(DescribeNodePool, np)
		if err != nil {
			t.Fatal(err)
		}
		if string(uid.ID) != accountID+"/"+region+"/"+id+"/workers" || len(entities) == 0 {
			t.Fatalf("lost parent-qualified identity: %s", uid)
		}
		d, err := p.Check(t.Context(), DescribeNodePool, np)
		if err != nil || d.Allowed != (id == "parent-a") {
			t.Fatalf("wrong parent decision: %+v %v", d, err)
		}
		np.ParentCluster.AccountID = "210987654321"
		if _, err := p.Check(t.Context(), DescribeNodePool, np); err == nil {
			t.Fatal("foreign parent accepted")
		}
	}
}
func TestFixedServiceRegion(t *testing.T) {
	b := serviceBundle()
	content := `permit(principal, action, resource) when { context.region == "us-west-2" && resource.region == "us-west-2" };`
	b["policies"].([]map[string]any)[0]["content"] = content
	b["serviceOperatorPolicies"].([]map[string]any)[0]["content"] = content
	a, err := LoadConfig(configFile(t, encodeBundle(t, b)), "us-west-2")
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
		{ListManagementClusters, Resource{Kind: ServiceCollection}, "us-west-2/management_clusters"},
		{DescribeManagementCluster, Resource{Kind: ManagementCluster, ID: "mc", AccountID: "210987654321", RegistrationRegion: "us-east-1"}, "us-west-2/mc"},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			d, err := p.Check(t.Context(), tc.action, tc.resource)
			if err != nil || !d.Allowed {
				t.Fatalf("constructor region was not used: %+v %v", d, err)
			}
			entities, resource, err := p.resourceEntities(tc.action, tc.resource)
			if err != nil || string(resource.ID) != tc.uid {
				t.Fatalf("resource UID = %s, want %s: %v", resource, tc.uid, err)
			}
			if tc.resource.Kind == ManagementCluster {
				registration, exists := entities[resource].Attributes.Get("registrationRegion")
				if !exists || registration != cedar.String("us-east-1") {
					t.Fatalf("registration region changed: %v", registration)
				}
			}
			if tc.resource.Kind == NodePool {
				parent := entityUID("Cluster", "123456789012/us-west-2/parent")
				if _, exists := entities[parent]; !exists {
					t.Fatal("parent did not use the service region")
				}
			}
		})
	}
}

func TestFrozenRequestContext(t *testing.T) {
	a := fixtureAuthorizer(t, bundleFixture())
	var observed []cedar.Record
	a.evaluate = func(set *cedar.PolicySet, entities cedar.EntityMap, req cedar.Request) (cedar.Decision, cedar.Diagnostic, error) {
		observed = append(observed, req.Context)
		return cedar.Deny, cedar.Diagnostic{}, nil
	}
	req := RequestContext{SourceIP: "192.0.2.5", UserAgent: "customer agent", RequestTime: time.Date(2026, 1, 4, 23, 30, 0, 0, time.FixedZone("caller", -3600))}
	p, err := a.Prepare(context.Background(), testIdentity(sessionARN), req)
	if err != nil {
		t.Fatal(err)
	}
	req.SourceIP = "forged"
	req.UserAgent = "changed"
	req.RequestTime = time.Now()
	for range 3 {
		if _, err := p.Check(t.Context(), DescribeCluster, testCluster()); err != nil {
			t.Fatal(err)
		}
	}
	want := cedar.NewRecord(cedar.RecordMap{"accountId": cedar.String(accountID), "region": cedar.String(region), "principalArn": cedar.String(sessionARN), "sourceIp": cedar.String("192.0.2.5"), "userAgent": cedar.String("customer agent"), "requestTime": cedar.NewRecord(cedar.RecordMap{"unixSeconds": cedar.Long(time.Date(2026, 1, 5, 0, 30, 0, 0, time.UTC).Unix()), "dayOfWeek": cedar.Long(1), "hour": cedar.Long(0)})})
	for i, c := range observed {
		if !reflect.DeepEqual(c, want) {
			t.Fatalf("check %d lost frozen UTC six-field context: %v want %v", i, c, want)
		}
	}
	if _, err := a.Prepare(t.Context(), testIdentity(sessionARN), RequestContext{}); err == nil {
		t.Fatal("missing clock accepted")
	}
}
func TestServiceRoleSessionForbid(t *testing.T) {
	b := serviceBundle()
	b["serviceOperatorPolicies"] = append(b["serviceOperatorPolicies"].([]map[string]any), map[string]any{"id": "block", "ownerAccountID": accountID, "content": `forbid(principal,action in HyperFleet::Action::"ServiceOperator",resource);`})
	b["serviceOperatorAttachments"] = append(b["serviceOperatorAttachments"].([]map[string]any), map[string]any{"id": "block", "policyID": "block", "principalARN": sessionARN, "scope": "global"})
	a := fixtureAuthorizer(t, b)
	for _, arn := range []string{sessionARN, otherSessionARN, "arn:aws:iam::123456789012:user/no-grants"} {
		t.Run(fmt.Sprint(arn), func(t *testing.T) {
			p, err := a.Prepare(t.Context(), testIdentity(arn), testRequestContext())
			if err != nil {
				t.Fatal(err)
			}
			d, err := p.Check(t.Context(), ListManagementClusters, Resource{Kind: ServiceCollection})
			if err != nil || d.Allowed != (arn == otherSessionARN) {
				t.Fatalf("exact-session/role grant: %+v %v", d, err)
			}
		})
	}
}
