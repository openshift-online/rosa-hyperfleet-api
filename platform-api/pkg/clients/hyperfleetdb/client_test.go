package hyperfleetdb

import (
	"context"
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

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = hyperfleetv1alpha1.AddToScheme(s)
	return s
}

func newUIDIndexedClient(t *testing.T) client.Client {
	t.Helper()
	return fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithIndex(&hyperfleetv1alpha1.DNSReservation{}, "metadata.uid", func(obj client.Object) []string {
			return []string{string(obj.GetUID())}
		}).
		WithIndex(&hyperfleetv1alpha1.OidcConfig{}, "metadata.uid", func(obj client.Object) []string {
			return []string{string(obj.GetUID())}
		}).
		WithStatusSubresource(&hyperfleetv1alpha1.DNSReservation{}, &hyperfleetv1alpha1.OidcConfig{}).
		Build()
}

func TestClient_CreateCluster_SetsAccountLabel(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	cluster := &hyperfleetv1alpha1.Cluster{
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

	np := &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-nodepool",
			Namespace: "account-wrong-account",
		},
		Spec: hyperfleetv1alpha1.NodePoolSpec{},
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
		&hyperfleetv1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-cluster", Namespace: "account-acct-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
		&hyperfleetv1alpha1.Cluster{
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
		&hyperfleetv1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "alpha.workers", Namespace: "account-acct-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
		&hyperfleetv1alpha1.NodePool{
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
		&hyperfleetv1alpha1.NodePool{
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
		&hyperfleetv1alpha1.Cluster{
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
		&hyperfleetv1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "update-me", Namespace: "account-acct-1"}},
		&hyperfleetv1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "delete-object", Namespace: "account-acct-1"}},
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
		&hyperfleetv1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "alpha.workers", Namespace: "account-acct-1", Labels: map[string]string{clusterUIDLabel: "cluster-1"}},
		},
		&hyperfleetv1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: "beta.workers", Namespace: "account-acct-1", Labels: map[string]string{clusterUIDLabel: "cluster-2"}},
		},
		&hyperfleetv1alpha1.NodePool{
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
	reservation := &hyperfleetv1alpha1.DNSReservation{
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
	byName.Status.Phase = hyperfleetv1alpha1.DNSReservationPhaseReady
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

	second := &hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "dns-two", UID: types.UID("dns-uid-2")}}
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

	manifest := &hyperfleetv1alpha1.Manifest{ObjectMeta: metav1.ObjectMeta{Name: "my-manifest"}}
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

	managementCluster := &hyperfleetv1alpha1.ManagementCluster{ObjectMeta: metav1.ObjectMeta{Name: "mc-one", Namespace: "ignored"}}
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
	config := &hyperfleetv1alpha1.OidcConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "oidc-one", UID: types.UID("oidc-uid-1"), Namespace: "wrong-namespace"},
		Spec: hyperfleetv1alpha1.OidcConfigSpec{
			Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
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
	byName.Labels["hyperfleet.io/claimed-by-cluster-uid"] = "cluster-uid"
	if err := c.UpdateOidcConfigObject(ctx, byName); err != nil {
		t.Fatalf("UpdateOidcConfigObject: %v", err)
	}
	if err := c.DeleteOidcConfig(ctx, "acct-1", "oidc-one"); err != nil {
		t.Fatalf("DeleteOidcConfig: %v", err)
	}
	if _, err := c.GetOidcConfig(ctx, "acct-1", "oidc-one"); !IsNotFound(err) {
		t.Fatalf("GetOidcConfig after delete error = %v, want NotFound", err)
	}

	second := &hyperfleetv1alpha1.OidcConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "oidc-two", UID: types.UID("oidc-uid-2")},
		Spec:       hyperfleetv1alpha1.OidcConfigSpec{Type: hyperfleetv1alpha1.OidcConfigTypeManaged},
	}
	if err := c.CreateOidcConfig(ctx, "acct-1", second); err != nil {
		t.Fatalf("CreateOidcConfig second: %v", err)
	}
	if err := c.DeleteOidcConfigObject(ctx, second); err != nil {
		t.Fatalf("DeleteOidcConfigObject: %v", err)
	}

	index := &hyperfleetv1alpha1.Index{ObjectMeta: metav1.ObjectMeta{
		Name:      "issuer-index",
		Namespace: hyperfleetv1alpha1.OidcIssuerReservationsNamespace,
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
	resource := schema.GroupResource{Group: "hyperfleet.io", Resource: "clusters"}
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
