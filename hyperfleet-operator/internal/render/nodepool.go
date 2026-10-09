package render

import (
	"fmt"
	"strings"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

const defaultRootVolumeSizeGiB int64 = 300

// NodePoolResource generates the HyperShift NodePool resource for the MC.
func NodePoolResource(nodePool *hyperfleetv1alpha1.NodePool, cluster *hyperfleetv1alpha1.Cluster) (Resource, error) {
	clusterID, ns, err := managementClusterIdentity(cluster)
	if err != nil {
		return Resource{}, err
	}
	clusterName := cluster.Name // human-readable
	parentName, npName, ok := strings.Cut(nodePool.Name, ".")
	if !ok || parentName != clusterName || npName == "" || strings.Contains(npName, ".") {
		return Resource{}, fmt.Errorf("NodePool name %q must be <cluster>.<child> for parent Cluster %q", nodePool.Name, clusterName)
	}

	npSpec, err := toNodePoolSpec(&nodePool.Spec.NodePool)
	if err != nil {
		return Resource{}, fmt.Errorf("converting NodePoolSpec for nodepool %s/%s: %w", ns, npName, err)
	}
	npSpec.ClusterName = clusterName
	// NodePools without an explicit release follow the parent cluster's.
	if npSpec.Release.Image == "" {
		npSpec.Release.Image = cluster.Spec.HostedCluster.Release.Image
	}

	if npSpec.Management.UpgradeType == "" {
		npSpec.Management.UpgradeType = hypershiftv1beta1.UpgradeTypeReplace
	}
	if nodePool.Spec.AutoRepair != nil {
		npSpec.Management.AutoRepair = *nodePool.Spec.AutoRepair
	} else {
		npSpec.Management.AutoRepair = true
	}

	npSpec.NodeLabels = nodePool.Spec.Labels

	if npSpec.Replicas == nil && npSpec.AutoScaling == nil {
		npSpec.Replicas = ptr.To(int32(2))
	}

	if npSpec.Platform.AWS != nil {
		if npSpec.Platform.AWS.InstanceType == "" {
			npSpec.Platform.AWS.InstanceType = "t3a.xlarge"
		}
		if npSpec.Platform.AWS.RootVolume == nil {
			npSpec.Platform.AWS.RootVolume = &hypershiftv1beta1.Volume{Size: defaultRootVolumeSizeGiB, Type: "gp3"}
		} else {
			if npSpec.Platform.AWS.RootVolume.Size == 0 {
				npSpec.Platform.AWS.RootVolume.Size = defaultRootVolumeSizeGiB
			}
			if npSpec.Platform.AWS.RootVolume.Type == "" {
				npSpec.Platform.AWS.RootVolume.Type = "gp3"
			}
		}
		// The worker EC2 instances are created by the NodePool, not the
		// HostedCluster, so the cluster's customer tags have to be applied here
		// too for day-1 tagging to actually reach them.
		npSpec.Platform.AWS.ResourceTags = appendSystemTags(npSpec.Platform.AWS.ResourceTags, "", cluster.Spec.Tags)
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
				Labels:    clusterLabels(clusterID),
			},
			Spec: *npSpec,
		},
	}, nil
}
