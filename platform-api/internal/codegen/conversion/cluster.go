package conversion

import (
	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"
)

// ClusterServiceSetFields holds platform-injected values for cluster creation.
type ClusterServiceSetFields struct {
	CreatorARN string
	IssuerURL  string
}

// InjectClusterServiceSet populates service-set fields on a ClusterSpec during creation.
// Only non-empty values are injected.
func InjectClusterServiceSet(spec *v1alpha1.ClusterSpec, ssf ClusterServiceSetFields) {
	if ssf.CreatorARN != "" {
		spec.CreatorARN = ssf.CreatorARN
	}
	if ssf.IssuerURL != "" {
		spec.HostedCluster.IssuerURL = ssf.IssuerURL
	}
}

// PreserveClusterServiceSet restores service-set field values from a pre-update
// snapshot into the updated spec, preventing the full-spec replacement in
// ApplyPlatformUpdateToClusterCR from wiping platform-managed fields.
func PreserveClusterServiceSet(updated, snapshot *v1alpha1.ClusterSpec) {
	updated.CreatorARN = snapshot.CreatorARN
	updated.AccountID = snapshot.AccountID
	updated.InternalID = snapshot.InternalID
	updated.HostedCluster.IssuerURL = snapshot.HostedCluster.IssuerURL
}
