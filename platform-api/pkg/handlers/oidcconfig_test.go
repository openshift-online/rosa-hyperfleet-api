//go:build integration

package handlers

import (
	"bytes"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
)

const testOidcIssuerBaseURL = "https://oidc.example.com"

// testOidcConfigCR creates an OidcConfig CR with Namespace=account-<accountID>, Name=configID,
// mirroring the namespace/name scheme used by hyperfleetdb.Client for OidcConfig operations.
func testOidcConfigCR(configID, accountID string, spec hyperfleetv1alpha1.OidcConfigSpec) *hyperfleetv1alpha1.OidcConfig {
	return &hyperfleetv1alpha1.OidcConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configID,
			Namespace: "account-" + accountID,
		},
		Spec: spec,
	}
}

func testManagedOidcConfigSpec(accountID string) hyperfleetv1alpha1.OidcConfigSpec {
	return hyperfleetv1alpha1.OidcConfigSpec{
		Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
		AccountID: accountID,
	}
}

func TestOidcConfigHandler_List_Success(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testOidcConfigCR("oidc-1", testAccountID, testManagedOidcConfigSpec(testAccountID)),
		testOidcConfigCR("oidc-2", testAccountID, testManagedOidcConfigSpec(testAccountID)),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/oidc_configs", nil)
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

func TestOidcConfigHandler_List_Empty(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/oidc_configs", nil)
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
	items := result["items"].([]any)
	if len(items) != 0 {
		t.Errorf("expected 0 items, got %d", len(items))
	}
}

func TestOidcConfigHandler_List_Pagination(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testOidcConfigCR("oidc-1", testAccountID, testManagedOidcConfigSpec(testAccountID)),
		testOidcConfigCR("oidc-2", testAccountID, testManagedOidcConfigSpec(testAccountID)),
		testOidcConfigCR("oidc-3", testAccountID, testManagedOidcConfigSpec(testAccountID)),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/oidc_configs?limit=2&offset=1", nil)
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
	if int(result["limit"].(float64)) != 2 {
		t.Errorf("expected limit=2, got %v", result["limit"])
	}
	if int(result["offset"].(float64)) != 1 {
		t.Errorf("expected offset=1, got %v", result["offset"])
	}
	items := result["items"].([]any)
	if len(items) != 2 {
		t.Errorf("expected 2 items (offset=1, limit=2 of 3), got %d", len(items))
	}
}

func TestOidcConfigHandler_List_OffsetBeyondTotal(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testOidcConfigCR("oidc-1", testAccountID, testManagedOidcConfigSpec(testAccountID)),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/oidc_configs?offset=10", nil)
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	if int(result["total"].(float64)) != 1 {
		t.Errorf("expected total=1, got %v", result["total"])
	}
	items := result["items"].([]any)
	if len(items) != 0 {
		t.Errorf("expected 0 items when offset beyond total, got %d", len(items))
	}
}

func TestOidcConfigHandler_Create_Success(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)
	handler.generateID = func() string { return "generated-config-id" }

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type": "managed",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	if uid := metaField(result, "uid"); uid != "generated-config-id" {
		t.Errorf("expected metadata.uid=generated-config-id, got %v", uid)
	}
	spec := result["spec"].(map[string]any)
	if spec["type"] != "managed" {
		t.Errorf("expected spec.type=managed, got %v", spec["type"])
	}
	wantIssuerURL := testOidcIssuerBaseURL + "/generated-config-id"
	if spec["issuerUrl"] != wantIssuerURL {
		t.Errorf("expected spec.issuerUrl=%s, got %v", wantIssuerURL, spec["issuerUrl"])
	}
}

func TestOidcConfigHandler_Create_ManagedRejectsWhenIssuerBaseURLNotConfigured(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// A blank base URL must never be silently turned into a path-only
	// issuerUrl (e.g. "/generated-config-id"); the server should refuse to
	// create the config instead.
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), "", logger)
	handler.generateID = func() string { return "generated-config-id" }

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type": "managed",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if !strings.Contains(errResp["message"].(string), ErrOidcConfigCreateIssuerNotConfigured.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrOidcConfigCreateIssuerNotConfigured.Code, errResp["message"])
	}

	if _, err := handler.db.GetOidcConfig(req.Context(), testAccountID, "generated-config-id"); err == nil {
		t.Error("expected no OidcConfig CR to be created when the issuer base URL is not configured")
	}
}

