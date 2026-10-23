package seaplanedb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	seaplanev1alpha1 "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = seaplanev1alpha1.AddToScheme(s)
	return s
}

func newUIDIndexedClient(t *testing.T) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithIndex(&seaplanev1alpha1.DNSReservation{}, "metadata.uid", func(obj client.Object) []string {
			return []string{string(obj.GetUID())}
		}).
		WithIndex(&seaplanev1alpha1.OidcConfig{}, "metadata.uid", func(obj client.Object) []string {
			return []string{string(obj.GetUID())}
		}).
		WithStatusSubresource(&seaplanev1alpha1.DNSReservation{}, &seaplanev1alpha1.OidcConfig{}).
		Build()
}

func TestClient_CreateCluster_SetsAccountLabel(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	cluster := &seaplanev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "account-wrong-account",
		},
	}

	if err := c.CreateCluster(context.Background(), "acct-123", cluster); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if got := cluster.Labels[accountIDLabel]; got != "acct-123" {
		t.Errorf("account-id label = %q, want %q", got, "acct-123")
	}
	if cluster.Namespace != accountNamespace("acct-123") {
		t.Errorf("namespace = %q, want %q", cluster.Namespace, accountNamespace("acct-123"))
	}
}

func TestClient_CreateNodePool_SetsNamespaceAndLabel(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	np := &seaplanev1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-nodepool",
			Namespace: "account-wrong-account",
		},
		Spec: seaplanev1alpha1.NodePoolSpec{},
	}

	if err := c.CreateNodePool(context.Background(), "acct-123", np); err != nil {
		t.Fatalf("CreateNodePool: %v", err)
	}

	if got := np.Labels[accountIDLabel]; got != "acct-123" {
		t.Errorf("account-id label = %q, want %q", got, "acct-123")
	}
	if np.Namespace != accountNamespace("acct-123") {
		t.Errorf("namespace = %q, want %q", np.Namespace, accountNamespace("acct-123"))
	}
}

func TestClient_ListClusters_FiltersByAccount(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&seaplanev1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-cluster", Namespace: "account-acct-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
		&seaplanev1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other-cluster", Namespace: "account-acct-2",
				Labels: map[string]string{accountIDLabel: "acct-2"},
			},
		},
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	list, err := c.ListClusters(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 cluster for acct-1, got %d", len(list.Items))
	}
	if list.Items[0].Name != "my-cluster" {
		t.Errorf("got cluster %q, want my-cluster", list.Items[0].Name)
	}
}

func TestClient_GetNodePool_ScopedToAccount(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&seaplanev1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alpha.workers", Namespace: "account-acct-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
		&seaplanev1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "beta.workers", Namespace: "account-acct-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	np, err := c.GetNodePool(context.Background(), "acct-1", "beta.workers")
	if err != nil {
		t.Fatalf("GetNodePool: %v", err)
	}
	if np.Namespace != "account-acct-1" {
		t.Errorf("namespace = %q, want account-acct-1", np.Namespace)
	}
}

func TestClient_GetNodePool_NotFoundOutsideAccount(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&seaplanev1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alpha.workers", Namespace: "account-acct-2",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
	).Build()
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	_, err := c.GetNodePool(context.Background(), "acct-1", "alpha.workers")
	if !IsNotFound(err) {
		t.Fatalf("GetNodePool across account namespace: expected NotFound, got %v", err)
	}
}

func TestClient_GetCluster_ScopedToAccount(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&seaplanev1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-cluster", Namespace: "account-acct-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	// Correct account can access
	cr, err := c.GetCluster(context.Background(), "acct-1", "my-cluster")
	if err != nil {
		t.Fatalf("GetCluster with correct account: %v", err)
	}
	if cr.Name != "my-cluster" {
		t.Errorf("got cluster %q, want my-cluster", cr.Name)
	}

	// Wrong account gets not-found
	_, err = c.GetCluster(context.Background(), "acct-2", "my-cluster")
	if !IsNotFound(err) {
		t.Errorf("GetCluster with wrong account: expected NotFound, got %v", err)
	}
}

