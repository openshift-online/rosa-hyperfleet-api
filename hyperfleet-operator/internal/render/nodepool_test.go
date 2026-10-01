package render

import (
	"testing"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func testNodePool() *hyperfleetv1alpha1.NodePool {
	return &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster.workers",
			Namespace: "account-123456789012",
		},
		Spec: hyperfleetv1alpha1.NodePoolSpec{
			NodePool: hyperfleetv1alpha1.NodePoolSpecPassthrough{
				Replicas: ptr.To(int32(3)),
				Management: hypershiftv1beta1.NodePoolManagement{
					AutoRepair:  true,
					UpgradeType: hypershiftv1beta1.UpgradeTypeReplace,
				},
				Release: hypershiftv1beta1.Release{Image: "quay.io/ocp:4.17"},
				Platform: hypershiftv1beta1.NodePoolPlatform{
					Type: hypershiftv1beta1.AWSPlatform,
					AWS: &hypershiftv1beta1.AWSNodePoolPlatform{
						InstanceType:    "m6a.xlarge",
						RootVolume:      &hypershiftv1beta1.Volume{Size: 120, Type: "gp3"},
						InstanceProfile: "worker-profile",
						Subnet: hypershiftv1beta1.AWSResourceReference{
							ID: ptr.To("subnet-1"),
						},
						SecurityGroups: []hypershiftv1beta1.AWSResourceReference{
							{ID: ptr.To("sg-abc")},
							{ID: ptr.To("sg-def")},
						},
					},
				},
			},
		},
	}
}

func TestNodePoolResourceGVR(t *testing.T) {
	r, err := NodePoolResource(testNodePool(), testCluster())
	if err != nil {
		t.Fatalf("NodePoolResource: %v", err)
	}

	if r.Group != "hypershift.openshift.io" {
		t.Errorf("Group = %q, want %q", r.Group, "hypershift.openshift.io")
	}
	if r.Version != "v1beta1" {
		t.Errorf("Version = %q, want %q", r.Version, "v1beta1")
	}
	if r.Resource != "nodepools" {
		t.Errorf("Resource = %q, want %q", r.Resource, "nodepools")
	}
}

func TestNodePoolResourceNaming(t *testing.T) {
	r, err := NodePoolResource(testNodePool(), testCluster())
	if err != nil {
		t.Fatalf("NodePoolResource: %v", err)
	}

	// On the MC the pool lives in the cluster's own namespace, so only the
	// child part of "<cluster>.<nodepool>" is rendered.
	wantName := "workers"
	if r.Name != wantName {
		t.Errorf("Name = %q, want %q", r.Name, wantName)
	}
	wantNS := "cluster-abc12345"
	if r.Namespace != wantNS {
		t.Errorf("Namespace = %q, want %q", r.Namespace, wantNS)
	}
}

func TestNodePoolResourceRejectsUnqualifiedName(t *testing.T) {
	np := testNodePool()
	np.Name = "workers"
	if _, err := NodePoolResource(np, testCluster()); err == nil {
		t.Error("expected an error for a NodePool name without a <cluster>. prefix")
	}
}

func TestNodePoolResourceObject(t *testing.T) {
	r, err := NodePoolResource(testNodePool(), testCluster())
	if err != nil {
		t.Fatalf("NodePoolResource: %v", err)
	}
	np, ok := r.Object.(*hypershiftv1beta1.NodePool)
	if !ok {
		t.Fatalf("Object is %T, want *NodePool", r.Object)
	}

	if np.Spec.ClusterName != "my-cluster" {
		t.Errorf("ClusterName = %q, want %q", np.Spec.ClusterName, "my-cluster")
	}
	if np.Spec.Replicas == nil || *np.Spec.Replicas != 3 {
		t.Errorf("Replicas = %v, want 3", np.Spec.Replicas)
	}
	if np.Spec.Platform.Type != hypershiftv1beta1.AWSPlatform {
		t.Errorf("Platform.Type = %q, want AWS", np.Spec.Platform.Type)
	}
	if np.Spec.Platform.AWS == nil {
		t.Fatal("Platform.AWS is nil")
	}
	if np.Spec.Platform.AWS.InstanceType != "m6a.xlarge" {
		t.Errorf("InstanceType = %q, want %q", np.Spec.Platform.AWS.InstanceType, "m6a.xlarge")
	}
	if got := len(np.Spec.Platform.AWS.SecurityGroups); got != 2 {
		t.Errorf("SecurityGroups count = %d, want 2", got)
	}
}

