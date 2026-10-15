package handlers

import (
	"reflect"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/utils/ptr"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
)

func TestClusterUpdateActions(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*v1.ClusterSpec)
		want []authz.Action
	}{
		"no change":      {func(*v1.ClusterSpec) {}, nil},
		"properties":     {func(s *v1.ClusterSpec) { s.Properties = map[string]string{"team": "new"} }, nil},
		"release":        {func(s *v1.ClusterSpec) { s.HostedCluster.Release.Image = "new" }, []authz.Action{authz.UpdateClusterVersion}},
		"upgrade policy": {func(s *v1.ClusterSpec) { s.ControlPlaneUpgradePolicy = &v1.ControlPlaneUpgradePolicySpec{} }, []authz.Action{authz.UpdateClusterVersion}},
		"configuration":  {func(s *v1.ClusterSpec) { s.HostedCluster.Configuration = &hypershiftv1beta1.ClusterConfiguration{} }, []authz.Action{authz.UpdateClusterConfig}},
		"version and configuration": {func(s *v1.ClusterSpec) {
			s.HostedCluster.Release.Image = "new"
			s.HostedCluster.Configuration = &hypershiftv1beta1.ClusterConfiguration{}
		}, []authz.Action{authz.UpdateClusterConfig, authz.UpdateClusterVersion}},
	} {
		t.Run(name, func(t *testing.T) {
			old := v1.ClusterSpec{Properties: map[string]string{"team": "old"}}
			old.HostedCluster.Release.Image = "old"
			candidate := *old.DeepCopy()
			tc.edit(&candidate)
			if got := clusterUpdateActions(&old, &candidate); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNodePoolUpdateActions(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(*v1.NodePoolSpec)
		want []authz.Action
	}{
		"no change":       {func(*v1.NodePoolSpec) {}, nil},
		"labels":          {func(s *v1.NodePoolSpec) { s.Labels = map[string]string{"team": "new"} }, nil},
		"replicas":        {func(s *v1.NodePoolSpec) { s.NodePool.Replicas = ptr.To[int32](4) }, []authz.Action{authz.ScaleNodePool}},
		"replicas reset":  {func(s *v1.NodePoolSpec) { s.NodePool.Replicas = nil }, []authz.Action{authz.ScaleNodePool}},
		"autoscaling":     {func(s *v1.NodePoolSpec) { s.NodePool.AutoScaling = &hypershiftv1beta1.NodePoolAutoScaling{Max: 5} }, []authz.Action{authz.ScaleNodePool}},
		"release":         {func(s *v1.NodePoolSpec) { s.NodePool.Release.Image = "new" }, []authz.Action{authz.UpdateNodePoolVersion}},
		"scale + release": {func(s *v1.NodePoolSpec) { s.NodePool.Replicas = nil; s.NodePool.Release.Image = "new" }, []authz.Action{authz.ScaleNodePool, authz.UpdateNodePoolVersion}},
	} {
		t.Run(name, func(t *testing.T) {
			old := v1.NodePoolSpec{Labels: map[string]string{"team": "old"}}
			old.NodePool.Replicas = ptr.To[int32](3)
			old.NodePool.Release.Image = "old"
			candidate := *old.DeepCopy()
			tc.edit(&candidate)
			if got := nodePoolUpdateActions(&old, &candidate); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
