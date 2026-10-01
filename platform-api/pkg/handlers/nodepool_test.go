//go:build integration

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
)

// testNodePoolCR creates a NodePool named "<cluster>.<child>" owned by cluster,
// as platform-api would store it.
func testNodePoolCR(child string, cluster *hyperfleetv1alpha1.Cluster) *hyperfleetv1alpha1.NodePool {
	return &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      hyperfleetv1alpha1.ChildName(cluster.Name, child),
			Namespace: cluster.Namespace,
			UID:       types.UID(uuid.NewString()),
			Labels:    map[string]string{hyperfleetv1alpha1.ClusterUIDLabel: string(cluster.UID)},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: hyperfleetv1alpha1.GroupVersion.String(),
				Kind:       "Cluster",
				Name:       cluster.Name,
				UID:        cluster.UID,
				Controller: ptr.To(true),
			}},
		},
	}
}

func newTestNodePoolHandler(t *testing.T, objects ...client.Object) (*NodePoolHandler, client.Client) {
	t.Helper()
	fc := fakeDB(newTestScheme()).WithObjects(objects...).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	return NewNodePoolHandler(hyperfleetdb.NewClientFrom(fc, logger), logger), fc
}

// nodePoolBody builds a create request for the NodePool named name.
func nodePoolBody(t *testing.T, name string) []byte {
	return mustMarshal(t, map[string]any{
		"metadata": map[string]any{"name": name},
		"spec":     map[string]any{"nodePool": map[string]any{}},
	})
}

func createNodePool(t *testing.T, handler *NodePoolHandler, name string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/v0/nodepools", bytes.NewReader(nodePoolBody(t, name)))
	req = req.WithContext(testContext(testAccountID))
	w := httptest.NewRecorder()
	handler.Create(w, req)
	return w
}

func listNodePools(t *testing.T, handler *NodePoolHandler, query string) []string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v0/nodepools"+query, nil)
	req = req.WithContext(testContext(testAccountID))
	w := httptest.NewRecorder()
	handler.List(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var result struct {
		Items []struct {
			Metadata metav1.ObjectMeta `json:"metadata"`
		} `json:"items"`
	}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var names []string
	for _, item := range result.Items {
		names = append(names, item.Metadata.Name)
	}
	return names
}

// TestNodePoolHandler_Update_MissingSpec verifies that PUT /nodepools/{name} with
// an absent or empty spec is rejected with 400 before any DB write.
func TestNodePoolHandler_Update_MissingSpec(t *testing.T) {
	cluster := testClusterCR(uuid.NewString(), "abc", testAccountID)
	np := testNodePoolCR("test-np", cluster)
	handler, _ := newTestNodePoolHandler(t, cluster, np)

	cases := []struct {
		name string
		body []byte
	}{
		{"no spec key", mustMarshal(t, map[string]any{})},
		{"empty spec object", mustMarshal(t, map[string]any{"spec": map[string]any{}})},
		{"whitespace spec", []byte(`{"spec":{ }}`)},
		{"whitespace with newline spec", []byte(`{"spec":{
}}`)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPut, "/api/v0/nodepools/abc.test-np", bytes.NewReader(tc.body))
			req = req.WithContext(testContext(testAccountID))
			req = mux.SetURLVars(req, map[string]string{"name": np.Name})

			w := httptest.NewRecorder()
			handler.Update(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d body=%s", w.Code, w.Body.String())
			}
			var errResp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
				t.Fatalf("response body is not valid JSON: %v", err)
			}
			msg, _ := errResp["message"].(string)
			if msg == "" {
				t.Errorf("expected non-empty error message")
			}
		})
	}
}