func TestOidcConfigHandler_Create_ManagedIgnoresClientIssuerUrl(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)
	handler.generateID = func() string { return "generated-config-id" }

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type":      "managed",
			"issuerUrl": "https://arbitrary.example.com/bypass",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	wantIssuerURL := testOidcIssuerBaseURL + "/generated-config-id"
	spec := result["spec"].(map[string]any)
	if issuerURL, _ := spec["issuerUrl"].(string); issuerURL != wantIssuerURL {
		t.Errorf("expected client-supplied issuerUrl to be overridden with %q, got %q", wantIssuerURL, issuerURL)
	}

	cr, err := handler.db.GetOidcConfig(req.Context(), testAccountID, "generated-config-id")
	if err != nil {
		t.Fatalf("failed to fetch created CR: %v", err)
	}
	if cr.Spec.IssuerUrl != wantIssuerURL {
		t.Errorf("expected stored CR issuerUrl=%q, got %q", wantIssuerURL, cr.Spec.IssuerUrl)
	}
}

func TestOidcConfigHandler_Create_InvalidJSON(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader([]byte("not json")))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if !strings.Contains(errResp["message"].(string), ErrOidcConfigCreateInvalidBody.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrOidcConfigCreateInvalidBody.Code, errResp["message"])
	}
}

func TestOidcConfigHandler_Create_MissingFields(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{"missing spec", map[string]any{}},
		{"missing type", map[string]any{"spec": map[string]any{}}},
		{"empty type", map[string]any{"spec": map[string]any{"type": ""}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme()
			fc := fake.NewClientBuilder().WithScheme(scheme).Build()
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

			body, _ := json.Marshal(tt.body)
			req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
			req = req.WithContext(testContext(testAccountID))

			w := httptest.NewRecorder()
			handler.Create(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}

			var errResp map[string]any
			_ = json.NewDecoder(w.Body).Decode(&errResp)
			if !strings.Contains(errResp["message"].(string), ErrOidcConfigCreateMissingFields.Code) {
				t.Errorf("expected message to contain %s, got %q", ErrOidcConfigCreateMissingFields.Code, errResp["message"])
			}
		})
	}
}

func TestOidcConfigHandler_Create_InvalidType(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type": "bogus",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if !strings.Contains(errResp["message"].(string), ErrOidcConfigCreateInvalidType.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrOidcConfigCreateInvalidType.Code, errResp["message"])
	}
}

func TestOidcConfigHandler_Create_InvalidFieldsForType(t *testing.T) {
	tests := []struct {
		name string
		spec map[string]any
	}{
		{
			name: "unmanaged missing secretArn",
			spec: map[string]any{
				"type":             "unmanaged",
				"installerRoleArn": "arn:aws:iam::123456789012:role/installer",
				"issuerUrl":        "https://example.com/oidc",
			},
		},
		{
			name: "unmanaged missing installerRoleArn",
			spec: map[string]any{
				"type":      "unmanaged",
				"secretArn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:foo",
				"issuerUrl": "https://example.com/oidc",
			},
		},
		{
			name: "unmanaged missing issuerUrl",
			spec: map[string]any{
				"type":             "unmanaged",
				"secretArn":        "arn:aws:secretsmanager:us-east-1:123456789012:secret:foo",
				"installerRoleArn": "arn:aws:iam::123456789012:role/installer",
			},
		},
		{
			name: "managed with secretArn set",
			spec: map[string]any{
				"type":      "managed",
				"secretArn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:foo",
			},
		},
		{
			name: "managed with installerRoleArn set",
			spec: map[string]any{
				"type":             "managed",
				"installerRoleArn": "arn:aws:iam::123456789012:role/installer",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheme := newTestScheme()
			fc := fake.NewClientBuilder().WithScheme(scheme).Build()
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

			body, _ := json.Marshal(map[string]any{"spec": tt.spec})
			req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
			req = req.WithContext(testContext(testAccountID))

			w := httptest.NewRecorder()
			handler.Create(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected 400, got %d: %s", w.Code, w.Body.String())
			}

			var errResp map[string]any
			_ = json.NewDecoder(w.Body).Decode(&errResp)
			if !strings.Contains(errResp["message"].(string), ErrOidcConfigCreateInvalidFields.Code) {
				t.Errorf("expected message to contain %s, got %q", ErrOidcConfigCreateInvalidFields.Code, errResp["message"])
			}
		})
	}
}

