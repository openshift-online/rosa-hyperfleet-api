package authz

import (
	"testing"
	"time"
)

func testRequestContext() RequestContext {
	return RequestContext{SourceIP: "192.0.2.1", UserAgent: "authz-test", RequestTime: time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)}
}

func TestImplementedActionSchema(t *testing.T) {
	_, v, err := loadSchema()
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"CreateCluster", "UpdateCluster", "UpdateClusterConfig", "UpdateClusterVersion", "DeleteCluster", "ListNodePools", "DescribeNodePool", "CreateNodePool", "UpdateNodePool", "ScaleNodePool", "UpdateNodePoolVersion", "DeleteNodePool", "ListOIDCConfigs", "DescribeOIDCConfig", "CreateOIDCConfig", "DeleteOIDCConfig", "ListManagementClusters", "DescribeManagementCluster", "CreateManagementCluster", "AllActions", "ClusterAdmin", "NodePoolAdmin", "OIDCConfigAdmin", "ServiceOperator"} {
		t.Run(action, func(t *testing.T) {
			if _, err := checkedPolicy(`permit(principal, action in HyperFleet::Action::"`+action+`", resource);`, "action", v); err != nil {
				t.Fatalf("implemented action missing: %v", err)
			}
		})
	}
}

func serviceBundle() map[string]any {
	b := bundleFixture()
	b["serviceOperatorPolicies"] = []map[string]any{{"id": "service", "ownerAccountID": accountID, "content": `permit(principal, action in HyperFleet::Action::"ServiceOperator", resource);`}}
	b["serviceOperatorAttachments"] = []map[string]any{{"id": "service", "policyID": "service", "principalARN": roleARN, "scope": "global"}}
	return b
}
