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
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/conversion"
	v1alpha1conv "github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/conversion/v1alpha1"
)

const (
	testClusterUID   = "550e8400-e29b-41d4-a716-446655440000"
	testAccountID    = "123456789012"
	testClusterName  = "test-cluster"
	testNodePoolName = "test-cluster.workers"
)

func testParentCluster() *hyperfleetv1alpha1.Cluster {
	return &hyperfleetv1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name:      testClusterName,
		Namespace: hyperfleetv1alpha1.AccountNamespace(testAccountID),
		UID:       types.UID(testClusterUID),
	}}
}

// --- Cluster conversion tests ---

func TestPublicToInternalCluster_KeepsOnlyClientMetadata(t *testing.T) {
	pub := &public.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:            testClusterName,
			Namespace:       "account-" + testAccountID,
			UID:             "client-uid",
			Finalizers:      []string{"x"},
			OwnerReferences: []metav1.OwnerReference{{Name: "other"}},
			Labels:          map[string]string{"team": "a", hyperfleetv1alpha1.ClusterUIDLabel: "spoofed"},
			Annotations:     map[string]string{"note": "hi"},
		},
		Spec: public.ClusterSpec{
			DisplayName: "Test Cluster",
		},
	}
	result := PublicToInternalCluster(pub, testAccountID)
	require.NotNil(t, result)
	assert.Equal(t, testClusterName, result.Name)
	assert.Equal(t, "account-"+testAccountID, result.Namespace)
	assert.Empty(t, result.UID, "uid is minted by the database")
	assert.Empty(t, result.Finalizers)
	assert.Empty(t, result.OwnerReferences)
	assert.Equal(t, map[string]string{"team": "a"}, result.Labels)
	assert.Equal(t, map[string]string{"note": "hi"}, result.Annotations)
}

func TestPublicToInternalCluster_InjectsServiceSetFields(t *testing.T) {
	pub := &public.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName},
		Spec:       public.ClusterSpec{DisplayName: "Test Cluster"},
	}
	result := PublicToInternalCluster(pub, testAccountID)
	require.NotNil(t, result)
	assert.Equal(t, testAccountID, result.Spec.AccountID)
}

func TestUnprojectCluster_DNSFieldsNestedCorrectly(t *testing.T) {
	baseDomainPrefix := "my-cluster"
	pub := &public.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: testClusterName},
		Spec:       public.ClusterSpec{DisplayName: "Test Cluster"},
	}

	// Import conversion package for direct UnprojectCluster call
	enrichment := &conversion.ServiceSetFields{
		AccountID: testAccountID,
		HostedCluster: &conversion.ServiceSetFieldsHostedCluster{
			DNS: &conversion.ServiceSetFieldsDNS{
				BaseDomain:       "example.com",
				BaseDomainPrefix: &baseDomainPrefix,
				PrivateZoneID:    "Z1234PRIVATE",
				PublicZoneID:     "Z5678PUBLIC",
			},
			KubeAPIServerDNSName: "api.my-cluster.example.com",
		},
	}

	result := v1alpha1conv.UnprojectCluster(&pub.Spec, enrichment)

	require.NotNil(t, result)
	// Verify DNS fields are nested under HostedCluster.DNS
	assert.Equal(t, "example.com", result.HostedCluster.DNS.BaseDomain)
	assert.Equal(t, &baseDomainPrefix, result.HostedCluster.DNS.BaseDomainPrefix)
	assert.Equal(t, "Z1234PRIVATE", result.HostedCluster.DNS.PrivateZoneID)
	assert.Equal(t, "Z5678PUBLIC", result.HostedCluster.DNS.PublicZoneID)
	// Verify KubeAPIServerDNSName is at HostedCluster level
	assert.Equal(t, "api.my-cluster.example.com", result.HostedCluster.KubeAPIServerDNSName)
	// Verify other service-set fields still work
	assert.Equal(t, testAccountID, result.AccountID)
}

func TestPublicToInternalCluster_NilInput(t *testing.T) {
	result := PublicToInternalCluster(nil, testAccountID)
	assert.Nil(t, result)
}

func TestInternalToPublicCluster_FiltersServiceSetFields(t *testing.T) {
	cr := &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testClusterName,
			Namespace: hyperfleetv1alpha1.AccountNamespace(testAccountID),
			UID:       types.UID(testClusterUID),
		},
		Spec: hyperfleetv1alpha1.ClusterSpec{
			AccountID:   testAccountID,
			DisplayName: "Test Cluster",
		},
	}

	result := InternalToPublicCluster(cr)

	require.NotNil(t, result)
	assert.Equal(t, testClusterName, result.Name)
	assert.Equal(t, types.UID(testClusterUID), result.UID, "the public uid is the stored uid")
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

	internal := PublicToInternalCluster(original, testAccountID)
	require.NotNil(t, internal)

	result := InternalToPublicCluster(internal)
	require.NotNil(t, result)

	assert.Equal(t, original.Spec.DisplayName, result.Spec.DisplayName)
	assert.Equal(t, original.Name, result.Name)
}

