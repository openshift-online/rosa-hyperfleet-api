package authz

import (
	"reflect"
	"slices"
	"testing"

	cedar "github.com/cedar-policy/cedar-go"
	expast "github.com/cedar-policy/cedar-go/x/exp/ast"
	"github.com/cedar-policy/cedar-go/x/exp/schema"
	"github.com/cedar-policy/cedar-go/x/exp/schema/resolved"
)

func parsePolicy(t *testing.T, content string) *cedar.Policy {
	t.Helper()
	var policy cedar.Policy
	if err := policy.UnmarshalCedar([]byte(content)); err != nil {
		t.Fatalf("policy parsing: %v", err)
	}
	return &policy
}

// schemaEntities builds a valid hand-made entity graph for one Cluster.
func schemaEntities(model *resolved.Schema) cedar.EntityMap {
	owned := cedar.NewRecord(cedar.RecordMap{"account": cedar.String(accountID), "region": cedar.String(region)})
	principalAttrs := cedar.NewRecord(cedar.RecordMap{"account": cedar.String(accountID)})
	role := entityUID("Role", roleARN)
	principal := entityUID("Principal", sessionARN)
	collection := entityUID("Collection", accountID+"/"+region+"/clusters")
	cluster := entityUID("Cluster", accountID+"/"+region+"/c")
	entities := cedar.EntityMap{
		role:       {UID: role, Attributes: principalAttrs},
		principal:  {UID: principal, Parents: cedar.NewEntityUIDSet(role), Attributes: principalAttrs},
		collection: {UID: collection, Attributes: owned},
		cluster:    {UID: cluster, Parents: cedar.NewEntityUIDSet(collection), Attributes: owned, Tags: resourceTags(map[string]string{"example.com/team": "blue"})},
	}
	for id, action := range model.Actions {
		entities[id] = action.Entity
	}
	return entities
}

func schemaRequest(action, resource cedar.EntityUID) cedar.Request {
	return cedar.Request{Principal: entityUID("Principal", sessionARN), Action: action, Resource: resource, Context: cedar.NewRecord(cedar.RecordMap{
		"region": cedar.String(region), "accountId": cedar.String(accountID), "principalArn": cedar.String(sessionARN),
		"sourceIp": cedar.String("192.0.2.1"), "userAgent": cedar.String("authz-test"),
		"requestTime": cedar.NewRecord(cedar.RecordMap{"unixSeconds": cedar.Long(1), "dayOfWeek": cedar.Long(1), "hour": cedar.Long(12)}),
	})}
}

