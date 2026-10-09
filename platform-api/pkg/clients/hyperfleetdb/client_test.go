package hyperfleetdb

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = hyperfleetv1alpha1.AddToScheme(s)
	return s
}

func TestClient_CreateCluster_SetsAccountLabel(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	cluster := &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "cluster-uuid-1",
		},
	}

	if err := c.CreateCluster(context.Background(), "acct-123", cluster); err != nil {
		t.Fatalf("CreateCluster: %v", err)
	}

	if got := cluster.Labels[accountIDLabel]; got != "acct-123" {
		t.Errorf("account-id label = %q, want %q", got, "acct-123")
	}
}

func TestClient_CreateNodePool_SetsNamespaceAndLabel(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	np := &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-nodepool",
			Namespace: "cluster-uuid-1",
		},
		Spec: hyperfleetv1alpha1.NodePoolSpec{},
	}

	if err := c.CreateNodePool(context.Background(), "acct-123", np); err != nil {
		t.Fatalf("CreateNodePool: %v", err)
	}

	if got := np.Labels[accountIDLabel]; got != "acct-123" {
		t.Errorf("account-id label = %q, want %q", got, "acct-123")
	}
}

func TestClient_ListClusters_FiltersByAccount(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&hyperfleetv1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-cluster", Namespace: "cluster-uuid-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
		&hyperfleetv1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "other-cluster", Namespace: "cluster-uuid-2",
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

func TestClient_GetNodePool_ScopedToCluster(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&hyperfleetv1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "workers", Namespace: "cluster-uuid-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
		&hyperfleetv1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "workers", Namespace: "cluster-uuid-2",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	np, err := c.GetNodePool(context.Background(), "acct-1", "uuid-2", "workers")
	if err != nil {
		t.Fatalf("GetNodePool: %v", err)
	}
	if np.Namespace != "cluster-uuid-2" {
		t.Errorf("namespace = %q, want cluster-uuid-2", np.Namespace)
	}
}

func TestClient_GetNodePool_FallbackWithoutClusterID(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&hyperfleetv1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{
				Name: "workers", Namespace: "cluster-uuid-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
	).Build()
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	np, err := c.GetNodePool(context.Background(), "acct-1", "", "workers")
	if err != nil {
		t.Fatalf("GetNodePool empty clusterID: unexpected err = %v", err)
	}
	if np.Name != "workers" {
		t.Errorf("got nodepool %q, want workers", np.Name)
	}
}

func TestClient_GetCluster_ScopedToAccount(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(
		&hyperfleetv1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{
				Name: "my-cluster", Namespace: "cluster-uuid-1",
				Labels: map[string]string{accountIDLabel: "acct-1"},
			},
		},
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	c := NewClientFrom(fc, logger)

	// Correct account can access
	cr, err := c.GetCluster(context.Background(), "acct-1", "uuid-1")
	if err != nil {
		t.Fatalf("GetCluster with correct account: %v", err)
	}
	if cr.Name != "my-cluster" {
		t.Errorf("got cluster %q, want my-cluster", cr.Name)
	}

	// Wrong account gets not-found
	_, err = c.GetCluster(context.Background(), "acct-2", "uuid-1")
	if !IsNotFound(err) {
		t.Errorf("GetCluster with wrong account: expected NotFound, got %v", err)
	}
}

func TestClient_DNSDomainReservationLifecycle(t *testing.T) {
	ctx := context.Background()
	fc := fake.NewClientBuilder().WithScheme(testScheme()).Build()
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	index := &hyperfleetv1alpha1.Index{ObjectMeta: metav1.ObjectMeta{Name: "abc12345", Namespace: "dns-shard-0-reservations"}}
	if err := c.CreateDNSDomainIndex(ctx, "acct-1", index); err != nil {
		t.Fatalf("CreateDNSDomainIndex: %v", err)
	}
	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "0-abc12345"},
		Spec: hyperfleetv1alpha1.DNSReservationSpec{
			IndexRef:    hyperfleetv1alpha1.IndexRef{Namespace: index.Namespace, Name: index.Name},
			BaseDomain:  "abc12345.0.example.com",
			ClusterArch: "hcp",
			UserDefined: true,
		},
	}
	if err := c.CreateDNSDomainReservation(ctx, "acct-1", reservation); err != nil {
		t.Fatalf("CreateDNSDomainReservation: %v", err)
	}

	reservations, err := c.ListDNSDomainReservations(ctx, "acct-1")
	if err != nil || len(reservations.Items) != 1 {
		t.Fatalf("ListDNSDomainReservations = (%v, %v), want one reservation", reservations, err)
	}
	claimed, err := c.ClaimDNSDomainReservation(ctx, "acct-1", "ABC12345.0.EXAMPLE.COM", "cluster-1")
	if err != nil {
		t.Fatalf("ClaimDNSDomainReservation: %v", err)
	}
	if got := claimed.Labels[hyperfleetv1alpha1.DNSReservationClusterNamespaceLabel]; got != "cluster-1" {
		t.Fatalf("claimed cluster label = %q, want cluster-1", got)
	}
	if _, err := c.ClaimDNSDomainReservation(ctx, "acct-1", reservation.Spec.BaseDomain, "cluster-1"); err != nil {
		t.Fatalf("idempotent claim by same cluster: %v", err)
	}
	if _, err := c.ClaimDNSDomainReservation(ctx, "acct-1", reservation.Spec.BaseDomain, "cluster-2"); !IsConflict(err) {
		t.Fatalf("second claim error = %v, want conflict", err)
	}
	if _, err := c.BeginDeleteDNSDomainReservation(ctx, "acct-1", reservation.Name); !IsConflict(err) {
		t.Fatalf("delete claimed reservation error = %v, want conflict", err)
	}
	if err := c.ReleaseDNSDomainReservationClaim(ctx, "acct-1", reservation.Name, "cluster-1"); err != nil {
		t.Fatalf("ReleaseDNSDomainReservationClaim: %v", err)
	}
	deleting, err := c.BeginDeleteDNSDomainReservation(ctx, "acct-1", reservation.Name)
	if err != nil {
		t.Fatalf("BeginDeleteDNSDomainReservation: %v", err)
	}
	if deleting.Labels[hyperfleetv1alpha1.DNSReservationDeletingLabel] != "true" {
		t.Fatalf("deleting label was not set: %#v", deleting.Labels)
	}
	if _, err := c.BeginDeleteDNSDomainReservation(ctx, "acct-1", reservation.Name); err != nil {
		t.Fatalf("repeated BeginDeleteDNSDomainReservation: %v", err)
	}
	if _, err := c.ClaimDNSDomainReservation(ctx, "acct-1", reservation.Spec.BaseDomain, "cluster-3"); !IsConflict(err) {
		t.Fatalf("claim during deletion error = %v, want conflict", err)
	}
	if err := c.DeleteDNSDomainIndex(ctx, index.Namespace, index.Name); err != nil {
		t.Fatalf("DeleteDNSDomainIndex: %v", err)
	}
	if err := c.DeleteDNSDomainReservation(ctx, "acct-1", reservation.Name); err != nil {
		t.Fatalf("DeleteDNSDomainReservation: %v", err)
	}
}

