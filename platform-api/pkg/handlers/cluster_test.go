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

	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

const testAccountID = "123456789012"

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	_ = hyperfleetv1alpha1.AddToScheme(s)
	return s
}

func testContext(accountID string) context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, middleware.ContextKeyAccountID, accountID)
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::"+accountID+":user/test")
	return ctx
}

// testClusterCR creates a cluster CR with Namespace="cluster-<clusterID>",
// Name=clusterName (human-readable), labeled with accountID.
// The namespace matches what clusterNamespace(clusterID) produces so that
// handler lookups (which call GetCluster → InNamespace("cluster-<id>")) work.
func testClusterCR(clusterID, clusterName, accountID string) *hyperfleetv1alpha1.Cluster {
	return &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: "cluster-" + clusterID,
			Labels:    map[string]string{"hyperfleet.io/account-id": accountID},
		},
		Spec: hyperfleetv1alpha1.ClusterSpec{
			HostedCluster: hyperfleetv1alpha1.HostedClusterSpecPassthrough{
				Platform: hypershiftv1beta1.PlatformSpec{
					Type: hypershiftv1beta1.AWSPlatform,
					AWS: &hypershiftv1beta1.AWSPlatformSpec{
						Region: "us-east-1",
					},
				},
			},
		},
	}
}

// metaField extracts a field from the K8s-native metadata object in a decoded response.
func metaField(result map[string]any, field string) any {
	meta, _ := result["metadata"].(map[string]any)
	if meta == nil {
		return nil
	}
	return meta[field]
}

// minSpec is the minimum valid cluster spec for create requests.
var minSpec = map[string]any{
	"hostedCluster": map[string]any{
		"release":    map[string]any{"image": ""},
		"networking": map[string]any{},
		"platform":   map[string]any{"type": "AWS"},
	},
}

// clusterBody builds a K8s-native cluster create request body.
// Pass nil spec to use minSpec.
func clusterBody(name string, spec map[string]any) []byte {
	s := spec
	if s == nil {
		s = minSpec
	}
	b, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"name": name},
		"spec":     s,
	})
	return b
}

// decodeErrorMessage decodes a metav1.Status response and returns the message field.
func decodeErrorMessage(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var errResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	msg, _ := errResp["message"].(string)
	return msg
}

func TestClusterHandler_List_Success(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testClusterCR("uuid-1", "cluster-1", testAccountID),
		testClusterCR("uuid-2", "cluster-2", testAccountID),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters", nil)
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	if int(result["total"].(float64)) != 2 {
		t.Errorf("expected total=2, got %v", result["total"])
	}
	items := result["items"].([]any)
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

func TestClusterHandler_List_Empty(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters", nil)
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	if int(result["total"].(float64)) != 0 {
		t.Errorf("expected total=0, got %v", result["total"])
	}
}

func TestClusterHandler_List_Pagination(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testClusterCR("uuid-c1", "c1", testAccountID),
		testClusterCR("uuid-c2", "c2", testAccountID),
		testClusterCR("uuid-c3", "c3", testAccountID),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters?limit=2&offset=1", nil)
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	if int(result["total"].(float64)) != 3 {
		t.Errorf("expected total=3, got %v", result["total"])
	}
	items := result["items"].([]any)
	if len(items) != 2 {
		t.Errorf("expected 2 items (offset=1, limit=2 of 3), got %d", len(items))
	}
}

func TestClusterHandler_Create_Success(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	if uid := metaField(result, "uid"); uid == nil || uid == "" {
		t.Error("expected non-empty cluster UID in metadata.uid")
	}
	if name := metaField(result, "name"); name != "my-cluster" {
		t.Errorf("expected metadata.name=my-cluster, got %v", name)
	}
}

func TestClusterHandler_Create_SetsCreatorARN(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// CreatorARN is a service-set field — not exposed in the public response.
	// Verify it was stored correctly in the internal CRD.
	var list hyperfleetv1alpha1.ClusterList
	if err := fc.List(context.Background(), &list); err != nil {
		t.Fatalf("listing clusters from fake client: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 cluster in store, got %d", len(list.Items))
	}
	wantARN := "arn:aws:iam::" + testAccountID + ":user/test"
	if list.Items[0].Spec.CreatorARN != wantARN {
		t.Errorf("expected CreatorARN=%s, got %s", wantARN, list.Items[0].Spec.CreatorARN)
	}
}

func TestClusterHandler_Create_InvalidJSON(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader([]byte("not json")))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestClusterHandler_Create_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"missing name", clusterBody("", map[string]any{"hostedCluster": map[string]any{}})},
		{"empty metadata", []byte(`{}`)},
		{"missing spec key", []byte(`{"metadata":{"name":"my-cluster"}}`)},
		{"null spec", []byte(`{"metadata":{"name":"my-cluster"},"spec":null}`)},
		{"empty spec object", clusterBody("my-cluster", map[string]any{})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme()
			fc := fake.NewClientBuilder().WithScheme(scheme).Build()
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

			req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(tt.body))
			req = req.WithContext(testContext(testAccountID))

			w := httptest.NewRecorder()
			handler.Create(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", w.Code)
			}
		})
	}
}

