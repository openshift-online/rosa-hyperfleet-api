package hyperfleetdb

import (
	"testing"
	"time"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/types"
)

func TestPlatformCreateToNodePoolCR_SetsAccountLabel(t *testing.T) {
	req := &types.NodePoolCreateRequest{
		ClusterID: "test-cluster-id",
		Name:      "my-nodepool",
		Spec: &hyperfleetv1alpha1.NodePoolSpec{
			NodePool: hyperfleetv1alpha1.NodePoolSpecPassthrough{
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

func TestNodePoolCRToPlatform_AutoRepair(t *testing.T) {
	tests := []struct {
		name       string
		autoRepair *bool
		wantTop    *bool
		wantMgmt   bool
	}{
		{
			name:       "nil defaults to true in passthrough",
			autoRepair: nil,
			wantTop:    nil,
			wantMgmt:   true,
		},
		{
			name:       "explicit true propagates to passthrough",
			autoRepair: ptr.To(true),
			wantTop:    ptr.To(true),
			wantMgmt:   true,
		},
		{
			name:       "explicit false propagates to passthrough",
			autoRepair: ptr.To(false),
			wantTop:    ptr.To(false),
			wantMgmt:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := &hyperfleetv1alpha1.NodePool{
				Spec: hyperfleetv1alpha1.NodePoolSpec{
					AutoRepair: tt.autoRepair,
				},
			}
			np := NodePoolCRToPlatform(cr)

			if tt.wantTop == nil {
				if np.Spec.AutoRepair != nil {
					t.Errorf("Spec.AutoRepair = %v, want nil", *np.Spec.AutoRepair)
				}
			} else {
				if np.Spec.AutoRepair == nil {
					t.Fatalf("Spec.AutoRepair = nil, want %v", *tt.wantTop)
				}
				if *np.Spec.AutoRepair != *tt.wantTop {
					t.Errorf("Spec.AutoRepair = %v, want %v", *np.Spec.AutoRepair, *tt.wantTop)
				}
			}

			if np.Spec.NodePool.Management.AutoRepair != tt.wantMgmt {
				t.Errorf("Spec.NodePool.Management.AutoRepair = %v, want %v",
					np.Spec.NodePool.Management.AutoRepair, tt.wantMgmt)
			}
		})
	}
}

func TestNodePoolCRToPlatform_Labels(t *testing.T) {
	labels := map[string]string{"env": "staging", "team": "platform"}

	cr := &hyperfleetv1alpha1.NodePool{
		Spec: hyperfleetv1alpha1.NodePoolSpec{
			Labels: labels,
		},
	}
	np := NodePoolCRToPlatform(cr)

	if len(np.Spec.NodePool.NodeLabels) != len(labels) {
		t.Fatalf("NodeLabels len = %d, want %d", len(np.Spec.NodePool.NodeLabels), len(labels))
	}
	for k, v := range labels {
		if np.Spec.NodePool.NodeLabels[k] != v {
			t.Errorf("NodeLabels[%q] = %q, want %q", k, np.Spec.NodePool.NodeLabels[k], v)
		}
	}
}

func TestNodePoolCRToPlatform_LabelsEmpty(t *testing.T) {
	cr := &hyperfleetv1alpha1.NodePool{
		Spec: hyperfleetv1alpha1.NodePoolSpec{
			Labels: nil,
			NodePool: hyperfleetv1alpha1.NodePoolSpecPassthrough{
				NodeLabels: map[string]string{"stale": "value"},
			},
		},
	}
	np := NodePoolCRToPlatform(cr)

	if len(np.Spec.NodePool.NodeLabels) != 0 {
		t.Errorf("NodeLabels = %v, want empty", np.Spec.NodePool.NodeLabels)
	}
}

func TestClusterCRToPlatform_UpdatedAtReflectsLatestCondition(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	older := created.Add(1 * time.Hour)
	newer := created.Add(2 * time.Hour)

	cr := &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(created),
		},
		Status: hyperfleetv1alpha1.ClusterStatus{
			Phase: hyperfleetv1alpha1.ClusterPhaseReady,
			Conditions: []metav1.Condition{
				{Type: "Synced", Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(older)},
				{Type: "Available", Status: metav1.ConditionTrue, LastTransitionTime: metav1.NewTime(newer)},
			},
		},
	}

	cluster := ClusterCRToPlatform(cr)

	if !cluster.UpdatedAt.Equal(newer) {
		t.Errorf("UpdatedAt = %v, want %v (latest condition transition)", cluster.UpdatedAt, newer)
	}
	if cluster.Status == nil || !cluster.Status.LastUpdateTime.Equal(newer) {
		t.Errorf("Status.LastUpdateTime = %v, want %v", cluster.Status.LastUpdateTime, newer)
	}
}

func TestClusterCRToPlatform_UpdatedAtFallsBackToCreationTimestamp(t *testing.T) {
	created := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	cr := &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			CreationTimestamp: metav1.NewTime(created),
		},
	}

	cluster := ClusterCRToPlatform(cr)

	if !cluster.UpdatedAt.Equal(created) {
		t.Errorf("UpdatedAt = %v, want %v (creation timestamp fallback)", cluster.UpdatedAt, created)
	}
}

func TestPlatformCreateToClusterCR_SetsAccountLabel(t *testing.T) {
	req := &types.ClusterCreateRequest{
		Name: "my-cluster",
		Spec: &hyperfleetv1alpha1.ClusterSpec{
			HostedCluster: hyperfleetv1alpha1.HostedClusterSpecPassthrough{
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
