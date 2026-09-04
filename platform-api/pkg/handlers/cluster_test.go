//go:build integration

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
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

// testOidcConfigID is the OidcConfig ID used by tests that opt into the
// OidcConfig-backed path via specWithOidcConfigID.
const testOidcConfigID = "test-oidc-config"

// testOidcConfigIssuerURL is the issuerUrl on the fixture OidcConfig created
// by testReadyOidcConfig; tests assert the created cluster's issuerURL matches.
const testOidcConfigIssuerURL = "https://oidc.example.com/test-oidc-config"

// minSpec is the minimum valid cluster spec for create requests. It omits
// oidcConfigId, selecting the legacy (auto-generated issuer) path.
var minSpec = map[string]any{
	"hostedCluster": map[string]any{
		"release":    map[string]any{"image": ""},
		"networking": map[string]any{},
		"platform":   map[string]any{"type": "AWS"},
	},
}

// specWithOidcConfigID returns a copy of minSpec with oidcConfigId set,
// selecting the OidcConfig-backed issuer path.
func specWithOidcConfigID(id string) map[string]any {
	spec := map[string]any{"oidcConfigId": id}
	for k, v := range minSpec {
		spec[k] = v
	}
	return spec
}

// testReadyOidcConfig returns a managed OidcConfig CR in the Ready phase
// with the given issuerUrl, scoped to accountID.
func testReadyOidcConfig(configID, accountID, issuerURL string) *hyperfleetv1alpha1.OidcConfig {
	oc := testOidcConfigCR(configID, accountID, testManagedOidcConfigSpec(accountID))
	oc.Spec.IssuerUrl = issuerURL
	oc.Status.Phase = hyperfleetv1alpha1.OidcConfigPhaseReady
	return oc
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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
			handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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

// TestClusterHandler_Create_LegacyPath_NoOidcConfig verifies that omitting
// oidcConfigId auto-generates issuerURL from the handler's base URL.
func TestClusterHandler_Create_LegacyPath_NoOidcConfig(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	const baseURL = "https://oidc.example.com"
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), baseURL, 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)
	clusterID, _ := metaField(result, "uid").(string)
	if clusterID == "" {
		t.Fatal("expected non-empty cluster UID in metadata.uid")
	}

	var list hyperfleetv1alpha1.ClusterList
	if err := fc.List(context.Background(), &list); err != nil {
		t.Fatalf("listing clusters from fake client: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 cluster in store, got %d", len(list.Items))
	}
	wantIssuerURL := baseURL + "/" + clusterID
	if got := list.Items[0].Spec.HostedCluster.IssuerURL; got != wantIssuerURL {
		t.Errorf("expected issuerURL=%s, got %s", wantIssuerURL, got)
	}
	if got := list.Items[0].Spec.OidcConfigID; got != "" {
		t.Errorf("expected empty oidcConfigId on legacy-path cluster, got %s", got)
	}
}

func TestClusterHandler_Create_OidcConfigNotFound(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", specWithOidcConfigID(testOidcConfigID))))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateOidcConfigNotFound.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterCreateOidcConfigNotFound.Code, msg)
	}
}

// oidcConfigGetFailingClient simulates a non-NotFound failure (e.g. a transient DB error) when
// getting an OidcConfig, to distinguish that case from a genuine 404.
type oidcConfigGetFailingClient struct {
	client.Client
}

func (c *oidcConfigGetFailingClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*hyperfleetv1alpha1.OidcConfig); ok {
		return fmt.Errorf("simulated database error")
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

func TestClusterHandler_Create_OidcConfigLookupFailure(t *testing.T) {
	scheme := newTestScheme()
	fc := &oidcConfigGetFailingClient{Client: fake.NewClientBuilder().WithScheme(scheme).Build()}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", specWithOidcConfigID(testOidcConfigID))))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateOidcConfigLookupFailed.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterCreateOidcConfigLookupFailed.Code, msg)
	}
}

func TestClusterHandler_Create_OidcConfigWrongAccount(t *testing.T) {
	otherAccount := "999999999999"
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testReadyOidcConfig(testOidcConfigID, otherAccount, testOidcConfigIssuerURL),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	// The OidcConfig exists but belongs to a different account; it must not
	// be resolvable by a caller in testAccountID.
	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", specWithOidcConfigID(testOidcConfigID))))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}
}

func TestClusterHandler_Create_UnmanagedOidcConfigNotReady(t *testing.T) {
	scheme := newTestScheme()
	pending := testOidcConfigCR(testOidcConfigID, testAccountID, testUnmanagedOidcConfigSpec(testAccountID))
	pending.Status.Phase = hyperfleetv1alpha1.OidcConfigPhasePending
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pending).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", specWithOidcConfigID(testOidcConfigID))))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateOidcConfigNotReady.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterCreateOidcConfigNotReady.Code, msg)
	}
}