func TestClient_ClusterUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&seaplanev1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "update-me", Namespace: "account-acct-1"}},
		&seaplanev1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "delete-object", Namespace: "account-acct-1"}},
	).Build()
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	cluster, err := c.GetCluster(ctx, "acct-1", "update-me")
	if err != nil {
		t.Fatalf("GetCluster: %v", err)
	}
	cluster.Labels = map[string]string{"updated": "true"}
	if err := c.UpdateCluster(ctx, cluster); err != nil {
		t.Fatalf("UpdateCluster: %v", err)
	}
	updated, err := c.GetCluster(ctx, "acct-1", "update-me")
	if err != nil || updated.Labels["updated"] != "true" {
		t.Fatalf("updated Cluster = %+v, err = %v", updated, err)
	}

	if err := c.DeleteCluster(ctx, "acct-1", "update-me"); err != nil {
		t.Fatalf("DeleteCluster: %v", err)
	}
	if _, err := c.GetCluster(ctx, "acct-1", "update-me"); !IsNotFound(err) {
		t.Fatalf("GetCluster after DeleteCluster error = %v, want NotFound", err)
	}

	directDelete, err := c.GetCluster(ctx, "acct-1", "delete-object")
	if err != nil {
		t.Fatalf("GetCluster before DeleteClusterObject: %v", err)
	}
	if err := c.DeleteClusterObject(ctx, directDelete); err != nil {
		t.Fatalf("DeleteClusterObject: %v", err)
	}
	if _, err := c.GetCluster(ctx, "acct-1", "delete-object"); !IsNotFound(err) {
		t.Fatalf("GetCluster after DeleteClusterObject error = %v, want NotFound", err)
	}
	if err := c.DeleteCluster(ctx, "acct-1", "missing"); !IsNotFound(err) {
		t.Fatalf("DeleteCluster missing object error = %v, want NotFound", err)
	}
}

func TestClient_NodePoolListUpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&seaplanev1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "alpha.workers", Namespace: "account-acct-1", Labels: map[string]string{clusterUIDLabel: "cluster-1"}},
		},
		&seaplanev1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "beta.workers", Namespace: "account-acct-1", Labels: map[string]string{clusterUIDLabel: "cluster-2"}},
		},
		&seaplanev1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "foreign.workers", Namespace: "account-acct-2", Labels: map[string]string{clusterUIDLabel: "cluster-1"}},
		},
	).Build()
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	filtered, err := c.ListNodePools(ctx, "acct-1", "cluster-1")
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].Name != "alpha.workers" {
		t.Fatalf("ListNodePools by cluster UID = %+v, err = %v", filtered, err)
	}
	all, err := c.ListNodePools(ctx, "acct-1", "")
	if err != nil || len(all.Items) != 2 {
		t.Fatalf("ListNodePools without cluster filter returned %d items, err = %v", len(all.Items), err)
	}

	nodepool, err := c.GetNodePool(ctx, "acct-1", "alpha.workers")
	if err != nil {
		t.Fatalf("GetNodePool: %v", err)
	}
	nodepool.Labels["updated"] = "true"
	if err := c.UpdateNodePool(ctx, nodepool); err != nil {
		t.Fatalf("UpdateNodePool: %v", err)
	}
	updated, err := c.GetNodePool(ctx, "acct-1", "alpha.workers")
	if err != nil || updated.Labels["updated"] != "true" {
		t.Fatalf("updated NodePool = %+v, err = %v", updated, err)
	}

	if err := c.DeleteNodePool(ctx, "acct-1", "alpha.workers"); err != nil {
		t.Fatalf("DeleteNodePool: %v", err)
	}
	if _, err := c.GetNodePool(ctx, "acct-1", "alpha.workers"); !IsNotFound(err) {
		t.Fatalf("GetNodePool after DeleteNodePool error = %v, want NotFound", err)
	}
	if err := c.DeleteNodePool(ctx, "acct-1", "missing.workers"); !IsNotFound(err) {
		t.Fatalf("DeleteNodePool missing object error = %v, want NotFound", err)
	}
}

