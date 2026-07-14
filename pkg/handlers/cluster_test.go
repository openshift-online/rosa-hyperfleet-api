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
	"time"

	"github.com/gorilla/mux"
	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/hyperfleet"
	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/config"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
)

// TestClusterHandler_List_Success tests successful cluster listing
func TestClusterHandler_List_Success(t *testing.T) {
	now := time.Now()

	// Mock hyperfleet server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/hyperfleet/v1/clusters" {
			resp := hyperfleet.HFClusterList{
				Items: []hyperfleet.HFCluster{
					{
						ID:         "cluster-1",
						Name:       "test-cluster-1",
						Labels:     map[string]string{"target_project_id": "project-1"},
						Spec:       map[string]interface{}{"provider": "aws"},
						Generation: 1,
						CreatedBy:  "user@example.com",
						CreatedAt:  now,
						UpdatedAt:  now,
					},
					{
						ID:         "cluster-2",
						Name:       "test-cluster-2",
						Labels:     map[string]string{"target_project_id": "project-2"},
						Spec:       map[string]interface{}{"provider": "gcp"},
						Generation: 1,
						CreatedBy:  "user@example.com",
						CreatedAt:  now,
						UpdatedAt:  now,
					},
				},
				TotalCount: 2,
				Page:       1,
				PageSize:   50,
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.List(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if int(result["total"].(float64)) != 2 {
		t.Errorf("expected total=2, got %v", result["total"])
	}

	items := result["items"].([]interface{})
	if len(items) != 2 {
		t.Errorf("expected 2 items, got %d", len(items))
	}
}

// TestClusterHandler_List_WithPagination tests pagination parameters
func TestClusterHandler_List_WithPagination(t *testing.T) {
	tests := []struct {
		name             string
		queryParams      string
		expectedPage     int
		expectedPageSize int
	}{
		{
			name:             "custom limit and offset",
			queryParams:      "?limit=10&offset=10",
			expectedPage:     2,
			expectedPageSize: 10,
		},
		{
			name:             "default pagination",
			queryParams:      "",
			expectedPage:     1,
			expectedPageSize: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet && r.URL.Path == "/api/hyperfleet/v1/clusters" {
					// Just verify request was received - pagination conversion is tested in client tests

					resp := hyperfleet.HFClusterList{
						Items:      []hyperfleet.HFCluster{},
						TotalCount: 0,
						Page:       tt.expectedPage,
						PageSize:   tt.expectedPageSize,
					}
					w.WriteHeader(http.StatusOK)
					_ = json.NewEncoder(w).Encode(resp)
				}
			}))
			defer server.Close()

			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
				BaseURL: server.URL,
				Timeout: 30 * time.Second,
			}, logger)
			handler := NewClusterHandler(hfClient, nil, logger)

			req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters"+tt.queryParams, nil)
			ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
			ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
			ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
			req = req.WithContext(ctx)

			w := httptest.NewRecorder()
			handler.List(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}
		})
	}
}

// TestClusterHandler_List_Error tests error handling in list
func TestClusterHandler_List_Error(t *testing.T) {
	// Mock hyperfleet server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"error": "internal server error",
		})
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
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

	if errorResp["code"] != "CLUSTERS-MGMT-LIST-001" {
		t.Errorf("expected code=CLUSTERS-MGMT-LIST-001, got %v", errorResp["code"])
	}
}