func TestClusterHandler_Create_ManagedOidcConfigPending_Succeeds(t *testing.T) {
	scheme := newTestScheme()
	pending := testOidcConfigCR(testOidcConfigID, testAccountID, testManagedOidcConfigSpec(testAccountID))
	pending.Spec.IssuerUrl = testOidcConfigIssuerURL
	pending.Status.Phase = hyperfleetv1alpha1.OidcConfigPhasePending
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(pending).
		WithStatusSubresource(pending).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", specWithOidcConfigID(testOidcConfigID))))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var list hyperfleetv1alpha1.ClusterList
	if err := fc.List(context.Background(), &list); err != nil {
		t.Fatalf("listing clusters from fake client: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 cluster in store, got %d", len(list.Items))
	}
	if got := list.Items[0].Spec.HostedCluster.IssuerURL; got != testOidcConfigIssuerURL {
		t.Errorf("expected issuerURL=%s, got %s", testOidcConfigIssuerURL, got)
	}
	if got := list.Items[0].Spec.OidcConfigID; got != testOidcConfigID {
		t.Errorf("expected oidcConfigId=%s, got %s", testOidcConfigID, got)
	}
}

func TestClusterHandler_Create_OidcConfigError_Rejected(t *testing.T) {
	for _, spec := range []hyperfleetv1alpha1.OidcConfigSpec{
		testManagedOidcConfigSpec(testAccountID),
		testUnmanagedOidcConfigSpec(testAccountID),
	} {
		scheme := newTestScheme()
		errored := testOidcConfigCR(testOidcConfigID, testAccountID, spec)
		errored.Status.Phase = hyperfleetv1alpha1.OidcConfigPhaseError
		fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(errored).Build()
		logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
		handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

		req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", specWithOidcConfigID(testOidcConfigID))))
		req = req.WithContext(testContext(testAccountID))

		w := httptest.NewRecorder()
		handler.Create(w, req)

		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("type=%s: expected 422, got %d: %s", spec.Type, w.Code, w.Body.String())
		}
		if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateOidcConfigNotReady.Code) {
			t.Errorf("type=%s: expected message to contain %s, got %q", spec.Type, ErrClusterCreateOidcConfigNotReady.Code, msg)
		}
	}
}

func TestClusterHandler_Create_OidcConfigAlreadyInUse(t *testing.T) {
	scheme := newTestScheme()
	oidcConfig := testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL)
	existingCluster := testClusterCR("existing-cluster-id", "existing-cluster", testAccountID)
	existingCluster.Spec.OidcConfigID = testOidcConfigID
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(oidcConfig, existingCluster).
		WithStatusSubresource(oidcConfig).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("new-cluster", specWithOidcConfigID(testOidcConfigID))))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateOidcConfigInUse.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterCreateOidcConfigInUse.Code, msg)
	}

	var list hyperfleetv1alpha1.ClusterList
	if err := fc.List(context.Background(), &list); err != nil {
		t.Fatalf("listing clusters from fake client: %v", err)
	}
	if len(list.Items) != 1 {
		t.Errorf("expected the rejected create to leave only the pre-existing cluster, got %d", len(list.Items))
	}
}

func TestClusterHandler_Create_OidcConfigInUseDifferentAccountAllowed(t *testing.T) {
	otherAccount := "999999999999"
	scheme := newTestScheme()
	oidcConfigOther := testReadyOidcConfig(testOidcConfigID, otherAccount, testOidcConfigIssuerURL)
	existingCluster := testClusterCR("existing-cluster-id", "existing-cluster", otherAccount)
	existingCluster.Spec.OidcConfigID = testOidcConfigID
	oidcConfigMine := testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL)
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(oidcConfigOther, existingCluster, oidcConfigMine).
		WithStatusSubresource(oidcConfigOther, oidcConfigMine).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("new-cluster", specWithOidcConfigID(testOidcConfigID))))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 (same oidcConfigId owned by a different account is unrelated), got %d: %s", w.Code, w.Body.String())
	}
}

type oidcConfigIDUniqueClient struct {
	client.Client
	mu sync.Mutex
}

func (c *oidcConfigIDUniqueClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cluster, ok := obj.(*hyperfleetv1alpha1.Cluster); ok && cluster.Spec.OidcConfigID != "" {
		acctID := cluster.Labels["hyperfleet.io/account-id"]
		var list hyperfleetv1alpha1.ClusterList
		if err := c.Client.List(ctx, &list, client.MatchingLabels{"hyperfleet.io/account-id": acctID}); err != nil {
			return err
		}
		for i := range list.Items {
			if list.Items[i].Spec.OidcConfigID == cluster.Spec.OidcConfigID {
				return apierrors.NewAlreadyExists(schema.GroupResource{Resource: "clusters"}, cluster.Name)
			}
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

func TestClusterHandler_Create_ConcurrentOidcConfigCollision(t *testing.T) {
	scheme := newTestScheme()
	oidcConfig := testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL)
	innerFC := fake.NewClientBuilder().WithScheme(scheme).WithObjects(oidcConfig).
		WithStatusSubresource(oidcConfig).Build()
	fc := &oidcConfigIDUniqueClient{Client: innerFC}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	var callCount int64
	handler.generateID = func() string {
		n := atomic.AddInt64(&callCount, 1)
		return fmt.Sprintf("aaaa-%04d-0000-0000", n)
	}

	var wg sync.WaitGroup
	names := []string{"concurrent-a", "concurrent-b"}
	codes := make([]int, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody(names[idx], specWithOidcConfigID(testOidcConfigID))))
			req = req.WithContext(testContext(testAccountID))
			w := httptest.NewRecorder()
			handler.Create(w, req)
			codes[idx] = w.Code
		}(i)
	}

	wg.Wait()

	var created, conflict int
	for _, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflict++
		}
	}
	if created != 1 || conflict != 1 {
		t.Fatalf("expected exactly one 201 and one 409 (ErrClusterCreateOidcConfigInUse) for concurrent creates referencing the same oidcConfigId, got codes %v", codes)
	}
}

func TestClusterHandler_Create_DerivesIssuerURLAndUpdatesLastUsed(t *testing.T) {
	scheme := newTestScheme()
	oidcConfig := testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL)
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(oidcConfig).
		WithStatusSubresource(oidcConfig).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("my-cluster", specWithOidcConfigID(testOidcConfigID))))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	// issuerURL is service-set (not writable by the caller) but remains
	// visible in the response, derived from the referenced OidcConfig.
	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)
	spec, _ := result["spec"].(map[string]any)
	hostedCluster, _ := spec["hostedCluster"].(map[string]any)
	if got := hostedCluster["issuerURL"]; got != testOidcConfigIssuerURL {
		t.Errorf("expected issuerURL=%s in response, got %v", testOidcConfigIssuerURL, got)
	}

	// ...and it must be persisted on the internal CRD, derived from the OidcConfig.
	var list hyperfleetv1alpha1.ClusterList
	if err := fc.List(context.Background(), &list); err != nil {
		t.Fatalf("listing clusters from fake client: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 cluster in store, got %d", len(list.Items))
	}
	if got := list.Items[0].Spec.HostedCluster.IssuerURL; got != testOidcConfigIssuerURL {
		t.Errorf("expected issuerURL=%s, got %s", testOidcConfigIssuerURL, got)
	}

	// The referenced OidcConfig's lastUsedTimestamp must be stamped.
	var updated hyperfleetv1alpha1.OidcConfig
	if err := fc.Get(context.Background(), client.ObjectKeyFromObject(oidcConfig), &updated); err != nil {
		t.Fatalf("getting oidc config: %v", err)
	}
	if updated.Status.LastUsedTimestamp == nil {
		t.Error("expected status.lastUsedTimestamp to be set after cluster creation")
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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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

// TestClusterHandler_Update_RejectsOidcConfigIdChange verifies that
// oidcConfigId cannot be changed from one value to another via update.
func TestClusterHandler_Update_RejectsOidcConfigIdChange(t *testing.T) {
	scheme := newTestScheme()
	cr := testClusterCR("cluster-123", "test-cluster", testAccountID)
	cr.Spec.OidcConfigID = testOidcConfigID
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"oidcConfigId": "some-other-oidc-config",
		},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v0/clusters/cluster-123", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-123"})

	w := httptest.NewRecorder()
	handler.Update(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterValidation.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterValidation.Code, msg)
	}
}

// TestClusterHandler_Update_RejectsOidcConfigIdAddition verifies a legacy-path
// cluster cannot adopt the OidcConfig-backed path via update.
func TestClusterHandler_Update_RejectsOidcConfigIdAddition(t *testing.T) {
	scheme := newTestScheme()
	cr := testClusterCR("cluster-123", "test-cluster", testAccountID)
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"oidcConfigId": testOidcConfigID,
		},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v0/clusters/cluster-123", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-123"})

	w := httptest.NewRecorder()
	handler.Update(w, req)

	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterValidation.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterValidation.Code, msg)
	}
}

