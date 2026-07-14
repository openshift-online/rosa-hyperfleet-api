package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	workv1 "open-cluster-management.io/api/work/v1"
)

// mockMaestroClient is a mock implementation of the Maestro client
type mockMaestroClient struct {
	listResourceBundlesFunc  func(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error)
	deleteResourceBundleFunc func(ctx context.Context, id string) error
}

func (m *mockMaestroClient) ListResourceBundles(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error) {
	if m.listResourceBundlesFunc != nil {
		return m.listResourceBundlesFunc(ctx, page, size, search, orderBy, fields)
	}
	return nil, errors.New("not implemented")
}

func (m *mockMaestroClient) GetResourceBundle(ctx context.Context, id string) (*maestro.ResourceBundle, error) {
	return nil, errors.New("not implemented")
}

func (m *mockMaestroClient) GetManifestWork(ctx context.Context, clusterName string, name string) (*workv1.ManifestWork, error) {
	return nil, errors.New("not implemented")
}

func (m *mockMaestroClient) DeleteResourceBundle(ctx context.Context, id string) error {
	if m.deleteResourceBundleFunc != nil {
		return m.deleteResourceBundleFunc(ctx, id)
	}
	return errors.New("not implemented")
}

func (m *mockMaestroClient) CreateConsumer(ctx context.Context, req *maestro.ConsumerCreateRequest) (*maestro.Consumer, error) {
	return nil, errors.New("not implemented")
}

func (m *mockMaestroClient) ListConsumers(ctx context.Context, page, size int) (*maestro.ConsumerList, error) {
	return nil, errors.New("not implemented")
}

func (m *mockMaestroClient) GetConsumer(ctx context.Context, id string) (*maestro.Consumer, error) {
	return nil, errors.New("not implemented")
}

func (m *mockMaestroClient) CreateManifestWork(ctx context.Context, clusterName string, manifestWork *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	return nil, errors.New("not implemented")
}

func (m *mockMaestroClient) DeleteManifestWork(ctx context.Context, clusterName string, name string) error {
	return nil
}

