package authz

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
	expast "github.com/cedar-policy/cedar-go/x/exp/ast"
	"github.com/cedar-policy/cedar-go/x/exp/schema"
	"github.com/cedar-policy/cedar-go/x/exp/schema/resolved"
	"github.com/cedar-policy/cedar-go/x/exp/schema/validate"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	listClusters    = "ListClusters"
	describeCluster = "DescribeCluster"
	accountID       = "123456789012"
	region          = "us-east-1"
	roleARN         = "arn:aws:iam::123456789012:role/platform/readers"
	sessionARN      = "arn:aws:sts::123456789012:assumed-role/readers/session-a"
	otherSessionARN = "arn:aws:sts::123456789012:assumed-role/readers/session-b"
	userARN         = "arn:aws:iam::123456789012:user/alice"
	labelPermit     = `@description("stored labels, not spec tags")
permit(principal, action == HyperFleet::Action::"DescribeCluster", resource is HyperFleet::Cluster)
when { resource.hasTag("example.com/team") && resource.getTag("example.com/team") == "blue \"team\"" }
unless { resource.hasTag("example.com/blocked") };`
)

func uid(kind, id string) cedar.EntityUID {
	return cedar.NewEntityUID(cedar.EntityType("HyperFleet::"+kind), cedar.String(id))
}

func gateSchema(t *testing.T) (*resolved.Schema, *validate.Validator) {
	t.Helper()
	content, err := os.ReadFile("testdata/hyperfleet.cedarschema")
	if err != nil {
		t.Fatal(err)
	}
	var parsed schema.Schema
	if err := parsed.UnmarshalCedar(content); err != nil {
		t.Fatalf("schema parsing: %v", err)
	}
	model, err := parsed.Resolve()
	if err != nil {
		t.Fatalf("schema resolution: %v", err)
	}
	return model, validate.New(model, validate.WithStrict())
}

func parsePolicy(t *testing.T, content string) *cedar.Policy {
	t.Helper()
	var policy cedar.Policy
	if err := policy.UnmarshalCedar([]byte(content)); err != nil {
		t.Fatalf("policy parsing: %v", err)
	}
	return &policy
}

func checkPolicy(t *testing.T, v *validate.Validator, id cedar.PolicyID, policy *cedar.Policy) {
	t.Helper()
	if err := v.Policy(string(id), (*expast.Policy)(policy.AST())); err != nil {
		t.Fatalf("policy schema validation for %s: %v", id, err)
	}
}

func policySet(t *testing.T, v *validate.Validator, policies map[cedar.PolicyID]*cedar.Policy) *cedar.PolicySet {
	t.Helper()
	set := cedar.NewPolicySet()
	for id, policy := range policies {
		checkPolicy(t, v, id, policy)
		set.Add(id, policy)
	}
	return set
}

func labelTags(meta metav1.ObjectMeta) cedar.Record {
	// Entity tags accept arbitrary metadata keys without a finite record schema.
	tags := make(cedar.RecordMap, len(meta.Labels))
	for key, value := range meta.Labels {
		tags[cedar.String(key)] = cedar.String(value)
	}
	return cedar.NewRecord(tags)
}

func gateEntities(model *resolved.Schema, labels map[string]string) cedar.EntityMap {
	owned := cedar.NewRecord(cedar.RecordMap{"account": cedar.String(accountID), "region": cedar.String(region)})
	principalAttrs := cedar.NewRecord(cedar.RecordMap{"account": cedar.String(accountID)})
	role := uid("Role", roleARN)
	collection := uid("Collection", accountID+"/"+region+"/clusters")
	cluster := uid("Cluster", accountID+"/"+region+"/4610b27e-cf07-4a18-b1a1-7e8f52e95701")
	entities := cedar.EntityMap{
		role:       {UID: role, Attributes: principalAttrs},
		collection: {UID: collection, Attributes: owned},
		cluster: {UID: cluster, Parents: cedar.NewEntityUIDSet(collection), Attributes: owned,
			Tags: labelTags(metav1.ObjectMeta{Labels: labels})},
	}
	for _, arn := range []string{sessionARN, otherSessionARN} {
		principal := uid("Principal", arn)
		entities[principal] = cedar.Entity{UID: principal, Parents: cedar.NewEntityUIDSet(role), Attributes: principalAttrs}
	}
	user := uid("Principal", userARN)
	entities[user] = cedar.Entity{UID: user, Attributes: principalAttrs}
	// Schema action declarations do not populate the authorizer's entity map.
	for id, action := range model.Actions {
		entities[id] = action.Entity
	}
	return entities
}

