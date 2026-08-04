package hyperfleetdb

import (
	"strings"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/types"
)

// --- Cluster conversions ---

// ClusterCRToPlatform converts a v1alpha1.Cluster CR to the platform API type.
// Namespace = clusterID (UUID), Name = human-readable cluster name.
func ClusterCRToPlatform(cr *hyperfleetv1alpha1.Cluster) *types.Cluster {
	cluster := &types.Cluster{
		ID:              clusterIDFromNamespace(cr.Namespace),
		Name:            cr.Name,
		Generation:      cr.Generation,
		ResourceVersion: cr.ResourceVersion,
		Spec:            cr.Spec,
		CreatedAt:       cr.CreationTimestamp.Time,
		UpdatedAt:       metaTime(cr),
	}

	if cr.Spec.CreatorARN != "" {
		cluster.CreatedBy = cr.Spec.CreatorARN
	}

	if accountID := cr.Labels["hyperfleet.io/account-id"]; accountID != "" {
		cluster.TargetProjectID = accountID
	}

	cluster.OIDCIssuerURL = cr.Spec.HostedCluster.IssuerURL

	if phase := cr.Status.Phase; phase != "" {
		cluster.Status = &types.ClusterStatusInfo{
			ObservedGeneration:   cr.Status.ObservedGeneration,
			Phase:                string(phase),
			ControlPlaneEndpoint: apiEndpointFromCR(cr.Status.ControlPlaneEndpoint),
			Version:              cr.Status.Version,
			LastUpdateTime:       metaTime(cr),
		}

		if pr := cr.Status.PlacementRef; pr != nil {
			cluster.Status.PlacementRef = &types.PlacementReference{
				Name:              pr.Name,
				ManagementCluster: pr.ManagementCluster,
			}
		}

		if len(cr.Status.Conditions) > 0 {
			cluster.Status.Conditions = make([]types.Condition, 0, len(cr.Status.Conditions))
			for _, c := range cr.Status.Conditions {
				cluster.Status.Conditions = append(cluster.Status.Conditions, types.Condition{
					Type:               c.Type,
					Status:             string(c.Status),
					LastTransitionTime: c.LastTransitionTime.Time,
					Reason:             c.Reason,
					Message:            c.Message,
				})
			}
		}
	}

	return cluster
}

// PlatformCreateToClusterCR converts a platform ClusterCreateRequest into a
// v1alpha1.Cluster CR. metadata.Namespace = clusterID (UUID),
// metadata.Name = human-readable cluster name.
func PlatformCreateToClusterCR(clusterID, accountID string, req *types.ClusterCreateRequest) (*hyperfleetv1alpha1.Cluster, error) {
	spec := *req.Spec
	spec.AccountID = accountID
	spec.InternalID = clusterID

	return &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: clusterNamespace(clusterID),
			Labels: map[string]string{
				"hyperfleet.io/account-id": accountID,
			},
		},
		Spec: spec,
	}, nil
}

// ApplyPlatformUpdateToClusterCR applies an update request to an existing CR.
func ApplyPlatformUpdateToClusterCR(cr *hyperfleetv1alpha1.Cluster, req *types.ClusterUpdateRequest) error {
	if req.Spec == nil {
		return nil
	}
	cr.Spec = *req.Spec
	return nil
}

// ClusterStatusFromCR builds the status response from a Cluster CR.
func ClusterStatusFromCR(cr *hyperfleetv1alpha1.Cluster) *types.ClusterStatusResponse {
	platform := ClusterCRToPlatform(cr)
	return &types.ClusterStatusResponse{
		ClusterID: clusterIDFromNamespace(cr.Namespace),
		Status:    platform.Status,
	}
}

// --- NodePool conversions ---

// NodePoolCRToPlatform converts a v1alpha1.NodePool CR to the platform API type.
// Namespace = clusterID (UUID), Name = human-readable nodepool name.
func NodePoolCRToPlatform(cr *hyperfleetv1alpha1.NodePool) *types.NodePool {
	np := &types.NodePool{
		ID:              cr.Name,
		ClusterID:       clusterIDFromNamespace(cr.Namespace),
		Name:            cr.Name,
		Generation:      cr.Generation,
		ResourceVersion: cr.ResourceVersion,
		Spec:            cr.Spec,
		CreatedAt:       cr.CreationTimestamp.Time,
		UpdatedAt:       metaTime(cr),
	}

	if phase := cr.Status.Phase; phase != "" {
		np.Status = &types.NodePoolStatusInfo{
			ObservedGeneration: cr.Status.ObservedGeneration,
			Phase:              string(phase),
			LastUpdateTime:     metaTime(cr),
		}
		if len(cr.Status.Conditions) > 0 {
			np.Status.Conditions = make([]types.Condition, 0, len(cr.Status.Conditions))
			for _, c := range cr.Status.Conditions {
				np.Status.Conditions = append(np.Status.Conditions, types.Condition{
					Type:               c.Type,
					Status:             string(c.Status),
					LastTransitionTime: c.LastTransitionTime.Time,
					Reason:             c.Reason,
					Message:            c.Message,
				})
			}
		}
	}

	return np
}

// PlatformCreateToNodePoolCR converts a platform NodePoolCreateRequest into a
// v1alpha1.NodePool CR. metadata.Namespace = clusterID, metadata.Name = human name.
func PlatformCreateToNodePoolCR(accountID, internalPoolID string, req *types.NodePoolCreateRequest) (*hyperfleetv1alpha1.NodePool, error) {
	spec := *req.Spec
	spec.AccountID = accountID
	spec.InternalPoolID = internalPoolID

	return &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: clusterNamespace(req.ClusterID),
			Labels: map[string]string{
				"hyperfleet.io/account-id": accountID,
			},
		},
		Spec: spec,
	}, nil
}

// ApplyPlatformUpdateToNodePoolCR applies an update request to an existing CR.
func ApplyPlatformUpdateToNodePoolCR(cr *hyperfleetv1alpha1.NodePool, req *types.NodePoolUpdateRequest) error {
	if req.Spec == nil {
		return nil
	}
	cr.Spec = *req.Spec
	return nil
}

// NodePoolStatusFromCR builds the status response from a NodePool CR.
func NodePoolStatusFromCR(cr *hyperfleetv1alpha1.NodePool) *types.NodePoolStatusResponse {
	platform := NodePoolCRToPlatform(cr)
	return &types.NodePoolStatusResponse{
		NodePoolID: cr.Name,
		Status:     platform.Status,
	}
}

// --- helpers ---

func apiEndpointFromCR(ep hypershiftv1beta1.APIEndpoint) *types.APIEndpoint {
	if ep.Host == "" {
		return nil
	}
	return &types.APIEndpoint{Host: ep.Host, Port: ep.Port}
}

func metaTime(obj metav1.Object) time.Time {
	if t := obj.GetDeletionTimestamp(); t != nil {
		return t.Time
	}
	return obj.GetCreationTimestamp().Time
}

const clusterNSPrefix = "cluster-"

func clusterNamespace(clusterID string) string {
	return clusterNSPrefix + clusterID
}

func clusterIDFromNamespace(ns string) string {
	return strings.TrimPrefix(ns, clusterNSPrefix)
}
