package hyperfleetdb

import (
	"encoding/json"
	"maps"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/conversion"
	v1alpha1conv "github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/conversion/v1alpha1"
)

// MergeSpecJSON merges raw JSON into dst. Only fields present in the JSON
// overwrite dst; omitted fields are preserved. This avoids data loss from
// non-omitempty struct fields (e.g. HostedCluster, NodePool passthrough)
// that would serialize as empty objects if marshaled from a typed Go struct.
func MergeSpecJSON(dst any, specJSON []byte) error {
	if len(specJSON) == 0 {
		return nil
	}
	return json.Unmarshal(specJSON, dst)
}

// --- Cluster conversions ---

// PublicToInternalCluster converts public.Cluster to internal v1alpha1.Cluster
// for storage in FleetDB, enriched with service-set fields. Only the client-owned
// metadata is kept: name, namespace, annotations, and labels outside the reserved
// hyperfleet.io/ prefix. uid, ownerReferences, and finalizers are never taken from
// the client. The input pub is not modified.
func PublicToInternalCluster(pub *public.Cluster, accountID string) *hyperfleetv1alpha1.Cluster {
	if pub == nil {
		return nil
	}

	enrichment := &conversion.ServiceSetFields{
		AccountID: accountID,
	}
	crdSpec := v1alpha1conv.UnprojectCluster(&pub.Spec, enrichment)

	return &hyperfleetv1alpha1.Cluster{
		TypeMeta:   pub.TypeMeta,
		ObjectMeta: clientObjectMeta(pub.ObjectMeta),
		Spec:       *crdSpec,
	}
}

// InternalToPublicCluster converts internal v1alpha1.Cluster to public.Cluster
// for REST API responses. Filters service-set fields via JSON roundtrip projection.
func InternalToPublicCluster(cr *hyperfleetv1alpha1.Cluster) *public.Cluster {
	if cr == nil {
		return nil
	}
	return v1alpha1conv.ProjectCluster(cr)
}

// --- NodePool conversions ---

// PublicToInternalNodePool converts public.NodePool to internal v1alpha1.NodePool
// for storage in FleetDB. It enriches service-set fields, points the NodePool at
// its parent cluster (controller ownerReference and hyperfleet.io/cluster-uid
// label, set together from the same object), and syncs the top-level autoRepair
// and labels fields into the HyperShift passthrough. The input pub is not modified.
func PublicToInternalNodePool(pub *public.NodePool, accountID string, cluster *hyperfleetv1alpha1.Cluster) *hyperfleetv1alpha1.NodePool {
	if pub == nil {
		return nil
	}

	meta := clientObjectMeta(pub.ObjectMeta)
	setOwner(&meta, cluster)

	// Capture top-level user values before Unproject, which overlays ServiceSetFields
	// (some of which have no omitempty and can zero out fields like Labels).
	// Clone labels so crdSpec.Labels and NodePool.NodeLabels do not share a mutable
	// map with each other or with the original pub.Spec.Labels.
	userAutoRepair := pub.Spec.AutoRepair
	userLabels := maps.Clone(pub.Spec.Labels)

	enrichment := &conversion.ServiceSetFields{
		AccountID: accountID,
	}
	crdSpec := v1alpha1conv.UnprojectNodePool(&pub.Spec, enrichment)

	// ServiceSetFields.Labels has no omitempty so the overlay can zero spec.Labels.
	// Restore the user-supplied labels so they're preserved in the stored CRD and
	// remain readable by ProjectNodePool on the read path.
	crdSpec.Labels = maps.Clone(userLabels)

	// Sync top-level fields into the HyperShift passthrough using the pre-Unproject
	// values; the operator owns management.autoRepair and nodeLabels and reconciles them,
	// but we mirror them here so the stored CRD is internally consistent from day one.
	// Clone again so crdSpec.Labels and crdSpec.NodePool.NodeLabels are independent maps.
	syncNodePoolPassthrough(crdSpec, userAutoRepair, maps.Clone(userLabels))

	return &hyperfleetv1alpha1.NodePool{
		TypeMeta:   pub.TypeMeta,
		ObjectMeta: meta,
		Spec:       *crdSpec,
	}
}