func gateRequest(action string, principal cedar.EntityUID) cedar.Request {
	resource := uid("Cluster", accountID+"/"+region+"/4610b27e-cf07-4a18-b1a1-7e8f52e95701")
	if action == listClusters {
		resource = uid("Collection", accountID+"/"+region+"/clusters")
	}
	return cedar.Request{Principal: principal, Action: uid("Action", action), Resource: resource, Context: cedar.NewRecord(cedar.RecordMap{
		"region": cedar.String(region), "accountId": cedar.String(accountID), "principalArn": principal.ID,
		"sourceIp": cedar.String("192.0.2.1"), "userAgent": cedar.String("authz-test"),
		"requestTime": cedar.NewRecord(cedar.RecordMap{"unixSeconds": cedar.Long(1), "dayOfWeek": cedar.Long(1), "hour": cedar.Long(12)}),
	})}
}

func checkDecision(t *testing.T, set *cedar.PolicySet, entities cedar.EntityMap, req cedar.Request, want cedar.Decision, reasons ...cedar.PolicyID) {
	t.Helper()
	decision, diagnostics := cedar.Authorize(set, entities, req)
	if decision != want || len(diagnostics.Errors) != 0 {
		t.Fatalf("want %s with no errors, got %s, diagnostics %+v", want, decision, diagnostics)
	}
	got := make([]cedar.PolicyID, 0, len(diagnostics.Reasons))
	for _, reason := range diagnostics.Reasons {
		got = append(got, reason.PolicyID)
	}
	slices.Sort(got)
	slices.Sort(reasons)
	if !slices.Equal(got, reasons) {
		t.Fatalf("determining policy IDs: want %v, got %v", reasons, got)
	}
}

func TestSchemaValidation(t *testing.T) {
	model, v := gateSchema(t)
	if len(model.Entities) != 8 || len(model.Actions) != 27 {
		t.Fatalf("resolved declarations: %+v", model)
	}
	cluster := model.Entities["HyperFleet::Cluster"]
	if !reflect.DeepEqual(cluster.Tags, resolved.StringType{}) || !slices.Equal(cluster.ParentTypes, []cedar.EntityType{"HyperFleet::Collection"}) {
		t.Fatalf("resolved Cluster tag type or ancestry: %+v", cluster)
	}
	if !reflect.DeepEqual(cluster.Shape["account"].Type, resolved.StringType{}) {
		t.Fatalf("AccountID alias did not resolve: %+v", cluster.Shape)
	}
	if !model.Actions[uid("Action", describeCluster)].Entity.Parents.Contains(uid("Action", "ReadOnly")) {
		t.Fatal("action group did not resolve")
	}
	entities := gateEntities(model, map[string]string{"example.com/team": `blue "team"`})
	if err := v.Entities(entities); err != nil {
		t.Fatalf("valid entities: %v", err)
	}
	for _, action := range []string{listClusters, describeCluster} {
		if err := v.Request(gateRequest(action, uid("Principal", sessionARN))); err != nil {
			t.Fatalf("valid request: %v", err)
		}
	}
	checkPolicy(t, v, "label-permit", parsePolicy(t, labelPermit))
	checkPolicy(t, v, "trusted-context", parsePolicy(t, `permit(principal, action in HyperFleet::Action::"ReadOnly", resource) when { context.region == "us-east-1" && context.accountId == "123456789012" && context.principalArn == "caller" };`))
	for _, key := range []cedar.String{"region", "accountId", "principalArn"} {
		t.Run("required context "+string(key), func(t *testing.T) {
			req := gateRequest(describeCluster, uid("Principal", sessionARN))
			attrs := cedar.RecordMap{"region": cedar.String(region), "accountId": cedar.String(accountID), "principalArn": cedar.String(sessionARN)}
			delete(attrs, key)
			req.Context = cedar.NewRecord(attrs)
			if err := v.Request(req); err == nil {
				t.Fatal("missing context attribute accepted")
			}
			attrs[key] = cedar.Long(1)
			req.Context = cedar.NewRecord(attrs)
			if err := v.Request(req); err == nil {
				t.Fatal("wrong context attribute type accepted")
			}
		})
	}

	t.Run("syntax is not resolution", func(t *testing.T) {
		var parsed schema.Schema
		if err := parsed.UnmarshalCedar([]byte(`entity Broken { value: MissingType };`)); err != nil {
			t.Fatalf("syntactically valid schema: %v", err)
		}
		if _, err := parsed.Resolve(); err == nil {
			t.Fatal("unresolved schema reference was accepted")
		}
	})

	for name, content := range map[string]string{
		"unknown attribute":     `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.unknown == "x" };`,
		"wrong attribute type":  `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.account == 12 };`,
		"unknown action":        `permit(principal, action == HyperFleet::Action::"UnimplementedAction", resource);`,
		"wrong action resource": `permit(principal, action == HyperFleet::Action::"ListClusters", resource is HyperFleet::Cluster);`,
		"unguarded tag":         `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.getTag("example.com/team") == "blue" };`,
	} {
		t.Run(name, func(t *testing.T) {
			policy := parsePolicy(t, content)
			err := v.Policy(name, (*expast.Policy)(policy.AST()))
			if err == nil {
				t.Fatal("syntactically valid schema-invalid policy accepted")
			}
			t.Logf("native schema rejection: %v", err)
		})
	}

	t.Run("entity and request validation", func(t *testing.T) {
		req := gateRequest(describeCluster, uid("Principal", sessionARN))
		bad := entities[req.Resource]
		bad.Tags = cedar.NewRecord(cedar.RecordMap{"example.com/team": cedar.Long(42)})
		if err := v.Entity(bad); err == nil {
			t.Fatal("non-string label tag accepted")
		}
		bad = entities[req.Resource]
		bad.Attributes = cedar.NewRecord(nil)
		if err := v.Entity(bad); err == nil {
			t.Fatal("missing required account/region accepted")
		}
		bad = entities[gateRequest(listClusters, req.Principal).Resource]
		bad.Tags = labelTags(metav1.ObjectMeta{Labels: map[string]string{"example.com/team": "blue"}})
		if err := v.Entity(bad); err == nil {
			t.Fatal("collection with object labels accepted")
		}
		req.Resource = gateRequest(listClusters, req.Principal).Resource
		if err := v.Request(req); err == nil {
			t.Fatal("DescribeCluster request on Collection accepted")
		}
	})
}

