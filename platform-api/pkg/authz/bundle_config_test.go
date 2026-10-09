package authz

import (
	"fmt"
	"strings"
	"testing"
)

func TestBundleVersionScalar(t *testing.T) {
	valid := encodeBundle(t, bundleFixture())
	for _, version := range []string{`"1"`, "1.0", "01", "0x1", "true", "0", "-1", "2"} {
		t.Run(version, func(t *testing.T) {
			content := strings.Replace(valid, "formatVersion: 1", "formatVersion: "+version, 1)
			if a, err := LoadConfig(configFile(t, content), region); err == nil || a != nil {
				t.Fatalf("accepted formatVersion %s", version)
			}
		})
	}
}

func TestServiceRecordsStrictShape(t *testing.T) {
	for _, records := range []string{"serviceOperatorPolicies", "serviceOperatorAttachments"} {
		for field := range serviceBundle()[records].([]map[string]any)[0] {
			for _, value := range []any{nil, ""} {
				t.Run(fmt.Sprintf("%s/%s/%v", records, field, value), func(t *testing.T) {
					bundle := serviceBundle()
					bundle[records].([]map[string]any)[0][field] = value
					if a, err := LoadConfig(configFile(t, encodeBundle(t, bundle)), region); err == nil || a != nil {
						t.Fatalf("accepted invalid %s.%s", records, field)
					}
				})
			}
		}
		t.Run(records+"/unknown field", func(t *testing.T) {
			bundle := serviceBundle()
			bundle[records].([]map[string]any)[0]["unknown"] = "value"
			if a, err := LoadConfig(configFile(t, encodeBundle(t, bundle)), region); err == nil || a != nil {
				t.Fatal("accepted unknown service record field")
			}
		})
	}
}

func TestServiceBundleForbid(t *testing.T) {
	bundle := serviceBundle()
	bundle["serviceOperatorPolicies"] = append(bundle["serviceOperatorPolicies"].([]map[string]any), map[string]any{
		"id": "forbid-description", "ownerAccountID": accountID,
		"content": `forbid(principal, action == HyperFleet::Action::"DescribeManagementCluster", resource);`,
	})
	bundle["serviceOperatorAttachments"] = append(bundle["serviceOperatorAttachments"].([]map[string]any), map[string]any{
		"id": "forbid-description", "policyID": "forbid-description", "principalARN": roleARN, "scope": "global",
	})
	a := fixtureAuthorizer(t, bundle)
	prepared, err := a.Prepare(t.Context(), testIdentity(sessionARN), testRequestContext())
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		action Action
		want   bool
	}{{CreateManagementCluster, true}, {DescribeManagementCluster, false}} {
		d, err := prepared.Check(t.Context(), check.action, Resource{Kind: ManagementCluster, ID: "mc", AccountID: "210987654321", RegistrationRegion: region})
		if err != nil || d.Allowed != check.want {
			t.Fatalf("%s allowed=%v want=%v err=%v", check.action, d.Allowed, check.want, err)
		}
	}
}