func TestClient_DNSDomainReservationNotFoundPaths(t *testing.T) {
	ctx := context.Background()
	c := NewClientFrom(fake.NewClientBuilder().WithScheme(testScheme()).Build(), slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if _, err := c.ClaimDNSDomainReservation(ctx, "acct-1", "missing.0.example.com", "cluster-1"); !IsNotFound(err) {
		t.Fatalf("ClaimDNSDomainReservation for missing domain error = %v, want not found", err)
	}
	if err := c.ReleaseDNSDomainReservationClaim(ctx, "acct-1", "missing", "cluster-1"); err != nil {
		t.Fatalf("ReleaseDNSDomainReservationClaim for missing reservation: %v", err)
	}
	if err := c.DeleteDNSDomainIndex(ctx, "dns-shard-0-reservations", "missing"); !IsNotFound(err) {
		t.Fatalf("DeleteDNSDomainIndex for missing index error = %v, want not found", err)
	}
	if err := c.DeleteDNSDomainReservation(ctx, "acct-1", "missing"); !IsNotFound(err) {
		t.Fatalf("DeleteDNSDomainReservation for missing reservation error = %v, want not found", err)
	}
}

func TestClient_BeginDeleteDNSDomainReservationRetriesConflict(t *testing.T) {
	ctx := context.Background()
	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "0-abc12345", Namespace: accountNamespace("acct-1")},
		Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "abc12345.0.example.com", ClusterArch: "hcp", UserDefined: true},
	}
	var updateAttempts int
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(reservation).WithInterceptorFuncs(interceptor.Funcs{
		Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
			if _, ok := obj.(*hyperfleetv1alpha1.DNSReservation); ok {
				updateAttempts++
				if updateAttempts == 1 {
					return apierrors.NewConflict(dnsReservationGR, obj.GetName(), fmt.Errorf("concurrent update"))
				}
			}
			return c.Update(ctx, obj, opts...)
		},
	}).Build()
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))

	deleted, err := c.BeginDeleteDNSDomainReservation(ctx, "acct-1", reservation.Name)
	if err != nil {
		t.Fatalf("BeginDeleteDNSDomainReservation: %v", err)
	}
	if updateAttempts != 2 {
		t.Fatalf("reservation update attempts = %d, want 2", updateAttempts)
	}
	if deleted.Labels[hyperfleetv1alpha1.DNSReservationDeletingLabel] != "true" {
		t.Fatalf("deleting label was not set after retry: %#v", deleted.Labels)
	}
}