// TestClusterHandler_Create_Success tests successful cluster creation
func TestClusterHandler_Create_Success(t *testing.T) {
	now := time.Now()

	// Mock hyperfleet server
	hfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/hyperfleet/v1/clusters" {
			var req hyperfleet.HFClusterCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed to decode request: %v", err)
			}

			if req.Name != "new-cluster" {
				t.Errorf("expected name=new-cluster, got %s", req.Name)
			}

			// Verify cloudUrl was added to the create request (CloudFront URL only, no cluster ID)
			if req.Spec["cloudUrl"] != "https://doku78iof5s87.cloudfront.net" {
				t.Errorf("expected cloudUrl=https://doku78iof5s87.cloudfront.net in create spec, got %v", req.Spec["cloudUrl"])
			}

			// Verify placement was auto-populated from management cluster name
			if req.Spec["placement"] != "management-cluster" {
				t.Errorf("expected placement=management-cluster in create spec, got %v", req.Spec["placement"])
			}

			resp := hyperfleet.HFCluster{
				ID:         "cluster-123",
				Name:       "new-cluster",
				Labels:     map[string]string{"target_project_id": "project-1"},
				Spec:       req.Spec, // Return the spec as provided (with CloudFront URL only)
				Generation: 1,
				CreatedBy:  "user@example.com",
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer hfServer.Close()

	// Mock maestro server
	maestroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/maestro/v1/consumers" {
			resp := map[string]interface{}{
				"kind":  "ConsumerList",
				"page":  1,
				"size":  1,
				"total": 1,
				"items": []map[string]interface{}{
					{
						"id":   "mgmt-cluster-1",
						"name": "management-cluster",
						"labels": map[string]string{
							"cloudfront_url": "https://doku78iof5s87.cloudfront.net",
						},
					},
				},
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer maestroServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: hfServer.URL,
		Timeout: 30 * time.Second,
	}, logger)
	maestroClient := maestro.NewClient(config.MaestroConfig{
		BaseURL: maestroServer.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, maestroClient, logger)

	reqBody := map[string]interface{}{
		"name": "new-cluster",
		"spec": map[string]interface{}{"provider": "aws"},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["id"] != "cluster-123" {
		t.Errorf("expected ID=cluster-123, got %v", result["id"])
	}

	if result["name"] != "new-cluster" {
		t.Errorf("expected name=new-cluster, got %v", result["name"])
	}

	// Check that cloudUrl was added to spec
	spec, ok := result["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected spec to be a map, got %T", result["spec"])
	}

	expectedIssuerURL := "https://doku78iof5s87.cloudfront.net/cluster-123"
	if spec["cloudUrl"] != expectedIssuerURL {
		t.Errorf("expected cloudUrl=%s, got %v", expectedIssuerURL, spec["cloudUrl"])
	}
}

// TestClusterHandler_Create_InvalidJSON tests invalid JSON in request body
func TestClusterHandler_Create_InvalidJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: "http://localhost:8080",
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, nil, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader([]byte("invalid json")))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["code"] != "CLUSTERS-MGMT-CREATE-001" {
		t.Errorf("expected code=CLUSTERS-MGMT-CREATE-001, got %v", errorResp["code"])
	}
}

// TestClusterHandler_Create_MissingFields tests missing required fields
func TestClusterHandler_Create_MissingFields(t *testing.T) {
	tests := []struct {
		name    string
		reqBody map[string]interface{}
	}{
		{
			name:    "missing name",
			reqBody: map[string]interface{}{"spec": map[string]interface{}{"provider": "aws"}},
		},
		{
			name:    "missing spec",
			reqBody: map[string]interface{}{"name": "test-cluster"},
		},
		{
			name:    "empty name",
			reqBody: map[string]interface{}{"name": "", "spec": map[string]interface{}{"provider": "aws"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
			hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
				BaseURL: "http://localhost:8080",
				Timeout: 30 * time.Second,
			}, logger)
			handler := NewClusterHandler(hfClient, nil, logger)

			body, _ := json.Marshal(tt.reqBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(body))
			ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
			req = req.WithContext(ctx)

			w := httptest.NewRecorder()
			handler.Create(w, req)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status 400, got %d", w.Code)
			}

			var errorResp map[string]interface{}
			if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
				t.Fatalf("failed to decode error response: %v", err)
			}

			if errorResp["code"] != "CLUSTERS-MGMT-CREATE-002" {
				t.Errorf("expected code=CLUSTERS-MGMT-CREATE-002, got %v", errorResp["code"])
			}
		})
	}
}

