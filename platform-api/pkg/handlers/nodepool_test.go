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
	"testing"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
)

func testNodePoolCR(npName, clusterNamespace, accountID string) *hyperfleetv1alpha1.NodePool {
	return &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      npName,
			Namespace: clusterNamespace,
			Labels:    map[string]string{"hyperfleet.io/account-id": accountID},
		},
		Spec: hyperfleetv1alpha1.NodePoolSpec{AccountID: accountID},
	}
}

func newTestNodePoolHandler(t *testing.T, objects ...client.Object) *NodePoolHandler {
	t.Helper()
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	return NewNodePoolHandler(hyperfleetdb.NewClientFrom(fc, logger), logger)
}

func TestNodePoolHandler_List_PaginationIsolation(t *testing.T) {
	const ownCluster1 = "550e8400-e29b-41d4-a716-446655440000"
	const ownCluster2 = "550e8400-e29b-41d4-a716-446655440001"
	const foreignCluster = "550e8400-e29b-41d4-a716-446655440002"
	handler := newTestNodePoolHandler(t,
		testClusterCR(ownCluster1, "own-1", testAccountID),
		testClusterCR(ownCluster2, "own-2", testAccountID),
		testClusterCR(foreignCluster, "foreign", "999999999999"),
		testNodePoolCR("workers-1", "cluster-"+ownCluster1, testAccountID),
		testNodePoolCR("workers-2", "cluster-"+ownCluster2, testAccountID),
		testNodePoolCR("foreign-workers", "cluster-"+foreignCluster, "999999999999"),
	)
	req := httptest.NewRequest(http.MethodGet, "/api/v0/nodepools?limit=1&offset=1", nil)
	req = req.WithContext(testContext(testAccountID))
	w := httptest.NewRecorder()

	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Items []any `json:"items"`
		Total int   `json:"total"`
	}
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 2 || len(response.Items) != 1 {
		t.Errorf("got total=%d items=%d, want total=2 items=1", response.Total, len(response.Items))
	}
}

func TestNodePoolHandler_Create_SetsAccountOwnership(t *testing.T) {
	const clusterID = "550e8400-e29b-41d4-a716-446655440000"
	handler := newTestNodePoolHandler(t, testClusterCR(clusterID, "parent", testAccountID))
	body := mustMarshal(t, map[string]any{
		"metadata": map[string]any{
			"name":      "workers",
			"namespace": "cluster-" + clusterID,
			"labels":    map[string]any{"hyperfleet.io/account-id": "999999999999"},
		},
		"spec": map[string]any{"displayName": "workers", "accountId": "999999999999"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v0/nodepools", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	w := httptest.NewRecorder()

	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
	stored, err := handler.db.GetNodePool(req.Context(), clusterID, "workers")
	if err != nil {
		t.Fatal(err)
	}
	if stored.Spec.AccountID != testAccountID || stored.Labels["hyperfleet.io/account-id"] != testAccountID {
		t.Errorf("stored ownership = (%q, %q), want caller account %q", stored.Spec.AccountID, stored.Labels["hyperfleet.io/account-id"], testAccountID)
	}
}

func TestNodePoolHandler_CrossAccountOperations(t *testing.T) {
	const clusterID = "550e8400-e29b-41d4-a716-446655440000"
	foreign := testNodePoolCR("workers", "cluster-"+clusterID, "999999999999")
	handler := newTestNodePoolHandler(t,
		testClusterCR(clusterID, "foreign", "999999999999"),
		foreign,
	)

	tests := []struct {
		name, method, path, body string
		invoke                   func(http.ResponseWriter, *http.Request)
	}{
		{"create", http.MethodPost, "/api/v0/nodepools", `{"metadata":{"name":"new","namespace":"cluster-` + clusterID + `"},"spec":{"displayName":"new"}}`, handler.Create},
		{"get", http.MethodGet, "/api/v0/nodepools/workers?clusterId=" + clusterID, "", handler.Get},
		{"update", http.MethodPut, "/api/v0/nodepools/workers?clusterId=" + clusterID, `{"spec":{"displayName":"changed"}}`, handler.Update},
		{"delete", http.MethodDelete, "/api/v0/nodepools/workers?clusterId=" + clusterID, "", handler.Delete},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertHandlerStatus(t, http.StatusNotFound, tc.method, tc.path, tc.body,
				map[string]string{"id": "workers"}, testAccountID, tc.invoke)
		})
	}
}

func TestNodePoolHandler_SameAccountOperations(t *testing.T) {
	const clusterID = "550e8400-e29b-41d4-a716-446655440000"
	tests := []struct {
		name, method, body string
		wantStatus         int
		invoke             func(*NodePoolHandler, http.ResponseWriter, *http.Request)
	}{
		{"get", http.MethodGet, "", http.StatusOK, func(h *NodePoolHandler, w http.ResponseWriter, r *http.Request) { h.Get(w, r) }},
		{"update", http.MethodPut, `{"metadata":{"labels":{"hyperfleet.io/account-id":"999999999999"}},"spec":{"displayName":"changed"}}`, http.StatusOK, func(h *NodePoolHandler, w http.ResponseWriter, r *http.Request) { h.Update(w, r) }},
		{"delete", http.MethodDelete, "", http.StatusAccepted, func(h *NodePoolHandler, w http.ResponseWriter, r *http.Request) { h.Delete(w, r) }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := newTestNodePoolHandler(t,
				testClusterCR(clusterID, "parent", testAccountID),
				testNodePoolCR("workers", "cluster-"+clusterID, testAccountID),
			)
			req := httptest.NewRequest(tc.method, "/api/v0/nodepools/workers?clusterId="+clusterID, bytes.NewBufferString(tc.body))
			req = req.WithContext(testContext(testAccountID))
			req = mux.SetURLVars(req, map[string]string{"id": "workers"})
			w := httptest.NewRecorder()

			tc.invoke(handler, w, req)

			if w.Code != tc.wantStatus {
				t.Errorf("expected %d, got %d: %s", tc.wantStatus, w.Code, w.Body.String())
			}
			if tc.name == "update" {
				stored, err := handler.db.GetNodePool(req.Context(), clusterID, "workers")
				if err != nil {
					t.Fatal(err)
				}
				if stored.Labels["hyperfleet.io/account-id"] != testAccountID {
					t.Errorf("stored account label = %q, want %q", stored.Labels["hyperfleet.io/account-id"], testAccountID)
				}
			}
		})
	}
}

