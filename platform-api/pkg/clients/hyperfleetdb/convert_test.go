package hyperfleetdb

import (
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
)

const (
	testClusterID    = "550e8400-e29b-41d4-a716-446655440000"
	testAccountID    = "account-123"
	testClusterName  = "test-cluster"
	testNodePoolName = "test-nodepool"
)

// --- Cluster conversion tests ---

func TestPublicToInternalCluster_SetsMetadata(t *testing.T) {
	pub := &public.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: testClusterName,
		},
		Spec: public.ClusterSpec{
			DisplayName: "Test Cluster",
		},
	}

	result := PublicToInternalCluster(pub, testAccountID, testClusterID)

	require.NotNil(t, result)
	assert.Equal(t, testClusterName, result.Name)
	assert.Equal(t, clusterNamespace(testClusterID), result.Namespace)
	assert.Equal(t, types.UID(testClusterID), result.UID)
	assert.Equal(t, testAccountID, result.Labels["hyperfleet.io/account-id"])
}

func TestPublicToInternalCluster_InjectsServiceSetFields(t *testing.T) {
	pub := &public.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName},
		Spec:       public.ClusterSpec{DisplayName: "Test Cluster"},
	}

	result := PublicToInternalCluster(pub, testAccountID, testClusterID)

	require.NotNil(t, result)
	assert.Equal(t, testAccountID, result.Spec.AccountID)
	assert.Equal(t, testClusterID, result.Spec.InternalID)
}

func TestPublicToInternalCluster_NilInput(t *testing.T) {
	result := PublicToInternalCluster(nil, testAccountID, testClusterID)
	assert.Nil(t, result)
}

func TestInternalToPublicCluster_FiltersServiceSetFields(t *testing.T) {
	cr := &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName,
			Namespace: clusterNamespace(testClusterID),
			UID:       types.UID(testClusterID),
			Labels:    map[string]string{"hyperfleet.io/account-id": testAccountID},
		},
		Spec: hyperfleetv1alpha1.ClusterSpec{
			AccountID:   testAccountID,
			InternalID:  testClusterID,
			DisplayName: "Test Cluster",
		},
	}

	result := InternalToPublicCluster(cr)

	require.NotNil(t, result)
	assert.Equal(t, testClusterName, result.Name)
	assert.Equal(t, "Test Cluster", result.Spec.DisplayName)
	// Service-set fields absent from public type (filtered by JSON roundtrip)
}

func TestInternalToPublicCluster_NilInput(t *testing.T) {
	result := InternalToPublicCluster(nil)
	assert.Nil(t, result)
}

func TestClusterRoundTrip(t *testing.T) {
	original := &public.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName},
		Spec:       public.ClusterSpec{DisplayName: "Test Cluster"},
	}

	internal := PublicToInternalCluster(original, testAccountID, testClusterID)
	require.NotNil(t, internal)

	result := InternalToPublicCluster(internal)
	require.NotNil(t, result)

	assert.Equal(t, original.Spec.DisplayName, result.Spec.DisplayName)
	assert.Equal(t, original.Name, result.Name)
}

// --- NodePool conversion tests ---

func TestPublicToInternalNodePool_SetsMetadata(t *testing.T) {
	pub := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: testNodePoolName},
		Spec:       public.NodePoolSpec{DisplayName: "Test NodePool"},
	}

	// NodePool internalPoolID is tied to its name (cr.Name used as both ID and Name)
	result := PublicToInternalNodePool(pub, testAccountID, testClusterID, testNodePoolName)

	require.NotNil(t, result)
	assert.Equal(t, testNodePoolName, result.Name)
	assert.Equal(t, clusterNamespace(testClusterID), result.Namespace)
	assert.Equal(t, testAccountID, result.Labels["hyperfleet.io/account-id"])
}

func TestPublicToInternalNodePool_InjectsServiceSetFields(t *testing.T) {
	pub := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: testNodePoolName},
		Spec:       public.NodePoolSpec{DisplayName: "Test NodePool"},
	}

	result := PublicToInternalNodePool(pub, testAccountID, testClusterID, testNodePoolName)

	require.NotNil(t, result)
	assert.Equal(t, testAccountID, result.Spec.AccountID)
	assert.Equal(t, testNodePoolName, result.Spec.InternalPoolID)
}