// TestNodePoolHandler_Create_InvalidMetadata verifies that POST /nodepools with
// missing or malformed metadata is rejected before any DB lookup.
func TestNodePoolHandler_Create_InvalidMetadata(t *testing.T) {
	handler, _ := newTestNodePoolHandler(t) // no pre-seeded objects needed

	cases := []struct {
		name         string
		metadata     map[string]any
		wantStatus   int
		wantCodeFrag string // substring of the platform error code in message
	}{
		{"missing name", map[string]any{}, http.StatusBadRequest, ErrNodePoolCreateMissingFields.Code},
		{"other account", map[string]any{"name": "c.np", "namespace": "account-999999999999"}, http.StatusForbidden, ErrNodePoolCreateForbiddenNamespace.Code},
		{"legacy cluster namespace", map[string]any{"name": "c.np", "namespace": "cluster-550e8400-e29b-41d4-a716-446655440000"}, http.StatusForbidden, ErrNodePoolCreateForbiddenNamespace.Code},
		{"no cluster part", map[string]any{"name": "np"}, http.StatusBadRequest, ErrNodePoolCreateInvalidName.Code},
		{"empty child", map[string]any{"name": "c."}, http.StatusBadRequest, ErrNodePoolCreateInvalidName.Code},
		{"two dots", map[string]any{"name": "c.np.x"}, http.StatusBadRequest, ErrNodePoolCreateInvalidName.Code},
		{"uppercase", map[string]any{"name": "c.Workers"}, http.StatusBadRequest, ErrNodePoolCreateInvalidName.Code},
		{"cluster part too long", map[string]any{"name": strings.Repeat("c", hyperfleetv1alpha1.MaxClusterNameLength+1) + ".np"}, http.StatusBadRequest, ErrNodePoolCreateInvalidName.Code},
		{"child too long", map[string]any{"name": "c." + strings.Repeat("n", hyperfleetv1alpha1.MaxChildNameLength+1)}, http.StatusBadRequest, ErrNodePoolCreateInvalidName.Code},
		{"bad label", map[string]any{"name": "c.np", "labels": map[string]string{"bad key!": "v"}}, http.StatusBadRequest, ErrNodePoolCreateInvalidMetadata.Code},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := mustMarshal(t, map[string]any{"metadata": tc.metadata, "spec": map[string]any{}})
			req := httptest.NewRequest(http.MethodPost, "/api/v0/nodepools", bytes.NewReader(body))
			req = req.WithContext(testContext(testAccountID))

			w := httptest.NewRecorder()
			handler.Create(w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("expected %d, got %d body=%s", tc.wantStatus, w.Code, w.Body.String())
			}
			var errResp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
				t.Fatalf("response body is not valid JSON: %v", err)
			}
			msg, _ := errResp["message"].(string)
			if tc.wantCodeFrag != "" && !containsCode(msg, tc.wantCodeFrag) {
				t.Errorf("message %q does not contain code %s", msg, tc.wantCodeFrag)
			}
		})
	}
}

