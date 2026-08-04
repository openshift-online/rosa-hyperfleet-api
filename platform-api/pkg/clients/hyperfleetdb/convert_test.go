package hyperfleetdb

import (
	"testing"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/types"
)

func TestPlatformCreateToNodePoolCR_SetsAccountLabel(t *testing.T) {
	req := &types.NodePoolCreateRequest{
		ClusterID: "test-cluster-id",
		Name:      "my-nodepool",
		Spec: &hyperfleetv1alpha1.NodePoolSpec{
			NodePool: hypershiftv1beta1.NodePoolSpec{
				Platform: hypershiftv1beta1.NodePoolPlatform{
					Type: hypershiftv1beta1.AWSPlatform,
					AWS: &hypershiftv1beta1.AWSNodePoolPlatform{
						InstanceType: "m6a.xlarge",
					},
				},
			},
		},
	}

	np, err := PlatformCreateToNodePoolCR("acct-123", "pool-uuid-1", req)
	if err != nil {
		t.Fatalf("PlatformCreateToNodePoolCR: %v", err)
	}

	if got := np.Labels["hyperfleet.io/account-id"]; got != "acct-123" {
		t.Errorf("account-id label = %q, want %q", got, "acct-123")
	}

	if got := np.Namespace; got != "cluster-test-cluster-id" {
		t.Errorf("namespace = %q, want %q", got, "cluster-test-cluster-id")
	}

	if got := np.Spec.AccountID; got != "acct-123" {
		t.Errorf("spec.AccountID = %q, want %q", got, "acct-123")
	}

	if got := np.Spec.InternalPoolID; got != "pool-uuid-1" {
		t.Errorf("spec.InternalPoolID = %q, want %q", got, "pool-uuid-1")
	}
}

func TestPlatformCreateToClusterCR_SetsAccountLabel(t *testing.T) {
	req := &types.ClusterCreateRequest{
		Name: "my-cluster",
		Spec: &hyperfleetv1alpha1.ClusterSpec{
			HostedCluster: hypershiftv1beta1.HostedClusterSpec{
				Platform: hypershiftv1beta1.PlatformSpec{
					Type: hypershiftv1beta1.AWSPlatform,
					AWS: &hypershiftv1beta1.AWSPlatformSpec{
						Region: "us-east-1",
					},
				},
			},
		},
	}

	cr, err := PlatformCreateToClusterCR("cluster-uuid", "acct-456", req)
	if err != nil {
		t.Fatalf("PlatformCreateToClusterCR: %v", err)
	}

	if got := cr.Labels["hyperfleet.io/account-id"]; got != "acct-456" {
		t.Errorf("account-id label = %q, want %q", got, "acct-456")
	}

	if got := cr.Spec.AccountID; got != "acct-456" {
		t.Errorf("spec.AccountID = %q, want %q", got, "acct-456")
	}

	if got := cr.Spec.InternalID; got != "cluster-uuid" {
		t.Errorf("spec.InternalID = %q, want %q", got, "cluster-uuid")
	}
}