func TestOidcConfigHandler_Create_CreatesIssuerReservation(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)
	handler.generateID = func() string { return "generated-config-id" }

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type":             "unmanaged",
			"secretArn":        "arn:aws:secretsmanager:us-east-1:123456789012:secret:foo",
			"installerRoleArn": "arn:aws:iam::123456789012:role/installer",
			"issuerUrl":        "https://example.com/oidc",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var reservation hyperfleetv1alpha1.OidcIssuerReservation
	resName := hyperfleetv1alpha1.OidcIssuerReservationName("https://example.com/oidc")
	if err := fc.Get(req.Context(), client.ObjectKey{Name: resName}, &reservation); err != nil {
		t.Fatalf("expected OidcIssuerReservation to exist, got: %v", err)
	}
	if reservation.Spec.IssuerUrl != "https://example.com/oidc" {
		t.Errorf("expected reservation issuerUrl=https://example.com/oidc, got %q", reservation.Spec.IssuerUrl)
	}
	if reservation.Labels["hyperfleet.io/account-id"] != testAccountID {
		t.Errorf("expected reservation account-id label=%s, got %q", testAccountID, reservation.Labels["hyperfleet.io/account-id"])
	}
}

func TestOidcConfigHandler_Create_UnmanagedSuccess(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)
	handler.generateID = func() string { return "generated-config-id" }

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type":             "unmanaged",
			"secretArn":        "arn:aws:secretsmanager:us-east-1:123456789012:secret:foo",
			"installerRoleArn": "arn:aws:iam::123456789012:role/installer",
			"issuerUrl":        "https://example.com/oidc",
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)
	spec := result["spec"].(map[string]any)
	if spec["type"] != "unmanaged" {
		t.Errorf("expected spec.type=unmanaged, got %v", spec["type"])
	}
}

func TestOidcConfigHandler_Create_UnmanagedDuplicateIssuerUrlSameAccount(t *testing.T) {
	scheme := newTestScheme()
	issuerUrl := "https://example.com/oidc"
	existingReservation := &hyperfleetv1alpha1.OidcIssuerReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name: hyperfleetv1alpha1.OidcIssuerReservationName(issuerUrl),
		},
		Spec: hyperfleetv1alpha1.OidcIssuerReservationSpec{IssuerUrl: issuerUrl},
	}
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingReservation).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type":             "unmanaged",
			"secretArn":        "arn:aws:secretsmanager:us-east-1:123456789012:secret:bar",
			"installerRoleArn": "arn:aws:iam::123456789012:role/installer2",
			"issuerUrl":        issuerUrl,
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if !strings.Contains(errResp["message"].(string), ErrOidcConfigCreateDuplicateIssuerUrl.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrOidcConfigCreateDuplicateIssuerUrl.Code, errResp["message"])
	}
}