func TestClusterHandler_Create_NameTooLong(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	longName := strings.Repeat("a", hyperfleetdb.MaxClusterNameLen+1)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody(longName, nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateNameTooLong.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterCreateNameTooLong.Code, msg)
	}
}

func TestClusterHandler_Get_Success(t *testing.T) {
	cr := testClusterCR("cluster-123", "test-cluster", testAccountID)
	cr.Status = hyperfleetv1alpha1.ClusterStatus{
		ObservedGeneration: 1,
		Phase:              "Ready",
		Conditions: []metav1.Condition{
			{Type: "Available", Status: metav1.ConditionTrue, Reason: "AsExpected"},
		},
	}
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).
		WithStatusSubresource(cr).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters/cluster-123", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-123"})

	w := httptest.NewRecorder()
	handler.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	if uid := metaField(result, "uid"); uid != "cluster-123" {
		t.Errorf("expected metadata.uid=cluster-123, got %v", uid)
	}
	if name := metaField(result, "name"); name != "test-cluster" {
		t.Errorf("expected metadata.name=test-cluster, got %v", name)
	}
	// Status is included in GET — no separate /statuses endpoint.
	status, _ := result["status"].(map[string]any)
	if status["phase"] != "Ready" {
		t.Errorf("expected status.phase=Ready, got %v", status["phase"])
	}
}

func TestClusterHandler_Get_NotFound(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters/no-such-cluster", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "no-such-cluster"})

	w := httptest.NewRecorder()
	handler.Get(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterGetNotFound.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterGetNotFound.Code, msg)
	}
}

func TestClusterHandler_Delete_Success(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testClusterCR("cluster-123", "test-cluster", testAccountID),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/clusters/cluster-123", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-123"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	// Delete returns a plain map (not a K8s type) with the cluster_id.
	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)
	if result["cluster_id"] != "cluster-123" {
		t.Errorf("expected cluster_id=cluster-123, got %v", result["cluster_id"])
	}
}

func TestClusterHandler_Delete_NotFound(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/clusters/no-such-cluster", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "no-such-cluster"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestClusterHandler_Update_Success(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testClusterCR("cluster-123", "test-cluster", testAccountID),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"displayName": "updated-display",
		},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v0/clusters/cluster-123", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-123"})

	w := httptest.NewRecorder()
	handler.Update(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	// Verify the mutable spec field was merged into the response.
	spec, _ := result["spec"].(map[string]any)
	if spec["displayName"] != "updated-display" {
		t.Errorf("expected spec.displayName=updated-display, got %v", spec["displayName"])
	}
}

func TestClusterHandler_Update_NotFound(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{"displayName": "x"},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v0/clusters/no-such", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "no-such"})

	w := httptest.NewRecorder()
	handler.Update(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestClusterHandler_Update_MissingSpec(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testClusterCR("cluster-123", "test-cluster", testAccountID),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	body, _ := json.Marshal(map[string]any{})

	req := httptest.NewRequest(http.MethodPut, "/api/v0/clusters/cluster-123", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-123"})

	w := httptest.NewRecorder()
	handler.Update(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestClusterHandler_Create_DuplicateName(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testClusterCR("existing-id", "test-cluster", testAccountID),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("test-cluster", nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for duplicate name, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateNameConflict.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterCreateNameConflict.Code, msg)
	}
}

func TestClusterHandler_Create_SameNameDifferentAccount(t *testing.T) {
	otherAccount := "999999999999"
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testClusterCR("existing-id", "test-cluster", otherAccount),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("test-cluster", nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 (same name in different account is allowed), got %d: %s", w.Code, w.Body.String())
	}
}