// TestClusterHandler_Create_WithExistingPlacement tests that existing placement is not overridden
func TestClusterHandler_Create_WithExistingPlacement(t *testing.T) {
	now := time.Now()

	// Mock hyperfleet server
	hfServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/hyperfleet/v1/clusters" {
			var req hyperfleet.HFClusterCreateRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("failed to decode request: %v", err)
			}

			// Verify placement was NOT overridden
			if req.Spec["placement"] != "custom-placement" {
				t.Errorf("expected placement=custom-placement (client-provided), got %v", req.Spec["placement"])
			}

			resp := hyperfleet.HFCluster{
				ID:         "cluster-123",
				Name:       "new-cluster",
				Labels:     map[string]string{"target_project_id": "project-1"},
				Spec:       req.Spec,
				Generation: 1,
				CreatedBy:  "user@example.com",
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer hfServer.Close()

	// Mock maestro server
	maestroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/maestro/v1/consumers" {
			resp := map[string]interface{}{
				"kind":  "ConsumerList",
				"page":  1,
				"size":  1,
				"total": 1,
				"items": []map[string]interface{}{
					{
						"id":   "mgmt-cluster-1",
						"name": "management-cluster",
						"labels": map[string]string{
							"cloudfront_url": "https://doku78iof5s87.cloudfront.net",
						},
					},
				},
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer maestroServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: hfServer.URL,
		Timeout: 30 * time.Second,
	}, logger)
	maestroClient := maestro.NewClient(config.MaestroConfig{
		BaseURL: maestroServer.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, maestroClient, logger)

	reqBody := map[string]interface{}{
		"name": "new-cluster",
		"spec": map[string]interface{}{
			"provider":  "aws",
			"placement": "custom-placement", // Client-provided placement
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("expected status 201, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	spec, ok := result["spec"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected spec to be a map, got %T", result["spec"])
	}

	if spec["placement"] != "custom-placement" {
		t.Errorf("expected placement=custom-placement in response, got %v", spec["placement"])
	}
}

// TestClusterHandler_Create_NoManagementClusterName tests error when management cluster has no name
func TestClusterHandler_Create_NoManagementClusterName(t *testing.T) {
	// Mock maestro server returning management cluster without name
	maestroServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/maestro/v1/consumers" {
			resp := map[string]interface{}{
				"kind":  "ConsumerList",
				"page":  1,
				"size":  1,
				"total": 1,
				"items": []map[string]interface{}{
					{
						"id":   "mgmt-cluster-1",
						"name": "", // Empty name
						"labels": map[string]string{
							"cloudfront_url": "https://doku78iof5s87.cloudfront.net",
						},
					},
				},
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer maestroServer.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: "http://localhost:8080",
		Timeout: 30 * time.Second,
	}, logger)
	maestroClient := maestro.NewClient(config.MaestroConfig{
		BaseURL: maestroServer.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, maestroClient, logger)

	reqBody := map[string]interface{}{
		"name": "new-cluster",
		"spec": map[string]interface{}{
			"provider": "aws",
			// No placement provided
		},
	}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", bytes.NewReader(body))
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["code"] != "CLUSTERS-MGMT-CREATE-007" {
		t.Errorf("expected code=CLUSTERS-MGMT-CREATE-007, got %v", errorResp["code"])
	}

	if errorResp["reason"] != "Management cluster name not available for placement" {
		t.Errorf("expected reason about management cluster name, got %v", errorResp["reason"])
	}
}

// TestClusterHandler_Get_Success tests successful cluster retrieval
func TestClusterHandler_Get_Success(t *testing.T) {
	now := time.Now()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/hyperfleet/v1/clusters/cluster-123" {
			resp := hyperfleet.HFCluster{
				ID:         "cluster-123",
				Name:       "test-cluster",
				Labels:     map[string]string{"target_project_id": "project-1"},
				Spec:       map[string]interface{}{"provider": "aws"},
				Generation: 1,
				CreatedBy:  "user@example.com",
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters/cluster-123", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-123"})

	w := httptest.NewRecorder()
	handler.Get(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["id"] != "cluster-123" {
		t.Errorf("expected ID=cluster-123, got %v", result["id"])
	}
}

// TestClusterHandler_Get_NotFound tests cluster not found
func TestClusterHandler_Get_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/hyperfleet/v1/clusters/cluster-999" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    "404",
				"message": "cluster not found",
			})
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters/cluster-999", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-999"})

	w := httptest.NewRecorder()
	handler.Get(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["code"] != "CLUSTERS-MGMT-GET-001" {
		t.Errorf("expected code=CLUSTERS-MGMT-GET-001, got %v", errorResp["code"])
	}
}

// TestClusterHandler_Delete_Success tests successful cluster deletion
func TestClusterHandler_Delete_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/api/hyperfleet/v1/clusters/cluster-123" {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, nil, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/clusters/cluster-123", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-123"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected status 202, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["cluster_id"] != "cluster-123" {
		t.Errorf("expected cluster_id=cluster-123, got %v", result["cluster_id"])
	}
}