func TestClient_DNSReservationOperations(t *testing.T) {
	ctx := context.Background()
	fc := newUIDIndexedClient(t)
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	reservation := &seaplanev1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "dns-one", UID: types.UID("dns-uid-1"), Namespace: "wrong-namespace"},
	}

	if err := c.CreateDNSReservation(ctx, "acct-1", reservation); err != nil {
		t.Fatalf("CreateDNSReservation: %v", err)
	}
	if reservation.Namespace != accountNamespace("acct-1") || reservation.Labels[accountIDLabel] != "acct-1" {
		t.Fatalf("created reservation namespace/labels = %q/%v", reservation.Namespace, reservation.Labels)
	}
	byName, err := c.GetDNSReservation(ctx, "acct-1", "dns-one")
	if err != nil {
		t.Fatalf("GetDNSReservation: %v", err)
	}
	byUID, err := c.GetDNSReservationByUID(ctx, "acct-1", "dns-uid-1")
	if err != nil || byUID.Name != byName.Name {
		t.Fatalf("GetDNSReservationByUID = %+v, err = %v", byUID, err)
	}
	list, err := c.ListDNSReservations(ctx, "acct-1")
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("ListDNSReservations returned %d items, err = %v", len(list.Items), err)
	}

	byName.Labels["example.com/updated"] = "true"
	if err := c.UpdateDNSReservation(ctx, byName); err != nil {
		t.Fatalf("UpdateDNSReservation: %v", err)
	}
	byName.Status.Phase = seaplanev1alpha1.DNSReservationPhaseReady
	byName.Status.BaseDomain = "dns.example.com"
	if err := c.UpdateDNSReservationStatus(ctx, byName); err != nil {
		t.Fatalf("UpdateDNSReservationStatus: %v", err)
	}
	updated, err := c.GetDNSReservation(ctx, "acct-1", "dns-one")
	if err != nil || updated.Status.BaseDomain != "dns.example.com" || updated.Labels["example.com/updated"] != "true" {
		t.Fatalf("updated DNSReservation = %+v, err = %v", updated, err)
	}

	if _, err := c.GetDNSReservationByUID(ctx, "acct-1", "missing-uid"); !IsNotFound(err) {
		t.Fatalf("GetDNSReservationByUID missing UID error = %v, want NotFound", err)
	}
	if err := c.DeleteDNSReservation(ctx, "acct-1", "dns-one"); err != nil {
		t.Fatalf("DeleteDNSReservation: %v", err)
	}
	if _, err := c.GetDNSReservation(ctx, "acct-1", "dns-one"); !IsNotFound(err) {
		t.Fatalf("GetDNSReservation after delete error = %v, want NotFound", err)
	}

	second := &seaplanev1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "dns-two", UID: types.UID("dns-uid-2")}}
	if err := c.CreateDNSReservation(ctx, "acct-1", second); err != nil {
		t.Fatalf("CreateDNSReservation second: %v", err)
	}
	if err := c.DeleteDNSReservationObject(ctx, second); err != nil {
		t.Fatalf("DeleteDNSReservationObject: %v", err)
	}
}

func TestClient_ManifestAndManagementClusterOperations(t *testing.T) {
	ctx := context.Background()
	fc := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	manifest := &seaplanev1alpha1.Manifest{ObjectMeta: metav1.ObjectMeta{Name: "my-manifest"}}
	if err := c.CreateManifest(ctx, "management", manifest); err != nil {
		t.Fatalf("CreateManifest: %v", err)
	}
	if manifest.Namespace != "management" {
		t.Fatalf("Manifest namespace = %q, want management", manifest.Namespace)
	}
	if _, err := c.GetManifest(ctx, "management", "my-manifest"); err != nil {
		t.Fatalf("GetManifest: %v", err)
	}
	if err := c.DeleteManifest(ctx, "management", "my-manifest"); err != nil {
		t.Fatalf("DeleteManifest: %v", err)
	}
	if _, err := c.GetManifest(ctx, "management", "my-manifest"); !IsNotFound(err) {
		t.Fatalf("GetManifest after delete error = %v, want NotFound", err)
	}

	managementCluster := &seaplanev1alpha1.ManagementCluster{ObjectMeta: metav1.ObjectMeta{Name: "mc-one", Namespace: "ignored"}}
	if err := c.CreateManagementCluster(ctx, managementCluster); err != nil {
		t.Fatalf("CreateManagementCluster: %v", err)
	}
	if managementCluster.Namespace != "" {
		t.Fatalf("ManagementCluster namespace = %q, want cluster-scoped empty namespace", managementCluster.Namespace)
	}
	if _, err := c.GetManagementCluster(ctx, "mc-one"); err != nil {
		t.Fatalf("GetManagementCluster: %v", err)
	}
	list, err := c.ListManagementClusters(ctx)
	if err != nil || len(list.Items) != 1 || list.Items[0].Name != "mc-one" {
		t.Fatalf("ListManagementClusters = %+v, err = %v", list, err)
	}
}

