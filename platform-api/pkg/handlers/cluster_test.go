//go:build integration

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

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

// fakeDB returns a fake client builder that behaves like hyperfleet-db on
// Create: the database mints the uid and ignores any the client sent.
func fakeDB(scheme *runtime.Scheme) *fake.ClientBuilder {
	return fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			obj.SetUID(types.UID(uuid.NewString()))
			return c.Create(ctx, obj, opts...)
		},
	})
}

// testClusterCR creates a cluster CR named clusterName in accountID's namespace,
// with uid clusterUID as if minted by the database.
func testClusterCR(clusterUID, clusterName, accountID string) *hyperfleetv1alpha1.Cluster {
	return &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterName,
			Namespace: hyperfleetv1alpha1.AccountNamespace(accountID),
			UID:       types.UID(clusterUID),
		},
		Spec: hyperfleetv1alpha1.ClusterSpec{
			AccountID: accountID,
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
	fc := fakeDB(scheme).WithObjects(
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
	fc := fakeDB(scheme).Build()
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
	fc := fakeDB(scheme).WithObjects(
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
	fc := fakeDB(scheme).Build()
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
	fc := fakeDB(scheme).Build()
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
	fc := fakeDB(scheme).Build()
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
		{"whitespace spec", []byte(`{"metadata":{"name":"my-cluster"},"spec":{ }}`)},
		{"whitespace with newline spec", []byte(`{"metadata":{"name":"my-cluster"},"spec":{
}}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme()
			fc := fakeDB(scheme).Build()
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

func TestClusterHandler_Create_InvalidName(t *testing.T) {
	for name, clusterName := range map[string]string{
		"too long":      strings.Repeat("a", hyperfleetv1alpha1.MaxClusterNameLength+1),
		"contains dot":  "my.cluster",
		"uppercase":     "MyCluster",
		"leading dash":  "-cluster",
		"trailing dash": "cluster-",
	} {
		t.Run(name, func(t *testing.T) {
			fc := fakeDB(newTestScheme()).Build()
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

			req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody(clusterName, nil)))
			req = req.WithContext(testContext(testAccountID))

			w := httptest.NewRecorder()
			handler.Create(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
			}
			if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateInvalidName.Code) {
				t.Errorf("expected message to contain %s, got %q", ErrClusterCreateInvalidName.Code, msg)
			}
		})
	}
}

func TestClusterHandler_Create_MaxLengthName(t *testing.T) {
	fc := fakeDB(newTestScheme()).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	name := strings.Repeat("a", hyperfleetv1alpha1.MaxClusterNameLength)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody(name, nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

// TestClusterHandler_Create_Namespace verifies the namespace is the caller's
// account: empty defaults to it, and any other namespace is forbidden.
func TestClusterHandler_Create_Namespace(t *testing.T) {
	tests := []struct {
		namespace string
		wantCode  int
	}{
		{"", http.StatusCreated},
		{"account-" + testAccountID, http.StatusCreated},
		{"account-999999999999", http.StatusForbidden},
		{"cluster-abc", http.StatusForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.namespace, func(t *testing.T) {
			fc := fakeDB(newTestScheme()).Build()
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

			body, _ := json.Marshal(map[string]any{
				"metadata": map[string]any{"name": "my-cluster", "namespace": tt.namespace},
				"spec":     minSpec,
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(body))
			req = req.WithContext(testContext(testAccountID))

			w := httptest.NewRecorder()
			handler.Create(w, req)

			if w.Code != tt.wantCode {
				t.Fatalf("expected %d, got %d: %s", tt.wantCode, w.Code, w.Body.String())
			}
			if tt.wantCode != http.StatusCreated {
				return
			}
			var stored hyperfleetv1alpha1.Cluster
			key := client.ObjectKey{Namespace: "account-" + testAccountID, Name: "my-cluster"}
			if err := fc.Get(context.Background(), key, &stored); err != nil {
				t.Fatalf("get stored cluster: %v", err)
			}
		})
	}
}

// TestClusterHandler_Create_IgnoresServiceOwnedMetadata verifies that a client
// cannot set the uid, ownerReferences, finalizers, or hyperfleet.io/ labels,
// while its own labels and annotations are stored as sent.
func TestClusterHandler_Create_IgnoresServiceOwnedMetadata(t *testing.T) {
	fc := fakeDB(newTestScheme()).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	const sentUID = "00000000-0000-0000-0000-000000000001"
	body, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{
			"name":        "my-cluster",
			"uid":         sentUID,
			"finalizers":  []string{"evil"},
			"labels":      map[string]string{"team": "a", hyperfleetv1alpha1.ClusterUIDLabel: "spoofed"},
			"annotations": map[string]string{"note": "hello"},
			"ownerReferences": []map[string]any{{
				"apiVersion": "hyperfleet.io/v1alpha1", "kind": "Cluster", "name": "other", "uid": sentUID,
			}},
		},
		"spec": minSpec,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var stored hyperfleetv1alpha1.Cluster
	key := client.ObjectKey{Namespace: "account-" + testAccountID, Name: "my-cluster"}
	if err := fc.Get(context.Background(), key, &stored); err != nil {
		t.Fatalf("get stored cluster: %v", err)
	}
	if stored.UID == "" || stored.UID == sentUID {
		t.Errorf("expected a database-minted uid, got %q", stored.UID)
	}
	if len(stored.Finalizers) != 0 || len(stored.OwnerReferences) != 0 {
		t.Errorf("expected no client finalizers or ownerReferences, got %v / %v", stored.Finalizers, stored.OwnerReferences)
	}
	if _, ok := stored.Labels[hyperfleetv1alpha1.ClusterUIDLabel]; ok {
		t.Errorf("expected reserved label to be dropped, got %v", stored.Labels)
	}
	if stored.Labels["team"] != "a" || stored.Annotations["note"] != "hello" {
		t.Errorf("expected client labels/annotations to be kept, got %v / %v", stored.Labels, stored.Annotations)
	}
}

func TestClusterHandler_Create_InvalidAnnotation(t *testing.T) {
	fc := fakeDB(newTestScheme()).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	body, _ := json.Marshal(map[string]any{
		"metadata": map[string]any{"name": "my-cluster", "annotations": map[string]string{"not a key!": "x"}},
		"spec":     minSpec,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateInvalidMetadata.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterCreateInvalidMetadata.Code, msg)
	}
}

// TestClusterHandler_Create_LegacyPath_NoOidcConfig verifies that omitting
// oidcConfigId auto-generates issuerURL from the handler's base URL.
func TestClusterHandler_Create_LegacyPath_NoOidcConfig(t *testing.T) {
	scheme := newTestScheme()
	fc := fakeDB(scheme).Build()
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

	var list hyperfleetv1alpha1.ClusterList
	if err := fc.List(context.Background(), &list); err != nil {
		t.Fatalf("listing clusters from fake client: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected 1 cluster in store, got %d", len(list.Items))
	}
	// The issuer's last segment becomes the HostedCluster InfraID, so it is a fresh UUID.
	issuerID, ok := strings.CutPrefix(list.Items[0].Spec.HostedCluster.IssuerURL, baseURL+"/")
	if !ok {
		t.Fatalf("expected issuerURL under %s, got %s", baseURL, list.Items[0].Spec.HostedCluster.IssuerURL)
	}
	if _, err := uuid.Parse(issuerID); err != nil {
		t.Errorf("expected issuerURL to end in a UUID, got %s", issuerID)
	}
	if got := list.Items[0].Spec.OidcConfigID; got != "" {
		t.Errorf("expected empty oidcConfigId on legacy-path cluster, got %s", got)
	}
}

func TestClusterHandler_Create_OidcConfigNotFound(t *testing.T) {
	scheme := newTestScheme()
	fc := fakeDB(scheme).Build()
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
	fc := &oidcConfigGetFailingClient{Client: fakeDB(scheme).Build()}
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
	fc := fakeDB(scheme).WithObjects(
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
	fc := fakeDB(scheme).WithObjects(pending).Build()
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
	fc := fakeDB(scheme).WithObjects(pending).
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
		fc := fakeDB(scheme).WithObjects(errored).Build()
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
	// In-use is signaled by the claimed-by-cluster-uid label on the OidcConfig itself, not by scanning Cluster rows.
	oidcConfig.Labels = map[string]string{hyperfleetv1alpha1.ClaimedByClusterUIDLabel: "existing-cluster-id"}
	existingCluster := testClusterCR("existing-cluster-id", "existing-cluster", testAccountID)
	existingCluster.Spec.OidcConfigID = testOidcConfigID
	fc := fakeDB(scheme).WithObjects(oidcConfig, existingCluster).
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
	fc := fakeDB(scheme).WithObjects(oidcConfigOther, existingCluster, oidcConfigMine).
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

// Races two real Creates for the same oidcConfigId against a plain fake client (no DB backstop);
// verifies the label CAS in claimOidcConfig alone enforces the 1:1 binding, and that the loser's
// cluster is rolled back.
func TestClusterHandler_Create_ConcurrentOidcConfigCollision(t *testing.T) {
	scheme := newTestScheme()
	oidcConfig := testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL)
	fc := fakeDB(scheme).WithObjects(oidcConfig).
		WithStatusSubresource(oidcConfig).Build()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

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

	var list hyperfleetv1alpha1.ClusterList
	if err := fc.List(context.Background(), &list); err != nil {
		t.Fatalf("listing clusters: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("expected the losing cluster to be rolled back, got %d clusters", len(list.Items))
	}
	var claimed hyperfleetv1alpha1.OidcConfig
	if err := fc.Get(context.Background(), client.ObjectKeyFromObject(oidcConfig), &claimed); err != nil {
		t.Fatalf("getting oidc config: %v", err)
	}
	if got := claimed.Labels[hyperfleetv1alpha1.ClaimedByClusterUIDLabel]; got != string(list.Items[0].UID) {
		t.Errorf("expected the claim to name the surviving cluster %s, got %q", list.Items[0].UID, got)
	}
}

func TestClusterHandler_Create_DerivesIssuerURLAndUpdatesLastUsed(t *testing.T) {
	scheme := newTestScheme()
	oidcConfig := testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL)
	fc := fakeDB(scheme).WithObjects(oidcConfig).
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

	// The referenced OidcConfig's lastUsedTimestamp must be stamped, and it must be
	// claimed by the new cluster's database-minted uid.
	var updated hyperfleetv1alpha1.OidcConfig
	if err := fc.Get(context.Background(), client.ObjectKeyFromObject(oidcConfig), &updated); err != nil {
		t.Fatalf("getting oidc config: %v", err)
	}
	if updated.Status.LastUsedTimestamp == nil {
		t.Error("expected status.lastUsedTimestamp to be set after cluster creation")
	}
	if got := updated.Labels[hyperfleetv1alpha1.ClaimedByClusterUIDLabel]; got != string(list.Items[0].UID) {
		t.Errorf("expected claimed-by-cluster-uid=%s, got %q", list.Items[0].UID, got)
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
	fc := fakeDB(scheme).WithObjects(cr).
		WithStatusSubresource(cr).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters/cluster-123", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"name": "test-cluster"})

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
	fc := fakeDB(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters/no-such-cluster", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"name": "no-such-cluster"})

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
	fc := fakeDB(scheme).WithObjects(
		testClusterCR("cluster-123", "test-cluster", testAccountID),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/clusters/cluster-123", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"name": "test-cluster"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	// Delete returns a plain map (not a K8s type) with the cluster name.
	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)
	if result["name"] != "test-cluster" {
		t.Errorf("expected name=test-cluster, got %v", result["name"])
	}
}

func TestClusterHandler_Delete_NotFound(t *testing.T) {
	scheme := newTestScheme()
	fc := fakeDB(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/clusters/no-such-cluster", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"name": "no-such-cluster"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestClusterHandler_Update_Success(t *testing.T) {
	scheme := newTestScheme()
	fc := fakeDB(scheme).WithObjects(
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
	req = mux.SetURLVars(req, map[string]string{"name": "test-cluster"})

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
	fc := fakeDB(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{"displayName": "x"},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v0/clusters/no-such", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"name": "no-such"})

	w := httptest.NewRecorder()
	handler.Update(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

func TestClusterHandler_Update_MissingSpec(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{"empty spec object", []byte(`{}`)},
		{"whitespace spec", []byte(`{"spec":{ }}`)},
		{"whitespace with newline spec", []byte(`{"spec":{
}}`)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme()
			fc := fakeDB(scheme).WithObjects(
				testClusterCR("cluster-123", "test-cluster", testAccountID),
			).Build()
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

			req := httptest.NewRequest(http.MethodPut, "/api/v0/clusters/cluster-123", bytes.NewReader(tt.body))
			req = req.WithContext(testContext(testAccountID))
			req = mux.SetURLVars(req, map[string]string{"name": "test-cluster"})

			w := httptest.NewRecorder()
			handler.Update(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d", w.Code)
			}
		})
	}
}

// TestClusterHandler_Update_RejectsOidcConfigIdChange verifies that
// oidcConfigId cannot be changed from one value to another via update.
func TestClusterHandler_Update_RejectsOidcConfigIdChange(t *testing.T) {
	scheme := newTestScheme()
	cr := testClusterCR("cluster-123", "test-cluster", testAccountID)
	cr.Spec.OidcConfigID = testOidcConfigID
	fc := fakeDB(scheme).WithObjects(cr).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"oidcConfigId": "some-other-oidc-config",
		},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v0/clusters/cluster-123", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"name": "test-cluster"})

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
	fc := fakeDB(scheme).WithObjects(cr).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, logger)

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"oidcConfigId": testOidcConfigID,
		},
	})

	req := httptest.NewRequest(http.MethodPut, "/api/v0/clusters/cluster-123", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"name": "test-cluster"})

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
	fc := fakeDB(scheme).WithObjects(
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

func TestClusterHandler_Create_DBError(t *testing.T) {
	scheme := newTestScheme()
	fc := fakeDB(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			return errors.New("simulated datastore failure")
		},
	}).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "https://oidc.example.com", 0, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(clusterBody("test-cluster", nil)))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500 when the datastore create fails, got %d: %s", w.Code, w.Body.String())
	}
	if msg := decodeErrorMessage(t, w); !strings.Contains(msg, ErrClusterCreateFailed.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrClusterCreateFailed.Code, msg)
	}
}

func TestClusterHandler_Create_SameNameDifferentAccount(t *testing.T) {
	otherAccount := "999999999999"
	scheme := newTestScheme()
	oidcConfig := testReadyOidcConfig(testOidcConfigID, testAccountID, testOidcConfigIssuerURL)
	fc := fakeDB(scheme).WithObjects(
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