// TestClusterHandler_Delete_NotFound tests deleting non-existent cluster
func TestClusterHandler_Delete_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete && r.URL.Path == "/api/hyperfleet/v1/clusters/cluster-999" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    "404",
				"message": "cluster not found",
			})
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, nil, logger)

	req := httptest.NewRequest(http.MethodDelete, "/api/v0/clusters/cluster-999", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-999"})

	w := httptest.NewRecorder()
	handler.Delete(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["code"] != "CLUSTERS-MGMT-DELETE-001" {
		t.Errorf("expected code=CLUSTERS-MGMT-DELETE-001, got %v", errorResp["code"])
	}
}

// TestClusterHandler_GetStatus_Success tests successful status retrieval
func TestClusterHandler_GetStatus_Success(t *testing.T) {
	now := time.Now()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/hyperfleet/v1/clusters/cluster-123" {
			// Return cluster info
			clusterResp := hyperfleet.HFCluster{
				ID:         "cluster-123",
				Name:       "test-cluster",
				Labels:     map[string]string{"target_project_id": "project-1"},
				Spec:       map[string]interface{}{"provider": "aws"},
				Generation: 1,
				CreatedBy:  "user@example.com",
				Status: &hyperfleet.HFClusterStatus{
					ObservedGeneration: 1,
					Phase:              "Ready",
					Message:            "Cluster is ready",
					LastUpdateTime:     now,
				},
				CreatedAt: now,
				UpdatedAt: now,
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(clusterResp)
		} else if r.Method == http.MethodGet && r.URL.Path == "/api/hyperfleet/v1/clusters/cluster-123/statuses" {
			// Return adapter statuses
			statusResp := hyperfleet.HFAdapterStatusList{
				Items: []hyperfleet.HFAdapterStatus{
					{
						ClusterID:          "cluster-123",
						AdapterName:        "vpc-controller",
						ObservedGeneration: 1,
						Metadata:           map[string]interface{}{"region": "us-east-1"},
						Data:               map[string]interface{}{"vpc_id": "vpc-123"},
						LastUpdated:        now,
					},
				},
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(statusResp)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters/cluster-123/statuses", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-123"})

	w := httptest.NewRecorder()
	handler.GetStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var result map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if result["cluster_id"] != "cluster-123" {
		t.Errorf("expected cluster_id=cluster-123, got %v", result["cluster_id"])
	}

	status, ok := result["status"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected status to be a map, got %T", result["status"])
	}
	if status["phase"] != "Ready" {
		t.Errorf("expected phase=Ready, got %v", status["phase"])
	}

	if result["controller_statuses"] == nil {
		t.Fatal("expected controller_statuses to be present, got nil")
	}

	controllerStatuses, ok := result["controller_statuses"].([]interface{})
	if !ok {
		t.Fatalf("expected controller_statuses to be an array, got %T", result["controller_statuses"])
	}
	if len(controllerStatuses) != 1 {
		t.Errorf("expected 1 controller status, got %d", len(controllerStatuses))
	}

	firstController, ok := controllerStatuses[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected first controller to be a map, got %T", controllerStatuses[0])
	}
	if firstController["controller_name"] != "vpc-controller" {
		t.Errorf("expected controller_name=vpc-controller, got %v", firstController["controller_name"])
	}
}

// TestClusterHandler_GetStatus_NotFound tests status for non-existent cluster
func TestClusterHandler_GetStatus_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/api/hyperfleet/v1/clusters/cluster-999" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"code":    "404",
				"message": "cluster not found",
			})
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	hfClient := hyperfleet.NewClient(config.HyperfleetConfig{
		BaseURL: server.URL,
		Timeout: 30 * time.Second,
	}, logger)
	handler := NewClusterHandler(hfClient, nil, logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v0/clusters/cluster-999/statuses", nil)
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	ctx = context.WithValue(ctx, middleware.ContextKeyCallerARN, "arn:aws:iam::test-account-123:user/test")
	ctx = context.WithValue(ctx, middleware.ContextKeyUserID, "user@example.com")
	req = req.WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"id": "cluster-999"})

	w := httptest.NewRecorder()
	handler.GetStatus(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", w.Code)
	}

	var errorResp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if errorResp["code"] != "CLUSTERS-MGMT-STATUS-001" {
		t.Errorf("expected code=CLUSTERS-MGMT-STATUS-001, got %v", errorResp["code"])
	}
}