func TestResourceBundleHandler_List_Success(t *testing.T) {
	now := time.Now()
	expectedList := &maestro.ResourceBundleList{
		Kind:  "ResourceBundleList",
		Page:  1,
		Size:  100,
		Total: 2,
		Items: []maestro.ResourceBundle{
			{
				ID:           "rb-1",
				Kind:         "ResourceBundle",
				Href:         "/api/maestro/v1/resource-bundles/rb-1",
				Name:         "test-bundle-1",
				ConsumerName: "consumer-1",
				Version:      1,
				CreatedAt:    &now,
				UpdatedAt:    &now,
				Metadata: map[string]interface{}{
					"key1": "value1",
				},
			},
			{
				ID:           "rb-2",
				Kind:         "ResourceBundle",
				Href:         "/api/maestro/v1/resource-bundles/rb-2",
				Name:         "test-bundle-2",
				ConsumerName: "consumer-2",
				Version:      2,
				CreatedAt:    &now,
				UpdatedAt:    &now,
			},
		},
	}

	mockClient := &mockMaestroClient{
		listResourceBundlesFunc: func(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error) {
			if page != 1 || size != 100 {
				t.Errorf("expected page=1, size=100, got page=%d, size=%d", page, size)
			}
			return expectedList, nil
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewResourceBundleHandler(mockClient, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/resource_bundles", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	var result maestro.ResourceBundleList
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result.Total != 2 {
		t.Errorf("expected total=2, got %d", result.Total)
	}

	if len(result.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(result.Items))
	}

	if result.Items[0].ID != "rb-1" {
		t.Errorf("expected first item ID=rb-1, got %s", result.Items[0].ID)
	}
}

func TestResourceBundleHandler_List_WithPagination(t *testing.T) {
	tests := []struct {
		name         string
		queryParams  string
		expectedPage int
		expectedSize int
	}{
		{
			name:         "custom page and size",
			queryParams:  "?page=2&size=50",
			expectedPage: 2,
			expectedSize: 50,
		},
		{
			name:         "only page",
			queryParams:  "?page=3",
			expectedPage: 3,
			expectedSize: 100,
		},
		{
			name:         "only size",
			queryParams:  "?size=25",
			expectedPage: 1,
			expectedSize: 25,
		},
		{
			name:         "invalid page (negative)",
			queryParams:  "?page=-1&size=50",
			expectedPage: 1,
			expectedSize: 50,
		},
		{
			name:         "invalid page (zero)",
			queryParams:  "?page=0&size=50",
			expectedPage: 1,
			expectedSize: 50,
		},
		{
			name:         "invalid size (negative)",
			queryParams:  "?page=2&size=-10",
			expectedPage: 2,
			expectedSize: 100,
		},
		{
			name:         "invalid page (not a number)",
			queryParams:  "?page=abc&size=50",
			expectedPage: 1,
			expectedSize: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockMaestroClient{
				listResourceBundlesFunc: func(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error) {
					if page != tt.expectedPage {
						t.Errorf("expected page=%d, got %d", tt.expectedPage, page)
					}
					if size != tt.expectedSize {
						t.Errorf("expected size=%d, got %d", tt.expectedSize, size)
					}
					return &maestro.ResourceBundleList{
						Kind:  "ResourceBundleList",
						Page:  page,
						Size:  size,
						Total: 0,
						Items: []maestro.ResourceBundle{},
					}, nil
				},
			}

			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			handler := NewResourceBundleHandler(mockClient, logger)

			req := httptest.NewRequest(http.MethodGet, "/api/v0/resource_bundles"+tt.queryParams, nil)
			ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
			req = req.WithContext(ctx)

			w := httptest.NewRecorder()
			handler.List(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}
		})
	}
}

func TestResourceBundleHandler_List_WithQueryParams(t *testing.T) {
	tests := []struct {
		name           string
		queryParams    string
		expectedSearch string
		expectedOrder  string
		expectedFields string
	}{
		{
			name:           "with search",
			queryParams:    "?search=name%3D%27test%27",
			expectedSearch: "name='test'",
			expectedOrder:  "",
			expectedFields: "",
		},
		{
			name:           "with orderBy",
			queryParams:    "?orderBy=name%20asc",
			expectedSearch: "",
			expectedOrder:  "name asc",
			expectedFields: "",
		},
		{
			name:           "with fields",
			queryParams:    "?fields=id,name,version",
			expectedSearch: "",
			expectedOrder:  "",
			expectedFields: "id,name,version",
		},
		{
			name:           "with all query params",
			queryParams:    "?page=2&size=50&search=name%3D%27test%27&orderBy=created_at%20desc&fields=id,name",
			expectedSearch: "name='test'",
			expectedOrder:  "created_at desc",
			expectedFields: "id,name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockMaestroClient{
				listResourceBundlesFunc: func(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error) {
					if search != tt.expectedSearch {
						t.Errorf("expected search=%q, got %q", tt.expectedSearch, search)
					}
					if orderBy != tt.expectedOrder {
						t.Errorf("expected orderBy=%q, got %q", tt.expectedOrder, orderBy)
					}
					if fields != tt.expectedFields {
						t.Errorf("expected fields=%q, got %q", tt.expectedFields, fields)
					}
					return &maestro.ResourceBundleList{
						Kind:  "ResourceBundleList",
						Page:  page,
						Size:  size,
						Total: 0,
						Items: []maestro.ResourceBundle{},
					}, nil
				},
			}

			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			handler := NewResourceBundleHandler(mockClient, logger)

			req := httptest.NewRequest(http.MethodGet, "/api/v0/resource_bundles"+tt.queryParams, nil)
			ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
			req = req.WithContext(ctx)

			w := httptest.NewRecorder()
			handler.List(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}
		})
	}
}