// TestNodePoolHandler_Update_MissingSpec verifies that PUT /nodepools/{id} with
// an absent or empty spec is rejected with 400 before any DB write.
func TestNodePoolHandler_Update_MissingSpec(t *testing.T) {
	np := testNodePoolCR("test-np", "cluster-abc", testAccountID)
	handler := newTestNodePoolHandler(t, testClusterCR("abc", "parent", testAccountID), np)

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
			req := httptest.NewRequest(http.MethodPut, "/api/v0/nodepools/test-np", bytes.NewReader(tc.body))
			req = req.WithContext(testContext(testAccountID))
			req = mux.SetURLVars(req, map[string]string{"id": "test-np"})

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

// Reject accountId from raw spec even though the public type omits it.
func TestNodePoolHandler_Update_RejectsAccountID(t *testing.T) {
	const clusterID = "uuid-1"
	for _, body := range []string{
		`{"spec":{"accountId":"999999999999","displayName":"changed"}}`,
		`{"spec":{"AccountId":"","displayName":"changed"}}`,
	} {
		t.Run(body, func(t *testing.T) {
			np := testNodePoolCR("workers", "cluster-"+clusterID, testAccountID)
			np.Spec.AccountID = testAccountID
			fc := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(
				testClusterCR(clusterID, "owned", testAccountID), np,
			).Build()
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			handler := NewNodePoolHandler(hyperfleetdb.NewClientFrom(fc, logger), logger)
			assertHandlerStatus(t, http.StatusUnprocessableEntity, http.MethodPut,
				"/api/v0/nodepools/workers?clusterId="+clusterID, body,
				map[string]string{"id": "workers"}, testAccountID, handler.Update)
			var stored hyperfleetv1alpha1.NodePool
			if err := fc.Get(context.Background(), client.ObjectKeyFromObject(np), &stored); err != nil {
				t.Fatal(err)
			}
			if stored.Spec.AccountID != testAccountID {
				t.Errorf("stored account ID = %q, want %q", stored.Spec.AccountID, testAccountID)
			}
		})
	}
}

// TestNodePoolHandler_Create_MissingMetadata verifies that POST /nodepools with
// missing or malformed metadata fields is rejected before any DB lookup.
func TestNodePoolHandler_Create_MissingMetadata(t *testing.T) {
	handler := newTestNodePoolHandler(t) // no pre-seeded objects needed

	cases := []struct {
		name         string
		body         map[string]any
		wantStatus   int
		wantCodeFrag string // substring of the platform error code in message
	}{
		{
			name:         "missing name",
			body:         map[string]any{"metadata": map[string]any{"namespace": "cluster-550e8400-e29b-41d4-a716-446655440000"}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateMissingFields.Code,
		},
		{
			name:         "missing namespace",
			body:         map[string]any{"metadata": map[string]any{"name": "my-np"}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateMissingFields.Code,
		},
		{
			name:         "invalid namespace - not cluster prefix",
			body:         map[string]any{"metadata": map[string]any{"name": "my-np", "namespace": "notacluster"}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateInvalidNamespace.Code,
		},
		{
			name:         "invalid namespace - prefix only no uuid",
			body:         map[string]any{"metadata": map[string]any{"name": "my-np", "namespace": "cluster-"}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateInvalidNamespace.Code,
		},
		{
			name:         "invalid namespace - uppercase uuid",
			body:         map[string]any{"metadata": map[string]any{"name": "my-np", "namespace": "cluster-550E8400-E29B-41D4-A716-446655440000"}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateInvalidNamespace.Code,
		},
		{
			name:         "invalid namespace - uuid without prefix",
			body:         map[string]any{"metadata": map[string]any{"name": "my-np", "namespace": "550e8400-e29b-41d4-a716-446655440000"}},
			wantStatus:   http.StatusBadRequest,
			wantCodeFrag: ErrNodePoolCreateInvalidNamespace.Code,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v0/nodepools", bytes.NewReader(mustMarshal(t, tc.body)))
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
			if msg == "" {
				t.Errorf("expected non-empty error message")
			}
			if tc.wantCodeFrag != "" && !containsCode(msg, tc.wantCodeFrag) {
				t.Errorf("message %q does not contain code %s", msg, tc.wantCodeFrag)
			}
		})
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
