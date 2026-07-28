package conversion

import (
	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"
)

// PreserveNodePoolServiceSet restores service-set field values from a pre-update
// snapshot into the updated spec, preventing the full-spec replacement in
// ApplyPlatformUpdateToNodePoolCR from wiping platform-managed fields.
func PreserveNodePoolServiceSet(updated, snapshot *v1alpha1.NodePoolSpec) {
	updated.AccountID = snapshot.AccountID
	updated.InternalPoolID = snapshot.InternalPoolID
}