// InternalToPublicNodePool converts internal v1alpha1.NodePool to public.NodePool
// for REST API responses. Filters service-set fields via JSON roundtrip projection.
func InternalToPublicNodePool(cr *hyperfleetv1alpha1.NodePool) *public.NodePool {
	if cr == nil {
		return nil
	}
	return v1alpha1conv.ProjectNodePool(cr)
}

// --- OidcConfig conversions ---

// PublicToInternalOidcConfig converts public.OidcConfig to internal v1alpha1.OidcConfig
// for storage in FleetDB. Sets namespace from accountID and name from configID, and
// keeps only client-owned metadata (see PublicToInternalCluster).
// The input pub is not modified.
func PublicToInternalOidcConfig(pub *public.OidcConfig, accountID, configID string) *hyperfleetv1alpha1.OidcConfig {
	if pub == nil {
		return nil
	}

	meta := clientObjectMeta(pub.ObjectMeta)
	meta.Namespace = hyperfleetv1alpha1.AccountNamespace(accountID)
	meta.Name = configID

	enrichment := &conversion.ServiceSetFields{
		AccountID: accountID,
	}
	crdSpec := v1alpha1conv.UnprojectOidcConfig(&pub.Spec, enrichment)

	return &hyperfleetv1alpha1.OidcConfig{
		TypeMeta:   pub.TypeMeta,
		ObjectMeta: meta,
		Spec:       *crdSpec,
	}
}

// InternalToPublicOidcConfig converts internal v1alpha1.OidcConfig to public.OidcConfig
// for REST API responses. Filters service-set fields via JSON roundtrip projection.
func InternalToPublicOidcConfig(cr *hyperfleetv1alpha1.OidcConfig) *public.OidcConfig {
	if cr == nil {
		return nil
	}
	return v1alpha1conv.ProjectOidcConfig(cr)
}

// --- Helpers ---

// clientObjectMeta keeps the metadata a client may set on create: name,
// namespace, annotations, and labels outside the reserved hyperfleet.io/ prefix.
// Everything else (uid, ownerReferences, finalizers, ...) is set by the service.
func clientObjectMeta(in metav1.ObjectMeta) metav1.ObjectMeta {
	out := metav1.ObjectMeta{
		Name:        in.Name,
		Namespace:   in.Namespace,
		Annotations: maps.Clone(in.Annotations),
	}
	for k, v := range in.Labels {
		if strings.HasPrefix(k, hyperfleetv1alpha1.ReservedLabelPrefix) {
			continue
		}
		if out.Labels == nil {
			out.Labels = make(map[string]string, len(in.Labels))
		}
		out.Labels[k] = v
	}
	return out
}

// setOwner points meta at its parent cluster: a controller ownerReference and
// the hyperfleet.io/cluster-uid label, both from the same object so they can't
// drift apart.
func setOwner(meta *metav1.ObjectMeta, cluster *hyperfleetv1alpha1.Cluster) {
	meta.OwnerReferences = []metav1.OwnerReference{{
		APIVersion:         hyperfleetv1alpha1.GroupVersion.String(),
		Kind:               "Cluster",
		Name:               cluster.Name,
		UID:                cluster.UID,
		Controller:         ptr.To(true),
		BlockOwnerDeletion: ptr.To(true),
	}}
	if meta.Labels == nil {
		meta.Labels = make(map[string]string)
	}
	meta.Labels[hyperfleetv1alpha1.ClusterUIDLabel] = string(cluster.UID)
}

// syncNodePoolPassthrough mirrors the top-level autoRepair and labels into the
// HyperShift passthrough for internal consistency. Called only during public→internal
// conversion; the operator reconciles these during its own sync loop.
// autoRepair and labels are passed explicitly because the ServiceSetFields overlay in
// UnprojectNodePool can zero out spec.Labels (no omitempty on ServiceSetFields.Labels).
func syncNodePoolPassthrough(spec *hyperfleetv1alpha1.NodePoolSpec, autoRepair *bool, labels map[string]string) {
	if spec == nil {
		return
	}

	// Default autoRepair to true when unset (matches operator behavior).
	if autoRepair != nil {
		spec.NodePool.Management.AutoRepair = *autoRepair
	} else {
		spec.NodePool.Management.AutoRepair = true
	}

	spec.NodePool.NodeLabels = labels
}