func TestClient_OidcConfigAndIssuerIndexOperations(t *testing.T) {
	ctx := context.Background()
	fc := newUIDIndexedClient(t)
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	config := &seaplanev1alpha1.OidcConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "oidc-one", UID: types.UID("oidc-uid-1"), Namespace: "wrong-namespace"},
		Spec: seaplanev1alpha1.OidcConfigSpec{
			Type:      seaplanev1alpha1.OidcConfigTypeManaged,
			AccountID: "acct-1",
		},
	}
	if err := c.CreateOidcConfig(ctx, "acct-1", config); err != nil {
		t.Fatalf("CreateOidcConfig: %v", err)
	}
	if config.Namespace != accountNamespace("acct-1") || config.Labels[accountIDLabel] != "acct-1" {
		t.Fatalf("created OIDC config namespace/labels = %q/%v", config.Namespace, config.Labels)
	}
	byName, err := c.GetOidcConfig(ctx, "acct-1", "oidc-one")
	if err != nil {
		t.Fatalf("GetOidcConfig: %v", err)
	}
	byUID, err := c.GetOidcConfigByUID(ctx, "acct-1", "oidc-uid-1")
	if err != nil || byUID.Name != byName.Name {
		t.Fatalf("GetOidcConfigByUID = %+v, err = %v", byUID, err)
	}
	list, err := c.ListOidcConfigs(ctx, "acct-1")
	if err != nil || len(list.Items) != 1 {
		t.Fatalf("ListOidcConfigs returned %d items, err = %v", len(list.Items), err)
	}
	if _, err := c.GetOidcConfigByUID(ctx, "acct-1", "missing-uid"); !IsNotFound(err) {
		t.Fatalf("GetOidcConfigByUID missing UID error = %v, want NotFound", err)
	}

	timestamp := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
	if err := c.UpdateOidcConfigLastUsedTimestamp(ctx, "acct-1", "oidc-uid-1", timestamp); err != nil {
		t.Fatalf("UpdateOidcConfigLastUsedTimestamp: %v", err)
	}
	byName, err = c.GetOidcConfig(ctx, "acct-1", "oidc-one")
	if err != nil || byName.Status.LastUsedTimestamp == nil || !byName.Status.LastUsedTimestamp.Equal(&timestamp) {
		t.Fatalf("lastUsedTimestamp = %v, err = %v", byName.Status.LastUsedTimestamp, err)
	}
	byName.Labels["seaplane.io/claimed-by-cluster-uid"] = "cluster-uid"
	if err := c.UpdateOidcConfigObject(ctx, byName); err != nil {
		t.Fatalf("UpdateOidcConfigObject: %v", err)
	}
	if err := c.DeleteOidcConfig(ctx, "acct-1", "oidc-one"); err != nil {
		t.Fatalf("DeleteOidcConfig: %v", err)
	}
	if _, err := c.GetOidcConfig(ctx, "acct-1", "oidc-one"); !IsNotFound(err) {
		t.Fatalf("GetOidcConfig after delete error = %v, want NotFound", err)
	}

	second := &seaplanev1alpha1.OidcConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "oidc-two", UID: types.UID("oidc-uid-2")},
		Spec:       seaplanev1alpha1.OidcConfigSpec{Type: seaplanev1alpha1.OidcConfigTypeManaged},
	}
	if err := c.CreateOidcConfig(ctx, "acct-1", second); err != nil {
		t.Fatalf("CreateOidcConfig second: %v", err)
	}
	if err := c.DeleteOidcConfigObject(ctx, second); err != nil {
		t.Fatalf("DeleteOidcConfigObject: %v", err)
	}

	index := &seaplanev1alpha1.Index{ObjectMeta: metav1.ObjectMeta{
		Name:      "issuer-index",
		Namespace: seaplanev1alpha1.OidcIssuerReservationsNamespace,
	}}
	if err := fc.Create(ctx, index); err != nil {
		t.Fatalf("create issuer index: %v", err)
	}
	if _, err := c.GetOidcIssuerIndex(ctx, index.Name); err != nil {
		t.Fatalf("GetOidcIssuerIndex: %v", err)
	}
	if _, err := c.GetOidcIssuerIndex(ctx, "missing-index"); !IsNotFound(err) {
		t.Fatalf("GetOidcIssuerIndex missing error = %v, want NotFound", err)
	}
}

