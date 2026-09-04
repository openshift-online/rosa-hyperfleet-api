//go:build integration

package handlers

import (
	"bytes"
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
	}
}

func newTestNodePoolHandler(t *testing.T, objects ...client.Object) *NodePoolHandler {
	t.Helper()
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	return NewNodePoolHandler(hyperfleetdb.NewClientFrom(fc, logger), logger)
}

// TestNodePoolHandler_Update_MissingSpec verifies that PUT /nodepools/{id} with
// an absent or empty spec is rejected with 400 before any DB write.
func TestNodePoolHandler_Update_MissingSpec(t *testing.T) {
	np := testNodePoolCR("test-np", "cluster-abc", testAccountID)
	handler := newTestNodePoolHandler(t, np)

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