// --- NodePool conversion tests ---

func TestPublicToInternalNodePool_SetsOwner(t *testing.T) {
	pub := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:            testNodePoolName,
			Labels:          map[string]string{hyperfleetv1alpha1.ClusterUIDLabel: "spoofed"},
			OwnerReferences: []metav1.OwnerReference{{Name: "other", UID: "spoofed"}},
		},
		Spec: public.NodePoolSpec{DisplayName: "Test NodePool"},
	}
	result := PublicToInternalNodePool(pub, testAccountID, testParentCluster())
	require.NotNil(t, result)
	assert.Equal(t, testNodePoolName, result.Name)
	assert.Equal(t, testClusterUID, result.Labels[hyperfleetv1alpha1.ClusterUIDLabel])
	owner := metav1.GetControllerOf(result)
	require.NotNil(t, owner)
	assert.Len(t, result.OwnerReferences, 1)
	assert.Equal(t, types.UID(testClusterUID), owner.UID)
	assert.Equal(t, testClusterName, owner.Name)
	assert.Equal(t, "Cluster", owner.Kind)
	assert.Equal(t, hyperfleetv1alpha1.GroupVersion.String(), owner.APIVersion)
}

func TestPublicToInternalNodePool_InjectsServiceSetFields(t *testing.T) {
	pub := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: testNodePoolName},
		Spec:       public.NodePoolSpec{DisplayName: "Test NodePool"},
	}
	result := PublicToInternalNodePool(pub, testAccountID, testParentCluster())
	require.NotNil(t, result)
	assert.Equal(t, testAccountID, result.Spec.AccountID)
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

	result := PublicToInternalNodePool(pub, testAccountID, testParentCluster())

	require.NotNil(t, result)
	assert.Equal(t, true, result.Spec.NodePool.Management.AutoRepair)
	assert.Equal(t, map[string]string{"env": "test"}, result.Spec.NodePool.NodeLabels)
}

func TestPublicToInternalNodePool_DefaultsAutoRepairToTrue(t *testing.T) {
	pub := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: testNodePoolName},
		Spec:       public.NodePoolSpec{DisplayName: "Test NodePool"},
	}

	result := PublicToInternalNodePool(pub, testAccountID, testParentCluster())

	require.NotNil(t, result)
	// Matches operator default behavior
	assert.Equal(t, true, result.Spec.NodePool.Management.AutoRepair)
}

func TestPublicToInternalNodePool_NilInput(t *testing.T) {
	result := PublicToInternalNodePool(nil, testAccountID, testParentCluster())
	assert.Nil(t, result)
}

func TestInternalToPublicNodePool_FiltersServiceSetFields(t *testing.T) {
	cr := &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      testNodePoolName,
			Namespace: hyperfleetv1alpha1.AccountNamespace(testAccountID),
		},
		Spec: hyperfleetv1alpha1.NodePoolSpec{
			AccountID:   testAccountID,
			DisplayName: "Test NodePool",
			AutoRepair:  ptrBool(true),
			Labels:      map[string]string{"env": "test"},
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

	internal := PublicToInternalNodePool(original, testAccountID, testParentCluster())
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

// --- Input mutation regression tests ---

// TestPublicToInternalCluster_DoesNotMutateInput verifies that conversion leaves
// pub.ObjectMeta and its labels unchanged: dropping a reserved label must not
// delete it from the caller's map, and the result must not share it.
func TestPublicToInternalCluster_DoesNotMutateInput(t *testing.T) {
	pub := &public.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:   testClusterName,
			Labels: map[string]string{"user-key": "user-val", hyperfleetv1alpha1.ClusterUIDLabel: "x"},
		},
	}

	cr := PublicToInternalCluster(pub, testAccountID)
	cr.Labels["added"] = "y"

	assert.Equal(t, map[string]string{"user-key": "user-val", hyperfleetv1alpha1.ClusterUIDLabel: "x"}, pub.Labels,
		"pub.Labels must not be mutated by conversion")
}

// TestPublicToInternalNodePool_DoesNotMutateInput verifies that setting the owner
// on the result leaves pub.ObjectMeta unchanged.
func TestPublicToInternalNodePool_DoesNotMutateInput(t *testing.T) {
	pub := &public.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:   testNodePoolName,
			Labels: map[string]string{"user-key": "user-val"},
		},
	}

	np := PublicToInternalNodePool(pub, testAccountID, testParentCluster())
	assert.Equal(t, testClusterUID, np.Labels[hyperfleetv1alpha1.ClusterUIDLabel])

	assert.Equal(t, map[string]string{"user-key": "user-val"}, pub.Labels, "pub.Labels must not be mutated by conversion")
	assert.Empty(t, pub.OwnerReferences)
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

func ptrBool(b bool) *bool { return &b }