func TestClientClose(t *testing.T) {
	closed := false
	c := &Client{close: func() { closed = true }}
	c.Close()
	if !closed {
		t.Error("Close did not invoke the underlying cleanup")
	}
}

func TestClientErrorPredicates(t *testing.T) {
	resource := schema.GroupResource{Group: "seaplane.io", Resource: "clusters"}
	if !IsAlreadyExists(apierrors.NewAlreadyExists(resource, "my-cluster")) {
		t.Error("IsAlreadyExists did not recognize an AlreadyExists error")
	}
	if !IsConflict(apierrors.NewConflict(resource, "my-cluster", fmt.Errorf("resource version changed"))) {
		t.Error("IsConflict did not recognize a Conflict error")
	}
	if IsAlreadyExists(fmt.Errorf("ordinary error")) || IsConflict(fmt.Errorf("ordinary error")) {
		t.Error("error predicates matched an ordinary error")
	}
}

func TestNewClient_RequiresDSN(t *testing.T) {
	_, err := NewClient(context.Background(), "", slog.Default())
	if err == nil || !strings.Contains(err.Error(), "DSN is required") {
		t.Fatalf("NewClient error = %v, want missing DSN error", err)
	}
}

const (
	spotQueueTestAccountID = "123456789012"
	spotQueueTestURL       = "https://sqs.us-east-1.amazonaws.com/123456789012/spot-termination"
)

func spotQueueTestReservation(owner string, claimedAt time.Time) *seaplanev1alpha1.Index {
	return &seaplanev1alpha1.Index{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: seaplanev1alpha1.SpotTerminationQueueReservationsNamespace,
			Name:      seaplanev1alpha1.SpotTerminationQueueIndexName(spotQueueTestAccountID, spotQueueTestURL),
			Labels:    map[string]string{seaplanev1alpha1.SpotTerminationQueueOwnerLabel: owner},
			Annotations: map[string]string{
				seaplanev1alpha1.SpotTerminationQueueClaimedAtAnnotation: claimedAt.UTC().Format(time.RFC3339Nano),
			},
		},
	}
}

func newSpotQueueTestClient(objects ...client.Object) (*Client, client.Client) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(objects...).Build()
	return NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil))), fc
}

func TestClient_ClaimSpotTerminationQueueCreatesReservation(t *testing.T) {
	c, fc := newSpotQueueTestClient()
	created, err := c.ClaimSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1")
	if err != nil || !created {
		t.Fatalf("ClaimSpotTerminationQueue() = (%t, %v), want (true, nil)", created, err)
	}

	var reservation seaplanev1alpha1.Index
	key := types.NamespacedName{
		Namespace: seaplanev1alpha1.SpotTerminationQueueReservationsNamespace,
		Name:      seaplanev1alpha1.SpotTerminationQueueIndexName(spotQueueTestAccountID, spotQueueTestURL),
	}
	if err := fc.Get(context.Background(), key, &reservation); err != nil {
		t.Fatalf("getting created reservation: %v", err)
	}
	if got := reservation.Labels[seaplanev1alpha1.SpotTerminationQueueOwnerLabel]; got != spotQueueOwnerKey("cluster-1") {
		t.Errorf("reservation owner = %q, want %q", got, spotQueueOwnerKey("cluster-1"))
	}
	if _, err := time.Parse(time.RFC3339Nano, reservation.Annotations[seaplanev1alpha1.SpotTerminationQueueClaimedAtAnnotation]); err != nil {
		t.Errorf("reservation timestamp is invalid: %v", err)
	}
}

func TestClient_ClaimSpotTerminationQueueRefreshesSameOwner(t *testing.T) {
	reservation := spotQueueTestReservation(spotQueueOwnerKey("cluster-1"), time.Now().Add(-time.Hour))
	reservation.Annotations = nil
	c, fc := newSpotQueueTestClient(reservation)
	created, err := c.ClaimSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1")
	if err != nil || created {
		t.Fatalf("ClaimSpotTerminationQueue() = (%t, %v), want (false, nil)", created, err)
	}

	var updated seaplanev1alpha1.Index
	key := types.NamespacedName{Namespace: reservation.Namespace, Name: reservation.Name}
	if err := fc.Get(context.Background(), key, &updated); err != nil {
		t.Fatalf("getting refreshed reservation: %v", err)
	}
	if updated.Annotations[seaplanev1alpha1.SpotTerminationQueueClaimedAtAnnotation] == "" {
		t.Error("expected refresh to initialize the claim timestamp")
	}
}

