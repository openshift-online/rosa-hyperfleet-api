package hyperfleetdb

import (
	"encoding/json"
	"strings"
	"time"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/conversion"
	convv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/conversion/v1alpha1"
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

// MergeSpecJSON merges raw JSON into dst. Only fields present in the JSON
// overwrite dst; omitted fields are preserved. This avoids data loss from
// non-omitempty struct fields (e.g. HostedCluster, NodePool passthrough)
// that would serialize as empty objects if marshaled from a typed Go struct.
func MergeSpecJSON(dst any, specJSON json.RawMessage) error {
	return json.Unmarshal(specJSON, dst)
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

	// Sync top-level autoRepair → passthrough management.autoRepair so the
	// response is internally consistent. The operator defaults to true when
	// autoRepair is unset, so we mirror that here.
	if np.Spec.AutoRepair != nil {
		np.Spec.NodePool.Management.AutoRepair = *np.Spec.AutoRepair
	} else {
		np.Spec.NodePool.Management.AutoRepair = true
	}

	// Sync top-level labels → passthrough nodeLabels (unconditional to clear stale values).
	np.Spec.NodePool.NodeLabels = np.Spec.Labels

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

// NodePoolStatusFromCR builds the status response from a NodePool CR.
func NodePoolStatusFromCR(cr *hyperfleetv1alpha1.NodePool) *types.NodePoolStatusResponse {
	platform := NodePoolCRToPlatform(cr)
	return &types.NodePoolStatusResponse{
		NodePoolID: cr.Name,
		Status:     platform.Status,
	}
}

// --- OidcConfig conversions ---

// OidcConfigCRToPlatform converts a v1alpha1.OidcConfig CR to the platform API type.
func OidcConfigCRToPlatform(cr *hyperfleetv1alpha1.OidcConfig) *types.OidcConfig {
	projected := convv1alpha1.ProjectOidcConfig(cr)
	oc := &types.OidcConfig{
		ID:              cr.Name,
		Generation:      cr.Generation,
		ResourceVersion: cr.ResourceVersion,
		Spec:            projected.Spec,
		CreatedAt:       cr.CreationTimestamp.Time,
		UpdatedAt:       metaTime(cr),
	}

	if phase := cr.Status.Phase; phase != "" {
		oc.Status = &types.OidcConfigStatusInfo{
			ObservedGeneration: cr.Status.ObservedGeneration,
			Phase:              string(phase),
			Thumbprint:         cr.Status.Thumbprint,
			LastUpdateTime:     metaTime(cr),
		}

		if cr.Status.LastUsedTimestamp != nil {
			t := cr.Status.LastUsedTimestamp.Time
			oc.Status.LastUsedTimestamp = &t
		}

		if len(cr.Status.Conditions) > 0 {
			oc.Status.Conditions = make([]types.Condition, 0, len(cr.Status.Conditions))
			for _, c := range cr.Status.Conditions {
				oc.Status.Conditions = append(oc.Status.Conditions, types.Condition{
					Type:               c.Type,
					Status:             string(c.Status),
					LastTransitionTime: c.LastTransitionTime.Time,
					Reason:             c.Reason,
					Message:            c.Message,
				})
			}
		}
	}

	return oc
}

// PlatformCreateToOidcConfigCR converts a platform OidcConfigCreateRequest into
// a v1alpha1.OidcConfig CR. metadata.Namespace = account-<accountID>, metadata.Name = configID.
func PlatformCreateToOidcConfigCR(configID, accountID string, req *types.OidcConfigCreateRequest) *hyperfleetv1alpha1.OidcConfig {
	crdSpec := convv1alpha1.UnprojectOidcConfig(req.Spec, &conversion.ServiceSetFields{
		AccountID: accountID,
	})

	return &hyperfleetv1alpha1.OidcConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configID,
			Namespace: accountNSPrefix + accountID,
		},
		Spec: *crdSpec,
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

// clusterUUIDLen is the fixed length of a RFC 4122 UUID string (e.g. "4610b27e-8f77-4f4c-9661-c11b42e04dec").
const clusterUUIDLen = 36

// MaxClusterNameLen is the maximum allowed cluster name length.
// HyperShift creates a control plane namespace as "<hc-namespace>-<hc-name>",
// which expands to "cluster-<uuid>-<name>" and must fit within 63 characters (k8s namespace limit).
const MaxClusterNameLen = 63 - len(clusterNSPrefix) - clusterUUIDLen - len("-")

func clusterNamespace(clusterID string) string {
	return clusterNSPrefix + clusterID
}

func clusterIDFromNamespace(ns string) string {
	return strings.TrimPrefix(ns, clusterNSPrefix)
}