func TestClusterHandler_Create_DuplicateName(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testClusterCR("existing-id", "test-cluster", testAccountID),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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
	oidcConfig := testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL)
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testClusterCR("existing-id", "test-cluster", otherAccount),
		oidcConfig,
	).WithStatusSubresource(oidcConfig).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("test-cluster", nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 (same name in different account is allowed), got %d: %s", w.Code, w.Body.String())
	}
}

func sequenceIDGen(ids ...string) func() string {
	i := 0
	return func() string {
		id := ids[i]
		if i < len(ids)-1 {
			i++
		}
		return id
	}
}

func TestClusterHandler_Create_Hash4CollisionThenSuccess(t *testing.T) {
	existing := testClusterCR("aaaa-existing", "test-cluster", "999999999999")
	existing.Spec.InternalID = "aaaa-existing"

	scheme := newTestScheme()
	oidcConfig := testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL)
	innerFC := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		existing,
		oidcConfig,
	).WithStatusSubresource(oidcConfig).Build()
	fc := &hash4UniqueClient{Client: innerFC}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)
	handler.generateID = sequenceIDGen("aaaa-1111-1111-1111", "cccc-2222-2222-2222")

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("test-cluster", nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201 after retry, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)
	if uid := metaField(result, "uid"); uid != "cccc-2222-2222-2222" {
		t.Errorf("expected metadata.uid=cccc-2222-2222-2222, got %v", uid)
	}
}

func TestClusterHandler_Create_Hash4ExhaustedRetries(t *testing.T) {
	existing := testClusterCR("aaaa-existing", "test-cluster", "999999999999")
	existing.Spec.InternalID = "aaaa-existing"

	scheme := newTestScheme()
	innerFC := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		existing,
		testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL),
	).Build()
	fc := &hash4UniqueClient{Client: innerFC}
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)
	handler.generateID = sequenceIDGen(
		"aaaa-1111-1111-1111",
		"aaaa-2222-2222-2222",
		"aaaa-3333-3333-3333",
		"aaaa-4444-4444-4444",
		"aaaa-5555-5555-5555",
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("test-cluster", nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 after exhausted retries, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateIDExhausted.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterCreateIDExhausted.Code, msg)
	}
}

// hash4UniqueClient wraps a client.Client to enforce hash4 uniqueness on
// Cluster creates, modeling the database's idx_cluster_name_hash4 unique index.
type hash4UniqueClient struct {
	client.Client
	mu sync.Mutex
}

func (c *hash4UniqueClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if cluster, ok := obj.(*hyperfleetv1alpha1.Cluster); ok {
		if id := cluster.Spec.InternalID; len(id) >= 4 {
			var list hyperfleetv1alpha1.ClusterList
			if err := c.Client.List(ctx, &list); err != nil {
				return err
			}
			for i := range list.Items {
				existing := &list.Items[i]
				if existing.Name == cluster.Name &&
					len(existing.Spec.InternalID) >= 4 &&
					existing.Spec.InternalID[:4] == id[:4] {
					return apierrors.NewAlreadyExists(
						schema.GroupResource{Resource: "clusters"}, cluster.Name)
				}
			}
		}
	}
	return c.Client.Create(ctx, obj, opts...)
}

func TestClusterHandler_Create_ConcurrentHash4Collision(t *testing.T) {
	scheme := newTestScheme()
	oidcConfig0 := testReadyOidcConfig(testOidcConfigID, "account-0", testOidcConfigIssuerURL)
	oidcConfig1 := testReadyOidcConfig(testOidcConfigID, "account-1", testOidcConfigIssuerURL)
	innerFC := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		oidcConfig0,
		oidcConfig1,
	).WithStatusSubresource(oidcConfig0, oidcConfig1).Build()
	fc := &hash4UniqueClient{Client: innerFC}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	var callCount int64
	handler.generateID = func() string {
		n := atomic.AddInt64(&callCount, 1)
		return fmt.Sprintf("aaaa-%04d-0000-0000", n)
	}

	var wg sync.WaitGroup
	codes := make([]int, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			acct := fmt.Sprintf("account-%d", idx)
			req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("concurrent-cluster", nil)))
			req = req.WithContext(testContext(acct))
			w := httptest.NewRecorder()
			handler.Create(w, req)
			codes[idx] = w.Code
		}(i)
	}

	wg.Wait()

	var created int
	for _, code := range codes {
		if code == http.StatusCreated {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("expected exactly one 201 Created, got codes %v", codes)
	}
}