func TestResourceBundleHandler_List_MaestroError(t *testing.T) {
	mockClient := &mockMaestroClient{
		listResourceBundlesFunc: func(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error) {
			return nil, &maestro.Error{
				Kind:   "Error",
				Code:   "maestro-500",
				Reason: "Internal Maestro error",
			}
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewResourceBundleHandler(mockClient, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/resource_bundles", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.List(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["kind"] != "Error" {
		t.Errorf("expected kind=Error, got %v", errorResp["kind"])
	}

	if errorResp["code"] != "maestro-500" {
		t.Errorf("expected code=maestro-500, got %v", errorResp["code"])
	}

	if errorResp["reason"] != "Internal Maestro error" {
		t.Errorf("expected reason='Internal Maestro error', got %v", errorResp["reason"])
	}
}

func TestResourceBundleHandler_List_GenericError(t *testing.T) {
	mockClient := &mockMaestroClient{
		listResourceBundlesFunc: func(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error) {
			return nil, errors.New("network error")
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewResourceBundleHandler(mockClient, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/resource_bundles", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.List(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["kind"] != "Error" {
		t.Errorf("expected kind=Error, got %v", errorResp["kind"])
	}

	if errorResp["code"] != "maestro-error" {
		t.Errorf("expected code=maestro-error, got %v", errorResp["code"])
	}

	if errorResp["reason"] != "Failed to list resource bundles" {
		t.Errorf("expected reason='Failed to list resource bundles', got %v", errorResp["reason"])
	}
}

func TestResourceBundleHandler_WriteError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewResourceBundleHandler(nil, logger)

	tests := []struct {
		name           string
		status         int
		code           string
		reason         string
		expectedStatus int
		expectedCode   string
		expectedReason string
	}{
		{
			name:           "bad request error",
			status:         http.StatusBadRequest,
			code:           "invalid-request",
			reason:         "Invalid request parameters",
			expectedStatus: http.StatusBadRequest,
			expectedCode:   "invalid-request",
			expectedReason: "Invalid request parameters",
		},
		{
			name:           "internal server error",
			status:         http.StatusInternalServerError,
			code:           "internal-error",
			reason:         "Something went wrong",
			expectedStatus: http.StatusInternalServerError,
			expectedCode:   "internal-error",
			expectedReason: "Something went wrong",
		},
		{
			name:           "bad gateway error",
			status:         http.StatusBadGateway,
			code:           "maestro-error",
			reason:         "Maestro service unavailable",
			expectedStatus: http.StatusBadGateway,
			expectedCode:   "maestro-error",
			expectedReason: "Maestro service unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			handler.writeError(w, tt.status, tt.code, tt.reason)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
				t.Errorf("expected Content-Type application/json, got %s", contentType)
			}

			var errorResp map[string]interface{}
			if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
				t.Fatalf("failed to decode error response: %v", err)
			}

			if errorResp["kind"] != "Error" {
				t.Errorf("expected kind=Error, got %v", errorResp["kind"])
			}

			if errorResp["code"] != tt.expectedCode {
				t.Errorf("expected code=%s, got %v", tt.expectedCode, errorResp["code"])
			}

			if errorResp["reason"] != tt.expectedReason {
				t.Errorf("expected reason=%s, got %v", tt.expectedReason, errorResp["reason"])
			}
		})
	}
}

func TestResourceBundleHandler_Delete_Success(t *testing.T) {
	mockClient := &mockMaestroClient{
		deleteResourceBundleFunc: func(ctx context.Context, id string) error {
			if id != "rb-123" {
				t.Errorf("expected id=rb-123, got %s", id)
			}
			return nil
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewResourceBundleHandler(mockClient, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/resource_bundles/rb-123", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	// Set up the mux vars to simulate the route parameter
	req = mux.SetURLVars(req, map[string]string{"id": "rb-123"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status 204, got %d", w.Code)
	}
}

func TestResourceBundleHandler_Delete_NotFound(t *testing.T) {
	mockClient := &mockMaestroClient{
		deleteResourceBundleFunc: func(ctx context.Context, id string) error {
			return &maestro.Error{
				Kind:   "Error",
				Code:   "404",
				Reason: "Resource bundle not found",
			}
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewResourceBundleHandler(mockClient, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/resource_bundles/rb-999", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	req = mux.SetURLVars(req, map[string]string{"id": "rb-999"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["kind"] != "Error" {
		t.Errorf("expected kind=Error, got %v", errorResp["kind"])
	}

	if errorResp["code"] != "404" {
		t.Errorf("expected code=404, got %v", errorResp["code"])
	}
}

func TestResourceBundleHandler_Delete_MaestroError(t *testing.T) {
	mockClient := &mockMaestroClient{
		deleteResourceBundleFunc: func(ctx context.Context, id string) error {
			return &maestro.Error{
				Kind:   "Error",
				Code:   "maestro-500",
				Reason: "Internal Maestro error",
			}
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewResourceBundleHandler(mockClient, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/resource_bundles/rb-123", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	req = mux.SetURLVars(req, map[string]string{"id": "rb-123"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("expected status 502, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["code"] != "maestro-500" {
		t.Errorf("expected code=maestro-500, got %v", errorResp["code"])
	}
}

func TestResourceBundleHandler_Delete_GenericError(t *testing.T) {
	mockClient := &mockMaestroClient{
		deleteResourceBundleFunc: func(ctx context.Context, id string) error {
			return errors.New("network error")
		},
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewResourceBundleHandler(mockClient, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/resource_bundles/rb-123", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	req = mux.SetURLVars(req, map[string]string{"id": "rb-123"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["code"] != "maestro-error" {
		t.Errorf("expected code=maestro-error, got %v", errorResp["code"])
	}

	if errorResp["reason"] != "Failed to delete resource bundle" {
		t.Errorf("expected reason='Failed to delete resource bundle', got %v", errorResp["reason"])
	}
}

func TestResourceBundleHandler_Delete_MissingID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	handler := NewResourceBundleHandler(nil, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/resource_bundles/", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	// Empty ID
	req = mux.SetURLVars(req, map[string]string{"id": ""})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["code"] != "invalid-request" {
		t.Errorf("expected code=invalid-request, got %v", errorResp["code"])
	}
}
