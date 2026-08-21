package render

import (
	"fmt"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

// NodePoolResource generates the HyperShift NodePool resource for the MC.
func NodePoolResource(nodePool *hyperfleetv1alpha1.NodePool, cluster *hyperfleetv1alpha1.Cluster) (Resource, error) {
	clusterID := ClusterIDFromNamespace(cluster.Namespace)
	clusterName := cluster.Name // human-readable
	ns := cluster.Namespace     // already "cluster-<uuid>"
	npName := fmt.Sprintf("%s-%s", clusterName, nodePool.Name)

	npSpec, err := toNodePoolSpec(&nodePool.Spec.NodePool)
	if err != nil {
		return Resource{}, fmt.Errorf("converting NodePoolSpec for nodepool %s/%s: %w", ns, npName, err)
	}
	npSpec.ClusterName = clusterName
	npSpec.Release.Image = "quay.io/openshift-release-dev/ocp-release:5.0.0-ec.2-multi"

	if npSpec.Management.UpgradeType == "" {
		npSpec.Management.UpgradeType = hypershiftv1beta1.UpgradeTypeReplace
	}
	if nodePool.Spec.AutoRepair != nil {
		npSpec.Management.AutoRepair = *nodePool.Spec.AutoRepair
	} else {
		npSpec.Management.AutoRepair = true
	}

	npSpec.NodeLabels = nodePool.Spec.Labels

	if npSpec.Replicas == nil {
		npSpec.Replicas = ptr.To(int32(2))
	}

	if npSpec.Platform.AWS != nil {
		if npSpec.Platform.AWS.InstanceType == "" {
			npSpec.Platform.AWS.InstanceType = "t3a.xlarge"
		}
		if npSpec.Platform.AWS.RootVolume == nil {
			npSpec.Platform.AWS.RootVolume = &hypershiftv1beta1.Volume{Size: 120, Type: "gp3"}
		} else {
			if npSpec.Platform.AWS.RootVolume.Size == 0 {
				npSpec.Platform.AWS.RootVolume.Size = 120
			}
			if npSpec.Platform.AWS.RootVolume.Type == "" {
				npSpec.Platform.AWS.RootVolume.Type = "gp3"
			}
		}
		npSpec.Platform.AWS.ResourceTags = appendNodePoolSystemTags(npSpec.Platform.AWS.ResourceTags)
	}

	return Resource{
		Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "nodepools",
		Name: npName, Namespace: ns,
		Object: &hypershiftv1beta1.NodePool{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "hypershift.openshift.io/v1beta1",
				Kind:       "NodePool",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      npName,
				Namespace: ns,
				Labels: map[string]string{
					"hyperfleet.io/cluster-id": clusterID,
				},
			},
			Spec: *npSpec,
		},
	}, nil
}
