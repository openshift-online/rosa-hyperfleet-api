package authz

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestAttachmentApplicability(t *testing.T) {
	bundle := bundleFixture()
	bundle["policies"] = append(bundle["policies"].([]map[string]any),
		map[string]any{"id": "block", "ownerAccountID": accountID, "content": `forbid(principal, action in HyperFleet::Action::"ReadOnly", resource);`},
	)
	bundle["attachments"] = append(bundle["attachments"].([]map[string]any),
		map[string]any{"id": "user-read", "policyID": "read", "principalARN": userARN, "scope": "global"},
		map[string]any{"id": "session-block", "policyID": "block", "principalARN": sessionARN, "scope": "global"},
	)
	authorizer, err := LoadConfig(configFile(t, encodeBundle(t, bundle)), region)
	if err != nil {
		t.Fatalf("load attachments: %v", errors.Unwrap(err))
	}
	for _, tc := range []struct {
		name, arn   string
		isAllowed   bool
		attachments []string
	}{
		{"blocked session", sessionARN, false, []string{"session-block"}},
		{"other role session", otherSessionARN, true, []string{"role-read"}},
		{"exact user", userARN, true, []string{"user-read"}},
		{"unrelated user", "arn:aws:iam::123456789012:user/bob", false, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for range 8 {
				prepared, err := authorizer.Prepare(context.Background(), testIdentity(tc.arn), testRequestContext())
				if err != nil {
					t.Fatal(err)
				}
				decision, err := prepared.Check(context.Background(), DescribeCluster, testCluster())
				if err != nil || decision.Allowed != tc.isAllowed {
					t.Fatalf("decision = %+v, error = %v, want allowed %t", decision, err, tc.isAllowed)
				}
				attachments := make([]string, 0, len(decision.Provenance))
				for _, provenance := range decision.Provenance {
					attachments = append(attachments, provenance.AttachmentID)
				}
				if !slices.Equal(attachments, tc.attachments) {
					t.Fatalf("attachments = %v, want %v", attachments, tc.attachments)
				}
			}
		})
	}
}

func TestAttachmentRestrictions(t *testing.T) {
	bundle := bundleFixture()
	bundle["policies"].([]map[string]any)[0]["content"] = `@description("retain authored restrictions")
permit(principal == HyperFleet::Principal::"arn:aws:sts::123456789012:assumed-role/readers/session-a", action == HyperFleet::Action::"DescribeCluster", resource in HyperFleet::Collection::"123456789012/us-east-1/clusters")
when { resource.hasTag("example.com/team") && resource.getTag("example.com/team") == "blue \"team\"" }
unless { resource.hasTag("example.com/blocked") };`
	authorizer := fixtureAuthorizer(t, bundle)
	for _, tc := range []struct {
		name, arn string
		action    Action
		labels    map[string]string
		isAllowed bool
	}{
		{"matching session", sessionARN, DescribeCluster, map[string]string{"example.com/team": `blue "team"`}, true},
		{"other session", otherSessionARN, DescribeCluster, map[string]string{"example.com/team": `blue "team"`}, false},
		{"wrong action", sessionARN, UpdateCluster, map[string]string{"example.com/team": `blue "team"`}, false},
		{"missing label", sessionARN, DescribeCluster, nil, false},
		{"different label", sessionARN, DescribeCluster, map[string]string{"example.com/team": "red"}, false},
		{"unless blocks", sessionARN, DescribeCluster, map[string]string{"example.com/team": `blue "team"`, "example.com/blocked": "yes"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prepared, err := authorizer.Prepare(context.Background(), testIdentity(tc.arn), testRequestContext())
			if err != nil {
				t.Fatal(err)
			}
			resource := testCluster()
			resource.Labels = tc.labels
			decision, err := prepared.Check(context.Background(), tc.action, resource)
			if err != nil || decision.Allowed != tc.isAllowed {
				t.Fatalf("decision = %+v, error = %v, want allowed %t", decision, err, tc.isAllowed)
			}
		})
	}
}