func TestClient_ClaimSpotTerminationQueueRejectsActiveOwner(t *testing.T) {
	reservation := spotQueueTestReservation(spotQueueOwnerKey("owner"), time.Now())
	owner := spotQueueTestCluster("owner", spotQueueTestURL)
	c, _ := newSpotQueueTestClient(reservation, owner)
	created, err := c.ClaimSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "other")
	if created || !IsAlreadyExists(err) {
		t.Fatalf("ClaimSpotTerminationQueue() = (%t, %v), want an already-exists error", created, err)
	}
}

func TestClient_ClaimSpotTerminationQueueReclaimsExpiredReservation(t *testing.T) {
	reservation := spotQueueTestReservation(spotQueueOwnerKey("deleted-cluster"), time.Now().Add(-2*spotQueueClaimTimeout))
	c, fc := newSpotQueueTestClient(reservation)
	created, err := c.ClaimSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-new")
	if err != nil || !created {
		t.Fatalf("ClaimSpotTerminationQueue() = (%t, %v), want (true, nil)", created, err)
	}

	var current seaplanev1alpha1.Index
	key := types.NamespacedName{Namespace: reservation.Namespace, Name: reservation.Name}
	if err := fc.Get(context.Background(), key, &current); err != nil {
		t.Fatalf("getting reclaimed reservation: %v", err)
	}
	if got := current.Labels[seaplanev1alpha1.SpotTerminationQueueOwnerLabel]; got != spotQueueOwnerKey("cluster-new") {
		t.Errorf("reservation owner = %q, want %q", got, spotQueueOwnerKey("cluster-new"))
	}
}

func TestClient_ClaimSpotTerminationQueueKeepsRecentReservation(t *testing.T) {
	reservation := spotQueueTestReservation(spotQueueOwnerKey("deleted-cluster"), time.Now())
	c, _ := newSpotQueueTestClient(reservation)
	created, err := c.ClaimSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-new")
	if created || !IsAlreadyExists(err) {
		t.Fatalf("ClaimSpotTerminationQueue() = (%t, %v), want a recent-reservation conflict", created, err)
	}
}

func TestClient_ClaimSpotTerminationQueueReturnsClientErrors(t *testing.T) {
	wantErr := errors.New("storage failure")
	t.Run("create", func(t *testing.T) {
		c := NewClientFrom(fake.NewClientBuilder().WithScheme(testScheme()).WithInterceptorFuncs(interceptor.Funcs{
			Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error { return wantErr },
		}).Build(), slog.New(slog.NewTextHandler(os.Stdout, nil)))
		if _, err := c.ClaimSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1"); !errors.Is(err, wantErr) {
			t.Fatalf("ClaimSpotTerminationQueue() error = %v, want %v", err, wantErr)
		}
	})
	t.Run("get existing reservation", func(t *testing.T) {
		reservation := spotQueueTestReservation(spotQueueOwnerKey("owner"), time.Now())
		c := NewClientFrom(fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(reservation).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, client.WithWatch, types.NamespacedName, client.Object, ...client.GetOption) error {
				return wantErr
			},
		}).Build(), slog.New(slog.NewTextHandler(os.Stdout, nil)))
		if _, err := c.ClaimSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1"); !errors.Is(err, wantErr) {
			t.Fatalf("ClaimSpotTerminationQueue() error = %v, want %v", err, wantErr)
		}
	})
	t.Run("refresh existing reservation", func(t *testing.T) {
		reservation := spotQueueTestReservation(spotQueueOwnerKey("cluster-1"), time.Now())
		c := NewClientFrom(fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(reservation).WithInterceptorFuncs(interceptor.Funcs{
			Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error { return wantErr },
		}).Build(), slog.New(slog.NewTextHandler(os.Stdout, nil)))
		if _, err := c.ClaimSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1"); !errors.Is(err, wantErr) {
			t.Fatalf("ClaimSpotTerminationQueue() error = %v, want %v", err, wantErr)
		}
	})
	t.Run("release stale reservation", func(t *testing.T) {
		reservation := spotQueueTestReservation(spotQueueOwnerKey("deleted-cluster"), time.Now().Add(-2*spotQueueClaimTimeout))
		c := NewClientFrom(fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(reservation).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error { return wantErr },
		}).Build(), slog.New(slog.NewTextHandler(os.Stdout, nil)))
		if _, err := c.ClaimSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1"); !errors.Is(err, wantErr) {
			t.Fatalf("ClaimSpotTerminationQueue() error = %v, want %v", err, wantErr)
		}
	})
}

