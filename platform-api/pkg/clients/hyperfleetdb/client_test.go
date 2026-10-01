package hyperfleetdb

import (
	"context"
	"log/slog"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

func newTestClient(objs ...client.Object) *Client {
	fc := fake.NewClientBuilder().WithScheme(testScheme()).WithObjects(objs...).Build()
	return NewClientFrom(fc, slog.New(slog.NewTextHandler(os.Stdout, nil)))
}

func testCluster(accountID, name, uid string) *hyperfleetv1alpha1.Cluster {
	return &hyperfleetv1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{
		Name: name, Namespace: hyperfleetv1alpha1.AccountNamespace(accountID), UID: types.UID(uid),
	}}
}

func testNodePool(cluster *hyperfleetv1alpha1.Cluster, child string) *hyperfleetv1alpha1.NodePool {
	return &hyperfleetv1alpha1.NodePool{ObjectMeta: metav1.ObjectMeta{
		Name:      hyperfleetv1alpha1.ChildName(cluster.Name, child),
		Namespace: cluster.Namespace,
		Labels:    map[string]string{hyperfleetv1alpha1.ClusterUIDLabel: string(cluster.UID)},
	}}
}

func TestClient_ListClusters_ScopedToAccountNamespace(t *testing.T) {
	c := newTestClient(
		testCluster("acct-1", "my-cluster", "uid-1"),
		testCluster("acct-2", "other-cluster", "uid-2"),
	)

	list, err := c.ListClusters(context.Background(), "acct-1")
	if err != nil {
		t.Fatalf("ListClusters: %v", err)
	}
	if len(list.Items) != 1 || list.Items[0].Name != "my-cluster" {
		t.Fatalf("expected only my-cluster for acct-1, got %+v", list.Items)
	}
}

func TestClient_GetCluster_ScopedToAccount(t *testing.T) {
	c := newTestClient(testCluster("acct-1", "my-cluster", "uid-1"))

	cr, err := c.GetCluster(context.Background(), "acct-1", "my-cluster")
	if err != nil {
		t.Fatalf("GetCluster with correct account: %v", err)
	}
	if cr.UID != "uid-1" {
		t.Errorf("got cluster uid %q, want uid-1", cr.UID)
	}

	if _, err := c.GetCluster(context.Background(), "acct-2", "my-cluster"); !IsNotFound(err) {
		t.Errorf("GetCluster with wrong account: expected NotFound, got %v", err)
	}
}

func TestClient_ListNodePools_FiltersByClusterUID(t *testing.T) {
	a := testCluster("acct-1", "a", "uid-a")
	b := testCluster("acct-1", "b", "uid-b")
	c := newTestClient(a, b, testNodePool(a, "workers"), testNodePool(b, "workers"))

	all, err := c.ListNodePools(context.Background(), "acct-1", "")
	if err != nil {
		t.Fatalf("ListNodePools: %v", err)
	}
	if len(all.Items) != 2 {
		t.Errorf("expected 2 nodepools in the account, got %d", len(all.Items))
	}

	onlyA, err := c.ListNodePools(context.Background(), "acct-1", "uid-a")
	if err != nil {
		t.Fatalf("ListNodePools: %v", err)
	}
	if len(onlyA.Items) != 1 || onlyA.Items[0].Name != "a.workers" {
		t.Errorf("expected only a.workers, got %+v", onlyA.Items)
	}
}

func TestClient_GetNodePool_ScopedToAccount(t *testing.T) {
	a := testCluster("acct-1", "a", "uid-a")
	c := newTestClient(a, testNodePool(a, "workers"))

	if _, err := c.GetNodePool(context.Background(), "acct-1", "a.workers"); err != nil {
		t.Fatalf("GetNodePool: %v", err)
	}
	if _, err := c.GetNodePool(context.Background(), "acct-2", "a.workers"); !IsNotFound(err) {
		t.Errorf("GetNodePool with wrong account: expected NotFound, got %v", err)
	}
}