func TestPublicToInternalNodePool_SyncsAutoRepairToPassthrough(t *testing.T) {
	autoRepair := true
	pub := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: testNodePoolName},
		Spec: public.NodePoolSpec{
			AutoRepair: &autoRepair,
			Labels:     map[string]string{"env": "test"},
		},
	}

	result := PublicToInternalNodePool(pub, testAccountID, testClusterID, testNodePoolName)

	require.NotNil(t, result)
	assert.Equal(t, true, result.Spec.NodePool.Management.AutoRepair)
	assert.Equal(t, map[string]string{"env": "test"}, result.Spec.NodePool.NodeLabels)
}

func TestPublicToInternalNodePool_DefaultsAutoRepairToTrue(t *testing.T) {
	pub := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: testNodePoolName},
		Spec:       public.NodePoolSpec{DisplayName: "Test NodePool"},
	}

	result := PublicToInternalNodePool(pub, testAccountID, testClusterID, testNodePoolName)

	require.NotNil(t, result)
	// Matches operator default behavior
	assert.Equal(t, true, result.Spec.NodePool.Management.AutoRepair)
}

func TestPublicToInternalNodePool_NilInput(t *testing.T) {
	result := PublicToInternalNodePool(nil, testAccountID, testClusterID, testNodePoolName)
	assert.Nil(t, result)
}

func TestInternalToPublicNodePool_FiltersServiceSetFields(t *testing.T) {
	cr := &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testNodePoolName,
			Namespace: clusterNamespace(testClusterID),
			Labels:    map[string]string{"hyperfleet.io/account-id": testAccountID},
		},
		Spec: hyperfleetv1alpha1.NodePoolSpec{
			AccountID:      testAccountID,
			InternalPoolID: testNodePoolName,
			DisplayName:    "Test NodePool",
			AutoRepair:     ptrBool(true),
			Labels:         map[string]string{"env": "test"},
			NodePool: hyperfleetv1alpha1.NodePoolSpecPassthrough{
				Management: hypershiftv1beta1.NodePoolManagement{AutoRepair: true},
				NodeLabels: map[string]string{"env": "test"},
			},
		},
	}

	result := InternalToPublicNodePool(cr)

	require.NotNil(t, result)
	assert.Equal(t, testNodePoolName, result.Name)
	// Service-set fields absent; user-visible fields present
	require.NotNil(t, result.Spec.AutoRepair)
	assert.Equal(t, true, *result.Spec.AutoRepair)
	assert.Equal(t, map[string]string{"env": "test"}, result.Spec.Labels)
}

func TestInternalToPublicNodePool_NilInput(t *testing.T) {
	result := InternalToPublicNodePool(nil)
	assert.Nil(t, result)
}

func TestNodePoolRoundTrip(t *testing.T) {
	autoRepair := true
	original := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: testNodePoolName},
		Spec: public.NodePoolSpec{
			DisplayName: "Test NodePool",
			AutoRepair:  &autoRepair,
			Labels:      map[string]string{"env": "test"},
		},
	}

	internal := PublicToInternalNodePool(original, testAccountID, testClusterID, testNodePoolName)
	require.NotNil(t, internal)

	// Passthrough synced correctly
	assert.Equal(t, true, internal.Spec.NodePool.Management.AutoRepair)
	assert.Equal(t, original.Spec.Labels, internal.Spec.NodePool.NodeLabels)

	result := InternalToPublicNodePool(internal)
	require.NotNil(t, result)

	require.NotNil(t, result.Spec.AutoRepair)
	assert.Equal(t, *original.Spec.AutoRepair, *result.Spec.AutoRepair)
	assert.Equal(t, original.Spec.Labels, result.Spec.Labels)
}

// --- enrichMetadata tests ---

func TestEnrichMetadata_SetsFields(t *testing.T) {
	meta := &metav1.ObjectMeta{Name: "res"}

	enrichMetadata(meta, testClusterID, testClusterID, testAccountID)

	assert.Equal(t, clusterNamespace(testClusterID), meta.Namespace)
	assert.Equal(t, types.UID(testClusterID), meta.UID)
	assert.Equal(t, testAccountID, meta.Labels["hyperfleet.io/account-id"])
}