func TestEntityHierarchy(t *testing.T) {
	model, v := gateSchema(t)
	content := `permit(principal in HyperFleet::Role::"arn:aws:iam::123456789012:role/platform/readers", action in HyperFleet::Action::"ReadOnly", resource in HyperFleet::Collection::"123456789012/us-east-1/clusters");`
	set := policySet(t, v, map[cedar.PolicyID]*cedar.Policy{"hierarchy": parsePolicy(t, content)})
	for _, action := range []string{listClusters, describeCluster} {
		t.Run(action, func(t *testing.T) {
			entities := gateEntities(model, nil)
			req := gateRequest(action, uid("Principal", sessionARN))
			if err := v.Entities(entities); err != nil {
				t.Fatal(err)
			}
			if err := v.Request(req); err != nil {
				t.Fatal(err)
			}
			checkDecision(t, set, entities, req, cedar.Allow, "hierarchy")
			principal := entities[req.Principal]
			principal.Parents = cedar.NewEntityUIDSet()
			entities[req.Principal] = principal
			checkDecision(t, set, entities, req, cedar.Deny)
			entities = gateEntities(model, nil)
			actionEntity := entities[req.Action]
			actionEntity.Parents = cedar.NewEntityUIDSet()
			entities[req.Action] = actionEntity
			if err := v.Entity(actionEntity); err == nil {
				t.Fatal("action missing declared group was accepted by entity validator")
			}
			checkDecision(t, set, entities, req, cedar.Deny)
			if action == describeCluster {
				entities = gateEntities(model, nil)
				resource := entities[req.Resource]
				resource.Parents = cedar.NewEntityUIDSet()
				entities[req.Resource] = resource
				checkDecision(t, set, entities, req, cedar.Deny)
			}
		})
	}
	t.Run("ancestry does not inherit attributes", func(t *testing.T) {
		entities := gateEntities(model, nil)
		req := gateRequest(describeCluster, uid("Principal", sessionARN))
		resource := entities[req.Resource]
		resource.Attributes = cedar.NewRecord(nil)
		if err := v.Entity(resource); err == nil {
			t.Fatal("Cluster incorrectly inherited required ownership from Collection")
		}
	})
}