func TestSpotQueueHelpers(t *testing.T) {
	if !spotQueueClaimExpired(spotQueueTestReservation("owner", time.Now().Add(-2*spotQueueClaimTimeout))) {
		t.Error("expected old claim to be expired")
	}
	missingTimestamp := spotQueueTestReservation("owner", time.Time{})
	missingTimestamp.Annotations = nil
	if spotQueueClaimExpired(missingTimestamp) {
		t.Error("claim without a timestamp must not be expired")
	}
	if clusterUsesSpotTerminationQueue(nil, spotQueueTestURL) {
		t.Error("nil cluster must not use the queue")
	}
	if clusterUsesSpotTerminationQueue(&seaplanev1alpha1.Cluster{}, spotQueueTestURL) {
		t.Error("cluster without AWS platform must not use the queue")
	}
	cluster := &seaplanev1alpha1.Cluster{Spec: seaplanev1alpha1.ClusterSpec{HostedCluster: seaplanev1alpha1.HostedClusterSpecPassthrough{
		Platform: hypershiftPlatformWithQueue(spotQueueTestURL),
	}}}
	if !clusterUsesSpotTerminationQueue(cluster, spotQueueTestURL) || clusterUsesSpotTerminationQueue(cluster, "other") {
		t.Error("queue usage helper returned an unexpected result")
	}
}

func TestClient_ReleaseSpotTerminationQueue(t *testing.T) {
	t.Run("missing reservation", func(t *testing.T) {
		c, _ := newSpotQueueTestClient()
		if err := c.ReleaseSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1"); err != nil {
			t.Fatalf("ReleaseSpotTerminationQueue() error = %v, want nil", err)
		}
	})
	t.Run("wrong owner", func(t *testing.T) {
		reservation := spotQueueTestReservation(spotQueueOwnerKey("cluster-other"), time.Now())
		c, _ := newSpotQueueTestClient(reservation)
		if err := c.ReleaseSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1"); err == nil {
			t.Fatal("ReleaseSpotTerminationQueue() error = nil, want ownership error")
		}
	})
	t.Run("cluster still uses queue", func(t *testing.T) {
		reservation := spotQueueTestReservation(spotQueueOwnerKey("cluster-1"), time.Now())
		cluster := spotQueueTestCluster("cluster-1", spotQueueTestURL)
		c, fc := newSpotQueueTestClient(reservation, cluster)
		if err := c.ReleaseSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1"); err != nil {
			t.Fatalf("ReleaseSpotTerminationQueue() error = %v, want nil", err)
		}
		var found seaplanev1alpha1.Index
		if err := fc.Get(context.Background(), types.NamespacedName{Namespace: reservation.Namespace, Name: reservation.Name}, &found); err != nil {
			t.Errorf("reservation was deleted while cluster still uses it: %v", err)
		}
	})
	t.Run("cluster no longer uses queue", func(t *testing.T) {
		reservation := spotQueueTestReservation(spotQueueOwnerKey("cluster-1"), time.Now())
		cluster := spotQueueTestCluster("cluster-1", "")
		c, fc := newSpotQueueTestClient(reservation, cluster)
		if err := c.ReleaseSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1"); err != nil {
			t.Fatalf("ReleaseSpotTerminationQueue() error = %v, want nil", err)
		}
		var found seaplanev1alpha1.Index
		if err := fc.Get(context.Background(), types.NamespacedName{Namespace: reservation.Namespace, Name: reservation.Name}, &found); !IsNotFound(err) {
			t.Errorf("reservation still exists after release: %v", err)
		}
	})
	t.Run("delete failure", func(t *testing.T) {
		wantErr := errors.New("delete failure")
		reservation := spotQueueTestReservation(spotQueueOwnerKey("cluster-1"), time.Now())
		cluster := spotQueueTestCluster("cluster-1", "")
		c := NewClientFrom(fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(reservation, cluster).WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error { return wantErr },
		}).Build(), slog.New(slog.NewTextHandler(os.Stdout, nil)))
		if err := c.ReleaseSpotTerminationQueue(context.Background(), spotQueueTestAccountID, spotQueueTestURL, "cluster-1"); !errors.Is(err, wantErr) {
			t.Fatalf("ReleaseSpotTerminationQueue() error = %v, want %v", err, wantErr)
		}
	})
}