func TestNodePoolResourceDefaults(t *testing.T) {
	minimalNP := &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster.workers",
			Namespace: "account-123456789012",
		},
		Spec: hyperfleetv1alpha1.NodePoolSpec{
			NodePool: hyperfleetv1alpha1.NodePoolSpecPassthrough{
				Platform: hypershiftv1beta1.NodePoolPlatform{
					Type: hypershiftv1beta1.AWSPlatform,
					AWS: &hypershiftv1beta1.AWSNodePoolPlatform{
						InstanceProfile: "worker-profile",
						Subnet:          hypershiftv1beta1.AWSResourceReference{ID: ptr.To("subnet-1")},
						SecurityGroups:  []hypershiftv1beta1.AWSResourceReference{{ID: ptr.To("sg-abc")}},
					},
				},
			},
		},
	}

	r, err := NodePoolResource(minimalNP, testCluster())
	if err != nil {
		t.Fatalf("NodePoolResource: %v", err)
	}
	np := r.Object.(*hypershiftv1beta1.NodePool)

	tests := []struct {
		name  string
		check func(*testing.T)
	}{
		{"UpgradeType", func(t *testing.T) {
			if np.Spec.Management.UpgradeType != hypershiftv1beta1.UpgradeTypeReplace {
				t.Errorf("got %q, want %q", np.Spec.Management.UpgradeType, hypershiftv1beta1.UpgradeTypeReplace)
			}
		}},
		{"AutoRepair", func(t *testing.T) {
			if !np.Spec.Management.AutoRepair {
				t.Error("got false, want true")
			}
		}},
		{"Replicas", func(t *testing.T) {
			if np.Spec.Replicas == nil || *np.Spec.Replicas != 2 {
				t.Errorf("got %v, want 2", np.Spec.Replicas)
			}
		}},
		{"InstanceType", func(t *testing.T) {
			if np.Spec.Platform.AWS.InstanceType != "t3a.xlarge" {
				t.Errorf("got %q, want %q", np.Spec.Platform.AWS.InstanceType, "t3a.xlarge")
			}
		}},
		{"RootVolume.Size", func(t *testing.T) {
			if np.Spec.Platform.AWS.RootVolume == nil || np.Spec.Platform.AWS.RootVolume.Size != 300 {
				t.Errorf("got %v, want 300", np.Spec.Platform.AWS.RootVolume)
			}
		}},
		{"RootVolume.Type", func(t *testing.T) {
			if np.Spec.Platform.AWS.RootVolume == nil || np.Spec.Platform.AWS.RootVolume.Type != "gp3" {
				t.Errorf("got %v, want gp3", np.Spec.Platform.AWS.RootVolume)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

func TestNodePoolResourceAutoScalingOmitsDefaultReplicas(t *testing.T) {
	np := testNodePool()
	np.Spec.NodePool.Replicas = nil
	min := int32(1)
	np.Spec.NodePool.AutoScaling = &hypershiftv1beta1.NodePoolAutoScaling{
		Min: &min,
		Max: 3,
	}

	r, err := NodePoolResource(np, testCluster())
	if err != nil {
		t.Fatalf("NodePoolResource: %v", err)
	}
	rendered := r.Object.(*hypershiftv1beta1.NodePool)
	if rendered.Spec.Replicas != nil {
		t.Errorf("Replicas = %v with autoscaling enabled, want nil", *rendered.Spec.Replicas)
	}
	if rendered.Spec.AutoScaling == nil {
		t.Fatal("AutoScaling is nil, want it preserved")
	}
	if rendered.Spec.AutoScaling.Min == nil || *rendered.Spec.AutoScaling.Min != min {
		t.Errorf("AutoScaling.Min = %v, want %d", rendered.Spec.AutoScaling.Min, min)
	}
	if rendered.Spec.AutoScaling.Max != 3 {
		t.Errorf("AutoScaling.Max = %d, want 3", rendered.Spec.AutoScaling.Max)
	}
}

func TestNodePoolResourceLabels(t *testing.T) {
	r, err := NodePoolResource(testNodePool(), testCluster())
	if err != nil {
		t.Fatalf("NodePoolResource: %v", err)
	}
	np := r.Object.(*hypershiftv1beta1.NodePool)

	if np.Labels["hyperfleet.io/cluster-id"] != "abc12345" {
		t.Errorf("cluster-id label = %q, want %q", np.Labels["hyperfleet.io/cluster-id"], "abc12345")
	}
}

func TestNodePoolResourceAutoRepair(t *testing.T) {
	tests := []struct {
		name       string
		autoRepair *bool
		want       bool
	}{
		{"nil defaults to true", nil, true},
		{"explicit true", ptr.To(true), true},
		{"explicit false", ptr.To(false), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			np := testNodePool()
			np.Spec.AutoRepair = tt.autoRepair
			r, err := NodePoolResource(np, testCluster())
			if err != nil {
				t.Fatalf("NodePoolResource: %v", err)
			}
			got := r.Object.(*hypershiftv1beta1.NodePool).Spec.Management.AutoRepair
			if got != tt.want {
				t.Errorf("AutoRepair = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodePoolResourceNodeLabels(t *testing.T) {
	np := testNodePool()
	np.Spec.Labels = map[string]string{"env": "staging", "team": "platform"}

	r, err := NodePoolResource(np, testCluster())
	if err != nil {
		t.Fatalf("NodePoolResource: %v", err)
	}
	got := r.Object.(*hypershiftv1beta1.NodePool).Spec.NodeLabels

	for k, v := range np.Spec.Labels {
		if got[k] != v {
			t.Errorf("NodeLabels[%q] = %q, want %q", k, got[k], v)
		}
	}
	if len(got) != len(np.Spec.Labels) {
		t.Errorf("NodeLabels len = %d, want %d", len(got), len(np.Spec.Labels))
	}
}

func TestNodePoolResourceNodeLabelsEmpty(t *testing.T) {
	np := testNodePool()
	np.Spec.Labels = nil

	r, err := NodePoolResource(np, testCluster())
	if err != nil {
		t.Fatalf("NodePoolResource: %v", err)
	}
	got := r.Object.(*hypershiftv1beta1.NodePool).Spec.NodeLabels
	if len(got) != 0 {
		t.Errorf("NodeLabels = %v, want empty", got)
	}
}
