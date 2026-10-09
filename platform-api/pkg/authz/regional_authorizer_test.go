package authz_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
)

func TestRegionalAuthorizer(t *testing.T) {
	const bundle = `formatVersion: 1
registeredAccounts: ["123456789012"]
policies:
  - id: regional-read
    ownerAccountID: "123456789012"
    content: |
      permit(principal, action == HyperFleet::Action::"DescribeCluster", resource)
      when { context.region == "us-east-1" && resource.region == "us-east-1" && resource in HyperFleet::Collection::"123456789012/us-east-1/clusters" };
attachments:
  - id: alice-east
    policyID: regional-read
    principalARN: arn:aws:iam::123456789012:user/alice
    scope: regional
    region: us-east-1
`
	path := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(path, []byte(bundle), 0600); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", "invalid"} {
		if authorizer, err := authz.LoadConfig(path, invalid); err == nil || authorizer != nil {
			t.Fatalf("invalid service region %q accepted", invalid)
		}
	}
	east, err := authz.LoadConfig(path, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	west, err := authz.LoadConfig(path, "us-west-2")
	if err != nil {
		t.Fatal(err)
	}
	if !east.IsAccountRegistered(t.Context(), "123456789012") || east.IsAccountRegistered(t.Context(), "210987654321") {
		t.Fatal("enrollment is not exact membership")
	}
	if err := os.WriteFile(path, []byte("invalid replacement"), 0600); err != nil {
		t.Fatal(err)
	}

	var callers sync.WaitGroup
	for range 8 {
		for _, tc := range []struct {
			name       string
			authorizer *authz.Authorizer
			caller     string
			allowed    bool
		}{
			{"east Alice", east, "alice", true},
			{"west Alice", west, "alice", false},
			{"east Bob", east, "bob", false},
		} {
			callers.Add(1)
			go func() {
				defer callers.Done()
				prepared, err := tc.authorizer.Prepare(context.Background(), authz.Identity{
					AccountID: "123456789012", CallerARN: "arn:aws:iam::123456789012:user/" + tc.caller,
				}, authz.RequestContext{RequestTime: time.Unix(1, 0)})
				if err != nil {
					t.Errorf("%s preparation failed: %v", tc.name, err)
					return
				}
				for range 2 {
					decision, err := prepared.Check(context.Background(), authz.DescribeCluster, authz.Resource{
						Kind: authz.Cluster, ID: "cluster-a", AccountID: "123456789012",
					})
					if err != nil || decision.Allowed != tc.allowed {
						t.Errorf("%s want allowed %v, got %+v, %v", tc.name, tc.allowed, decision, err)
					}
					if tc.allowed && (len(decision.Provenance) != 1 || decision.Provenance[0].AttachmentID != "alice-east" || decision.Provenance[0].Region != "us-east-1") {
						t.Errorf("%s lost regional attachment provenance: %+v", tc.name, decision)
					}
				}
			}()
		}
	}
	callers.Wait()
}