func TestEnrichMetadata_PreservesExistingLabels(t *testing.T) {
	meta := &metav1.ObjectMeta{
		Name:   "res",
		Labels: map[string]string{"app": "test"},
	}

	enrichMetadata(meta, testClusterID, testClusterID, testAccountID)

	assert.Equal(t, "test", meta.Labels["app"])
	assert.Equal(t, testAccountID, meta.Labels["hyperfleet.io/account-id"])
}

// --- Input mutation regression tests ---

// TestPublicToInternalCluster_DoesNotMutateInput verifies that conversion leaves
// pub.ObjectMeta and its labels unchanged. The test catches mutation because
// enrichMetadata injects "hyperfleet.io/account-id" — if pub.Labels were modified
// in-place that key would appear in pub.Labels after the call.
func TestPublicToInternalCluster_DoesNotMutateInput(t *testing.T) {
	pub := &public.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName,
			Namespace: "",
			Labels:    map[string]string{"user-key": "user-val"},
		},
	}

	cr := PublicToInternalCluster(pub, testAccountID, testClusterID)

	// CRD must carry the enriched metadata
	assert.Equal(t, clusterNamespace(testClusterID), cr.Namespace)
	assert.Equal(t, testAccountID, cr.Labels["hyperfleet.io/account-id"])

	// pub.ObjectMeta must be unchanged
	assert.Equal(t, "", pub.Namespace)
	assert.Equal(t, types.UID(""), pub.UID)
	_, hasAccountLabel := pub.Labels["hyperfleet.io/account-id"]
	assert.False(t, hasAccountLabel, "pub.Labels must not be mutated by conversion")
	assert.Equal(t, "user-val", pub.Labels["user-key"])
}

// TestPublicToInternalNodePool_DoesNotMutateInput verifies that conversion leaves
// pub.ObjectMeta and its labels unchanged.
func TestPublicToInternalNodePool_DoesNotMutateInput(t *testing.T) {
	pub := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testNodePoolName,
			Namespace: "",
			Labels:    map[string]string{"user-key": "user-val"},
		},
	}

	np := PublicToInternalNodePool(pub, testAccountID, testClusterID, testNodePoolName)

	// CRD must carry the enriched metadata
	assert.Equal(t, clusterNamespace(testClusterID), np.Namespace)
	assert.Equal(t, testAccountID, np.Labels["hyperfleet.io/account-id"])

	// pub.ObjectMeta must be unchanged
	assert.Equal(t, "", pub.Namespace)
	assert.Equal(t, types.UID(""), pub.UID)
	_, hasAccountLabel := pub.Labels["hyperfleet.io/account-id"]
	assert.False(t, hasAccountLabel, "pub.Labels must not be mutated by conversion")
	assert.Equal(t, "user-val", pub.Labels["user-key"])
}

// --- syncNodePoolPassthrough tests ---

func TestSyncNodePoolPassthrough_SyncsAutoRepairAndLabels(t *testing.T) {
	autoRepair := true
	spec := &hyperfleetv1alpha1.NodePoolSpec{
		AutoRepair: &autoRepair,
		Labels:     map[string]string{"env": "test"},
	}

	syncNodePoolPassthrough(spec, spec.AutoRepair, spec.Labels)

	assert.Equal(t, true, spec.NodePool.Management.AutoRepair)
	assert.Equal(t, spec.Labels, spec.NodePool.NodeLabels)
}

func TestSyncNodePoolPassthrough_DefaultsAutoRepairWhenNil(t *testing.T) {
	spec := &hyperfleetv1alpha1.NodePoolSpec{}

	syncNodePoolPassthrough(spec, nil, nil)

	assert.Equal(t, true, spec.NodePool.Management.AutoRepair)
}

func TestSyncNodePoolPassthrough_NilSpec(t *testing.T) {
	// Must not panic
	syncNodePoolPassthrough(nil, nil, nil)
}

// --- namespace helpers ---

func TestNamespaceRoundTrip(t *testing.T) {
	for _, id := range []string{testClusterID, "00000000-0000-0000-0000-000000000000"} {
		ns := clusterNamespace(id)
		assert.Equal(t, id, clusterIDFromNamespace(ns), "clusterID not preserved through namespace roundtrip")
	}
}

func TestNamespaceLength(t *testing.T) {
	ns := clusterNamespace(testClusterID)
	assert.LessOrEqual(t, len(ns), 63, "namespace exceeds K8s 63-char limit")
}

func ptrBool(b bool) *bool { return &b }