// TestNodePoolHandler_Create_SetsOwner verifies that a created NodePool points at
// its parent through both an ownerReference and the cluster-uid label, and that
// client-sent owner metadata is ignored.
func TestNodePoolHandler_Create_SetsOwner(t *testing.T) {
	cluster := testClusterCR(uuid.NewString(), "prod", testAccountID)
	handler, fc := newTestNodePoolHandler(t, cluster)

	body := mustMarshal(t, map[string]any{
		"metadata": map[string]any{
			"name":   "prod.workers",
			"labels": map[string]string{hyperfleetv1alpha1.ClusterUIDLabel: "spoofed", "team": "a"},
			"ownerReferences": []map[string]any{{
				"apiVersion": "hyperfleet.io/v1alpha1", "kind": "Cluster", "name": "other", "uid": "spoofed",
			}},
		},
		"spec": map[string]any{"nodePool": map[string]any{}},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v0/nodepools", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	w := httptest.NewRecorder()
	handler.Create(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var np hyperfleetv1alpha1.NodePool
	key := client.ObjectKey{Namespace: cluster.Namespace, Name: "prod.workers"}
	if err := fc.Get(context.Background(), key, &np); err != nil {
		t.Fatalf("get stored nodepool: %v", err)
	}
	if np.UID == "" {
		t.Error("expected a database-minted uid")
	}
	if got := np.Labels[hyperfleetv1alpha1.ClusterUIDLabel]; got != string(cluster.UID) {
		t.Errorf("cluster-uid label = %q, want %q", got, cluster.UID)
	}
	if np.Labels["team"] != "a" {
		t.Errorf("expected client label to be kept, got %v", np.Labels)
	}
	owner := metav1.GetControllerOf(&np)
	if owner == nil || owner.UID != cluster.UID || owner.Name != "prod" || owner.Kind != "Cluster" || len(np.OwnerReferences) != 1 {
		t.Errorf("expected a single controller ownerReference to cluster prod/%s, got %+v", cluster.UID, np.OwnerReferences)
	}
}

func TestNodePoolHandler_Create_ParentMissing(t *testing.T) {
	handler, _ := newTestNodePoolHandler(t)

	w := createNodePool(t, handler, "ghost.workers")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestNodePoolHandler_Create_ParentDeleting(t *testing.T) {
	cluster := testClusterCR(uuid.NewString(), "dying", testAccountID)
	now := metav1.Now()
	cluster.DeletionTimestamp = &now
	cluster.Finalizers = []string{"hyperfleet.io/cluster"}
	handler, _ := newTestNodePoolHandler(t, cluster)

	w := createNodePool(t, handler, "dying.workers")
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !containsCode(msg, ErrNodePoolCreateClusterDeleting.Code) {
		t.Errorf("message %q does not contain code %s", msg, ErrNodePoolCreateClusterDeleting.Code)
	}
}

func TestNodePoolHandler_Create_DuplicateName(t *testing.T) {
	cluster := testClusterCR(uuid.NewString(), "prod", testAccountID)
	handler, _ := newTestNodePoolHandler(t, cluster)

	if w := createNodePool(t, handler, "prod.workers"); w.Code != http.StatusCreated {
		t.Fatalf("first create: expected 201, got %d: %s", w.Code, w.Body.String())
	}
	if w := createNodePool(t, handler, "prod.workers"); w.Code != http.StatusConflict {
		t.Fatalf("second create: expected 409, got %d: %s", w.Code, w.Body.String())
	}
}

// TestNodePoolHandler_SameChildNameInTwoClusters verifies that two clusters in
// one account can both have a "workers" pool, that each is listed under its own
// cluster, and that deleting one leaves the other alone.
func TestNodePoolHandler_SameChildNameInTwoClusters(t *testing.T) {
	a := testClusterCR(uuid.NewString(), "cluster-a", testAccountID)
	b := testClusterCR(uuid.NewString(), "cluster-b", testAccountID)
	handler, _ := newTestNodePoolHandler(t, a, b)

	for _, name := range []string{"cluster-a.workers", "cluster-b.workers"} {
		if w := createNodePool(t, handler, name); w.Code != http.StatusCreated {
			t.Fatalf("create %s: expected 201, got %d: %s", name, w.Code, w.Body.String())
		}
	}

	if got := listNodePools(t, handler, "?clusterId="+string(a.UID)); len(got) != 1 || got[0] != "cluster-a.workers" {
		t.Errorf("cluster-a pools = %v, want [cluster-a.workers]", got)
	}
	if got := listNodePools(t, handler, "?clusterId="+string(b.UID)); len(got) != 1 || got[0] != "cluster-b.workers" {
		t.Errorf("cluster-b pools = %v, want [cluster-b.workers]", got)
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/nodepools/cluster-a.workers", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"name": "cluster-a.workers"})
	w := httptest.NewRecorder()
	handler.Delete(w, req)
	if w.Code != http.StatusAccepted {
		t.Fatalf("delete: expected 202, got %d: %s", w.Code, w.Body.String())
	}

	if got := listNodePools(t, handler, ""); len(got) != 1 || got[0] != "cluster-b.workers" {
		t.Errorf("after delete, pools = %v, want [cluster-b.workers]", got)
	}
}

// TestNodePoolHandler_RecreatedClusterInheritsNothing verifies that a cluster
// recreated under a reused name gets a new uid, so a pool left behind by the old
// cluster is not listed as the new cluster's.
func TestNodePoolHandler_RecreatedClusterInheritsNothing(t *testing.T) {
	old := testClusterCR(uuid.NewString(), "prod", testAccountID)
	leftover := testNodePoolCR("workers", old)
	recreated := testClusterCR(uuid.NewString(), "prod", testAccountID)
	handler, _ := newTestNodePoolHandler(t, recreated, leftover)

	if got := listNodePools(t, handler, "?clusterId="+string(recreated.UID)); len(got) != 0 {
		t.Errorf("recreated cluster pools = %v, want none", got)
	}
}

func TestNodePoolHandler_Get_ScopedToAccount(t *testing.T) {
	cluster := testClusterCR(uuid.NewString(), "prod", testAccountID)
	np := testNodePoolCR("workers", cluster)
	handler, _ := newTestNodePoolHandler(t, cluster, np)

	for account, want := range map[string]int{testAccountID: http.StatusOK, "999999999999": http.StatusNotFound} {
		req := httptest.NewRequest(http.MethodGet, "/api/v0/nodepools/prod.workers", nil)
		req = req.WithContext(testContext(account))
		req = mux.SetURLVars(req, map[string]string{"name": "prod.workers"})
		w := httptest.NewRecorder()
		handler.Get(w, req)
		if w.Code != want {
			t.Errorf("account %s: expected %d, got %d: %s", account, want, w.Code, w.Body.String())
		}
	}
}

// containsCode reports whether the metav1.Status message contains the given code prefix.
func containsCode(message, code string) bool {
	return len(message) >= len(code) && message[:len(code)] == code
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}