func TestClient_DNSDomainReservationFilteringAndIdempotency(t *testing.T) {
	ctx := context.Background()
	reservations := []runtime.Object{
		&hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "not-user-defined", Namespace: accountNamespace("acct-1")}, Spec: hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "custom.0.example.com", ClusterArch: "hcp"}},
		&hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "not-hcp", Namespace: accountNamespace("acct-1")}, Spec: hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "custom.0.example.com", ClusterArch: "classic", UserDefined: true}},
		&hyperfleetv1alpha1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: "other-domain", Namespace: accountNamespace("acct-1")}, Spec: hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "other.0.example.com", ClusterArch: "hcp", UserDefined: true}},
	}
	c := NewClientFrom(fake.NewClientBuilder().WithScheme(testScheme()).WithRuntimeObjects(reservations...).Build(), slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if _, err := c.ClaimDNSDomainReservation(ctx, "acct-1", "custom.0.example.com", "cluster-1"); !IsNotFound(err) {
		t.Fatalf("claim with no eligible reservation error = %v, want not found", err)
	}

	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "custom", Namespace: accountNamespace("acct-2")},
		Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "custom.0.example.com", ClusterArch: "hcp", UserDefined: true},
	}
	otherAccountClient := NewClientFrom(fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(reservation).Build(), slog.New(slog.NewTextHandler(os.Stdout, nil)))
	claimed, err := otherAccountClient.ClaimDNSDomainReservation(ctx, "acct-2", "custom.0.example.com", "cluster-2")
	if err != nil {
		t.Fatalf("claim reservation with nil labels: %v", err)
	}
	if claimed.Labels[hyperfleetv1alpha1.DNSReservationClusterNamespaceLabel] != "cluster-2" {
		t.Fatalf("claim label missing: %#v", claimed.Labels)
	}
	if _, err := otherAccountClient.ClaimDNSDomainReservation(ctx, "acct-2", "custom.0.example.com", "cluster-2"); err != nil {
		t.Fatalf("repeat claim by same cluster: %v", err)
	}
	if err := otherAccountClient.ReleaseDNSDomainReservationClaim(ctx, "acct-2", "custom", "cluster-other"); err != nil {
		t.Fatalf("release by non-owner: %v", err)
	}
	if _, err := otherAccountClient.BeginDeleteDNSDomainReservation(ctx, "acct-2", "custom"); !IsConflict(err) {
		t.Fatalf("delete in-use reservation error = %v, want conflict", err)
	}
}

func TestClient_ListDNSDomainReservationsReturnsStorageError(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*hyperfleetv1alpha1.DNSReservationList); ok {
				return errors.New("list failed")
			}
			return c.List(ctx, list, opts...)
		},
	}).Build()
	c := NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if _, err := c.ListDNSDomainReservations(context.Background(), "acct-1"); err == nil {
		t.Fatal("ListDNSDomainReservations error = nil, want storage error")
	}
}

func TestClient_BeginDeleteDNSDomainReservationRejectsUnsupportedReservation(t *testing.T) {
	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{Name: "generated", Namespace: accountNamespace("acct-1")},
		Spec:       hyperfleetv1alpha1.DNSReservationSpec{BaseDomain: "generated.0.example.com", ClusterArch: "hcp", UserDefined: false},
	}
	c := NewClientFrom(fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(reservation).Build(), slog.New(slog.NewTextHandler(os.Stdout, nil)))
	if _, err := c.BeginDeleteDNSDomainReservation(context.Background(), "acct-1", reservation.Name); !IsNotFound(err) {
		t.Fatalf("BeginDeleteDNSDomainReservation error = %v, want not found", err)
	}
}