func TestSchemaValidation(t *testing.T) {
	model, v, err := loadSchema()
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Entities) != 9 || len(model.Actions) != 32 {
		t.Fatalf("resolved declarations: %d entities, %d actions", len(model.Entities), len(model.Actions))
	}
	cluster := model.Entities["HyperFleet::Cluster"]
	if !reflect.DeepEqual(cluster.Tags, resolved.StringType{}) || !slices.Equal(cluster.ParentTypes, []cedar.EntityType{"HyperFleet::Collection"}) || !reflect.DeepEqual(cluster.Shape["account"].Type, resolved.StringType{}) {
		t.Fatalf("resolved Cluster tags, ancestry, or account alias: %+v", cluster)
	}

	t.Run("implemented actions and groups", func(t *testing.T) {
		for _, action := range []string{"ListClusters", "DescribeCluster", "CreateCluster", "UpdateCluster", "UpdateClusterConfig", "UpdateClusterVersion", "DeleteCluster", "ListNodePools", "DescribeNodePool", "CreateNodePool", "UpdateNodePool", "ScaleNodePool", "UpdateNodePoolVersion", "DeleteNodePool", "ListOIDCConfigs", "DescribeOIDCConfig", "CreateOIDCConfig", "DeleteOIDCConfig", "ListDNSReservations", "DescribeDNSReservation", "CreateDNSReservation", "DeleteDNSReservation", "ListManagementClusters", "DescribeManagementCluster", "CreateManagementCluster", "AllActions", "ReadOnly", "ClusterAdmin", "NodePoolAdmin", "OIDCConfigAdmin", "DNSReservationAdmin", "ServiceOperator"} {
			if _, err := checkedPolicy(`permit(principal, action in HyperFleet::Action::"`+action+`", resource);`, action, v); err != nil {
				t.Errorf("%s: %v", action, err)
			}
		}
	})

	t.Run("policies", func(t *testing.T) {
		for _, content := range []string{labelPermit, `permit(principal, action in HyperFleet::Action::"ReadOnly", resource) when { context.region == "us-east-1" && context.accountId == "123456789012" && context.principalArn == "caller" };`} {
			if err := v.Policy("valid", (*expast.Policy)(parsePolicy(t, content).AST())); err != nil {
				t.Fatalf("valid policy rejected: %v", err)
			}
		}
		for name, content := range map[string]string{
			"unknown attribute":     `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.unknown == "x" };`,
			"wrong attribute type":  `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.account == 12 };`,
			"unknown action":        `permit(principal, action == HyperFleet::Action::"UnimplementedAction", resource);`,
			"wrong action resource": `permit(principal, action == HyperFleet::Action::"ListClusters", resource is HyperFleet::Cluster);`,
			"unguarded tag":         `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource) when { resource.getTag("example.com/team") == "blue" };`,
		} {
			if err := v.Policy(name, (*expast.Policy)(parsePolicy(t, content).AST())); err == nil {
				t.Errorf("%s: schema-invalid policy accepted", name)
			}
		}
		var parsed schema.Schema
		if err := parsed.UnmarshalCedar([]byte(`entity Broken { value: MissingType };`)); err != nil {
			t.Fatal(err)
		}
		if _, err := parsed.Resolve(); err == nil {
			t.Fatal("unresolved schema reference accepted")
		}
	})

	t.Run("entities and requests", func(t *testing.T) {
		entities := schemaEntities(model)
		if err := v.Entities(entities); err != nil {
			t.Fatalf("valid entities: %v", err)
		}
		collection := entityUID("Collection", accountID+"/"+region+"/clusters")
		cluster := entityUID("Cluster", accountID+"/"+region+"/c")
		describe := schemaRequest(entityUID("Action", "DescribeCluster"), cluster)
		for _, req := range []cedar.Request{describe, schemaRequest(entityUID("Action", "ListClusters"), collection)} {
			if err := v.Request(req); err != nil {
				t.Fatalf("valid request: %v", err)
			}
		}
		for _, key := range []cedar.String{"region", "accountId", "principalArn"} {
			attrs := cedar.RecordMap{"region": cedar.String(region), "accountId": cedar.String(accountID), "principalArn": cedar.String(sessionARN)}
			delete(attrs, key)
			req := describe
			req.Context = cedar.NewRecord(attrs)
			if err := v.Request(req); err == nil {
				t.Errorf("missing context %s accepted", key)
			}
			attrs[key] = cedar.Long(1)
			req.Context = cedar.NewRecord(attrs)
			if err := v.Request(req); err == nil {
				t.Errorf("wrong context type %s accepted", key)
			}
		}
		badTag := entities[cluster]
		badTag.Tags = cedar.NewRecord(cedar.RecordMap{"example.com/team": cedar.Long(42)})
		noOwner := entities[cluster]
		noOwner.Attributes = cedar.NewRecord(nil)
		taggedCollection := entities[collection]
		taggedCollection.Tags = resourceTags(map[string]string{"example.com/team": "blue"})
		for name, entity := range map[string]cedar.Entity{"non-string tag": badTag, "missing ownership despite parent": noOwner, "collection with labels": taggedCollection} {
			if err := v.Entity(entity); err == nil {
				t.Errorf("%s accepted", name)
			}
		}
		wrongResource := describe
		wrongResource.Resource = collection
		if err := v.Request(wrongResource); err == nil {
			t.Fatal("DescribeCluster on a Collection accepted")
		}
	})
}