func spotQueueTestCluster(clusterName, queueURL string) *seaplanev1alpha1.Cluster {
	return &seaplanev1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: accountNamespace(spotQueueTestAccountID),
			Labels:    map[string]string{accountIDLabel: spotQueueTestAccountID},
		},
		Spec: seaplanev1alpha1.ClusterSpec{HostedCluster: seaplanev1alpha1.HostedClusterSpecPassthrough{
			Platform: hypershiftPlatformWithQueue(queueURL),
		}},
	}
}

func hypershiftPlatformWithQueue(queueURL string) hypershiftv1beta1.PlatformSpec {
	return hypershiftv1beta1.PlatformSpec{
		AWS: &hypershiftv1beta1.AWSPlatformSpec{TerminationHandlerQueueURL: queueURL},
	}
}

func TestClient_DNSDomainReservationLifecycle(t *testing.T) {
	ctx := context.Background()
	fc := newUIDIndexedClient(t)
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	index := &seaplanev1alpha1.Index{
		ObjectMeta: metav1.ObjectMeta{Name: "abc12345", Namespace: "dns-shard-0-reservations"},
	}
	if err := c.CreateDNSDomainIndex(ctx, "acct-1", index); err != nil {
		t.Fatalf("CreateDNSDomainIndex: %v", err)
	}
	if index.Labels[accountIDLabel] != "acct-1" {
		t.Fatalf("index account label = %#v", index.Labels)
	}

	reservation := &seaplanev1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: "0-abc12345",
			UID:  types.UID("550e8400-e29b-41d4-a716-446655440000"),
		},
		Spec: seaplanev1alpha1.DNSReservationSpec{
			BaseDomain:            "abc12345.0.example.com",
			ClusterArch:           "hcp",
			UserDefined:           true,
			ClusterDeletionPolicy: seaplanev1alpha1.DNSReservationClusterDeletionPolicyRetain,
		},
	}
	if err := c.CreateDNSDomainReservation(ctx, "acct-1", reservation); err != nil {
		t.Fatalf("CreateDNSDomainReservation: %v", err)
	}
	if reservation.Namespace != accountNamespace("acct-1") || reservation.Labels[accountIDLabel] != "acct-1" {
		t.Fatalf("reservation namespace/labels = %q/%#v", reservation.Namespace, reservation.Labels)
	}
	if err := c.SetDNSDomainIndexOwner(ctx, index.Namespace, index.Name, string(reservation.UID)); err != nil {
		t.Fatalf("SetDNSDomainIndexOwner: %v", err)
	}

	var updatedIndex seaplanev1alpha1.Index
	if err := fc.Get(ctx, client.ObjectKeyFromObject(index), &updatedIndex); err != nil {
		t.Fatalf("get DNS domain index: %v", err)
	}
	if updatedIndex.Labels[ownerUIDLabel] != string(reservation.UID) {
		t.Fatalf("index owner label = %#v", updatedIndex.Labels)
	}

	reservations, err := c.ListDNSDomainReservations(ctx, "acct-1")
	if err != nil || len(reservations.Items) != 1 {
		t.Fatalf("ListDNSDomainReservations = (%v, %v), want one reservation", reservations, err)
	}

	reservation.Labels[claimedByClusterUIDLabel] = "cluster-uid"
	if err := c.UpdateDNSReservation(ctx, reservation); err != nil {
		t.Fatalf("claim DNS domain reservation: %v", err)
	}
	if err := c.DeleteDNSDomainReservationIfUnclaimed(ctx, "acct-1", reservation.Name); !IsConflict(err) {
		t.Fatalf("delete claimed DNS domain error = %v, want conflict", err)
	}

	reservation.Labels[claimedByClusterUIDLabel] = ""
	if err := c.UpdateDNSReservation(ctx, reservation); err != nil {
		t.Fatalf("release DNS domain reservation: %v", err)
	}
	if err := c.DeleteDNSDomainReservationIfUnclaimed(ctx, "acct-1", reservation.Name); err != nil {
		t.Fatalf("delete unclaimed DNS domain: %v", err)
	}
	if err := c.DeleteDNSDomainIndex(ctx, index.Namespace, index.Name); err != nil {
		t.Fatalf("DeleteDNSDomainIndex: %v", err)
	}
}