func TestOidcConfigHandler_Create_UnmanagedDuplicateIssuerUrlDifferentAccountRejected(t *testing.T) {
	issuerUrl := "https://example.com/oidc"
	scheme := newTestScheme()
	existingReservation := &hyperfleetv1alpha1.OidcIssuerReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:   hyperfleetv1alpha1.OidcIssuerReservationName(issuerUrl),
			Labels: map[string]string{"hyperfleet.io/account-id": "999999999999"},
		},
		Spec: hyperfleetv1alpha1.OidcIssuerReservationSpec{IssuerUrl: issuerUrl},
	}
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existingReservation).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)
	handler.generateID = func() string { return "generated-config-id" }

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type":             "unmanaged",
			"secretArn":        "arn:aws:secretsmanager:us-east-1:123456789012:secret:bar",
			"installerRoleArn": "arn:aws:iam::123456789012:role/installer2",
			"issuerUrl":        issuerUrl,
		},
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
	req = req.WithContext(testContext(testAccountID))

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409 for cross-account duplicate issuerUrl, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOidcConfigHandler_Create_ConcurrentUnmanagedDuplicateIssuerUrl(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	var callCount int64
	handler.generateID = func() string {
		n := atomic.AddInt64(&callCount, 1)
		return fmt.Sprintf("generated-config-id-%d", n)
	}

	body, _ := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type":             "unmanaged",
			"secretArn":        "arn:aws:secretsmanager:us-east-1:123456789012:secret:foo",
			"installerRoleArn": "arn:aws:iam::123456789012:role/installer",
			"issuerUrl":        "https://example.com/oidc-concurrent",
		},
	})

	var wg sync.WaitGroup
	codes := make([]int, 2)

	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/api/v0/oidc_configs", bytes.NewReader(body))
			req = req.WithContext(testContext(testAccountID))
			w := httptest.NewRecorder()
			handler.Create(w, req)
			codes[idx] = w.Code
		}(i)
	}

	wg.Wait()

	var created, conflicts int
	for _, code := range codes {
		switch code {
		case http.StatusCreated:
			created++
		case http.StatusConflict:
			conflicts++
		}
	}
	if created != 1 || conflicts != 1 {
		t.Fatalf("expected exactly one 201 and one 409 for concurrent creates with the same issuerUrl, got codes %v", codes)
	}
}

func TestOidcConfigHandler_Get_Success(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testOidcConfigCR("oidc-123", testAccountID, testManagedOidcConfigSpec(testAccountID)),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/oidc_configs/oidc-123", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "oidc-123"})

	w := httptest.NewRecorder()
	handler.Get(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)

	if uid := metaField(result, "uid"); uid != "oidc-123" {
		t.Errorf("expected metadata.uid=oidc-123, got %v", uid)
	}
}

func TestOidcConfigHandler_Get_NotFound(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/oidc_configs/no-such-config", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "no-such-config"})

	w := httptest.NewRecorder()
	handler.Get(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if !strings.Contains(errResp["message"].(string), ErrOidcConfigGetNotFound.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrOidcConfigGetNotFound.Code, errResp["message"])
	}
}

func TestOidcConfigHandler_Get_WrongAccount(t *testing.T) {
	otherAccount := "999999999999"
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testOidcConfigCR("oidc-123", otherAccount, testManagedOidcConfigSpec(otherAccount)),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/oidc_configs/oidc-123", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "oidc-123"})

	w := httptest.NewRecorder()
	handler.Get(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for config owned by a different account, got %d: %s", w.Code, w.Body.String())
	}
}

func TestOidcConfigHandler_Delete_Success(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(
		testOidcConfigCR("oidc-123", testAccountID, testManagedOidcConfigSpec(testAccountID)),
	).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/oidc_configs/oidc-123", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "oidc-123"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", w.Code, w.Body.String())
	}

	var result map[string]any
	_ = json.NewDecoder(w.Body).Decode(&result)
	if result["config_id"] != "oidc-123" {
		t.Errorf("expected config_id=oidc-123, got %v", result["config_id"])
	}
}

func TestOidcConfigHandler_Delete_NotFound(t *testing.T) {
	scheme := newTestScheme()
	fc := fake.NewClientBuilder().WithScheme(scheme).Build()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewOidcConfigHandler(hyperfleetdb.NewClientFrom(fc, logger), testOidcIssuerBaseURL, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/oidc_configs/no-such-config", nil)
	req = req.WithContext(testContext(testAccountID))
	req = mux.SetURLVars(req, map[string]string{"id": "no-such-config"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", w.Code, w.Body.String())
	}

	var errResp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&errResp)
	if !strings.Contains(errResp["message"].(string), ErrOidcConfigDeleteNotFound.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrOidcConfigDeleteNotFound.Code, errResp["message"])
	}
}