func TestMetadataLabelTags(t *testing.T) {
	model, v := gateSchema(t)
	set := policySet(t, v, map[cedar.PolicyID]*cedar.Policy{"labels": parsePolicy(t, labelPermit)})
	req := gateRequest(describeCluster, uid("Principal", sessionARN))
	for _, tc := range []struct {
		name   string
		labels map[string]string
		want   cedar.Decision
	}{
		{"quoted value", map[string]string{"example.com/team": `blue "team"`, "new.example.net/arbitrary": "anything"}, cedar.Allow},
		{"different value", map[string]string{"example.com/team": "red"}, cedar.Deny},
		{"missing key", map[string]string{"example.com/other": "blue"}, cedar.Deny},
		{"empty map", map[string]string{}, cedar.Deny},
		{"nil map", nil, cedar.Deny},
		{"unless preserved", map[string]string{"example.com/team": `blue "team"`, "example.com/blocked": ""}, cedar.Deny},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entities := gateEntities(model, tc.labels)
			if err := v.Entities(entities); err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(entities)
			if err != nil {
				t.Fatal(err)
			}
			var decoded cedar.EntityMap
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if err := v.Entities(decoded); err != nil {
				t.Fatal(err)
			}
			if tc.want == cedar.Allow {
				checkDecision(t, set, decoded, req, tc.want, "labels")
			} else {
				checkDecision(t, set, decoded, req, tc.want)
			}
		})
	}
}

func TestForbidsAndDiagnostics(t *testing.T) {
	model, v := gateSchema(t)
	entities := gateEntities(model, map[string]string{"example.com/team": `blue "team"`})
	permit := parsePolicy(t, labelPermit)
	forbidContent := `@description("exact session prohibition") forbid(principal == HyperFleet::Principal::"arn:aws:sts::123456789012:assumed-role/readers/session-a", action == HyperFleet::Action::"DescribeCluster", resource);`
	forbid := parsePolicy(t, forbidContent)
	set := policySet(t, v, map[cedar.PolicyID]*cedar.Policy{"role-permit": permit, "session-forbid": forbid})
	checkDecision(t, set, entities, gateRequest(describeCluster, uid("Principal", sessionARN)), cedar.Deny, "session-forbid")
	checkDecision(t, set, entities, gateRequest(describeCluster, uid("Principal", otherSessionARN)), cedar.Allow, "role-permit")
	checkDecision(t, cedar.NewPolicySet(), entities, gateRequest(describeCluster, uid("Principal", sessionARN)), cedar.Deny)

	t.Run("permit plus failing forbid", func(t *testing.T) {
		failingContent := `forbid(principal == HyperFleet::Principal::"arn:aws:sts::123456789012:assumed-role/readers/session-a", action == HyperFleet::Action::"DescribeCluster", resource) when { resource.account == "blocked" };`
		failing := parsePolicy(t, failingContent)
		unaffected := parsePolicy(t, strings.Replace(failingContent, "session-a", "session-b", 1))
		set := policySet(t, v, map[cedar.PolicyID]*cedar.Policy{
			"permit":              parsePolicy(t, `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource);`),
			"forbid-attachment-a": failing, "forbid-attachment-b": unaffected,
		})
		req := gateRequest(describeCluster, uid("Principal", sessionARN))
		brokenEntities := gateEntities(model, nil)
		resource := brokenEntities[req.Resource]
		resource.Attributes = cedar.NewRecord(cedar.RecordMap{"region": cedar.String(region)})
		brokenEntities[req.Resource] = resource
		if err := v.Entities(brokenEntities); err == nil {
			t.Fatal("entity validation did not catch injected missing ownership")
		}
		decision, diagnostics := cedar.Authorize(set, brokenEntities, req)
		if decision != cedar.Allow || len(diagnostics.Reasons) != 1 || diagnostics.Reasons[0].PolicyID != "permit" {
			t.Fatalf("native permit result changed: %s, %+v", decision, diagnostics)
		}
		if len(diagnostics.Errors) != 1 || diagnostics.Errors[0].PolicyID != "forbid-attachment-a" || !strings.Contains(diagnostics.Errors[0].Message, "account") || diagnostics.Errors[0].Position.Line == 0 {
			t.Fatalf("native diagnostic lost attachment ID, message, or source position: %+v", diagnostics)
		}
		t.Logf("native decision %s must not be accepted by future strict evaluator: %+v", decision, diagnostics)
	})
}
