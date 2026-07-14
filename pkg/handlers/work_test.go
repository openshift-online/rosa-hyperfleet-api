package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	workv1 "open-cluster-management.io/api/work/v1"
)

// mockWorkMaestroClient is a mock implementation for work tests
type mockWorkMaestroClient struct {
	createManifestWorkFunc func(ctx context.Context, clusterName string, manifestWork *workv1.ManifestWork) (*workv1.ManifestWork, error)
}

func (m *mockWorkMaestroClient) CreateManifestWork(ctx context.Context, clusterName string, manifestWork *workv1.ManifestWork) (*workv1.ManifestWork, error) {
	if m.createManifestWorkFunc != nil {
		return m.createManifestWorkFunc(ctx, clusterName, manifestWork)
	}
	return nil, errors.New("not implemented")
}

func (m *mockWorkMaestroClient) CreateConsumer(ctx context.Context, req *maestro.ConsumerCreateRequest) (*maestro.Consumer, error) {
	return nil, errors.New("not implemented")
}

func (m *mockWorkMaestroClient) ListConsumers(ctx context.Context, page, size int) (*maestro.ConsumerList, error) {
	return nil, errors.New("not implemented")
}

func (m *mockWorkMaestroClient) GetConsumer(ctx context.Context, id string) (*maestro.Consumer, error) {
	return nil, errors.New("not implemented")
}

func (m *mockWorkMaestroClient) ListResourceBundles(ctx context.Context, page, size int, search, orderBy, fields string) (*maestro.ResourceBundleList, error) {
	return nil, errors.New("not implemented")
}

func (m *mockWorkMaestroClient) GetResourceBundle(ctx context.Context, id string) (*maestro.ResourceBundle, error) {
	return nil, errors.New("not implemented")
}

func (m *mockWorkMaestroClient) GetManifestWork(ctx context.Context, clusterName string, name string) (*workv1.ManifestWork, error) {
	return nil, errors.New("not implemented")
}

func (m *mockWorkMaestroClient) DeleteResourceBundle(ctx context.Context, id string) error {
	return errors.New("not implemented")
}

func (m *mockWorkMaestroClient) DeleteManifestWork(ctx context.Context, clusterName string, name string) error {
	return nil
}

func TestWorkHandler_Create_Success(t *testing.T) {
	mockClient := &mockWorkMaestroClient{
		createManifestWorkFunc: func(ctx context.Context, clusterName string, manifestWork *workv1.ManifestWork) (*workv1.ManifestWork, error) {
			// Return a successful response
			return &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      manifestWork.Name,
					Namespace: clusterName,
					UID:       "test-uid-123",
				},
				Spec: manifestWork.Spec,
				Status: workv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{},
				},
			}, nil
		},
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := NewWorkHandler(mockClient, logger)

	// Create request body
	reqBody := map[string]interface{}{
		"cluster_id": "test-cluster-123",
		"data": map[string]interface{}{
			"apiVersion": "work.open-cluster-management.io/v1",
			"kind":       "ManifestWork",
			"metadata": map[string]interface{}{
				"name": "test-work",
			},
			"spec": map[string]interface{}{
				"workload": map[string]interface{}{
					"manifests": []map[string]interface{}{
						{
							"apiVersion": "v1",
							"kind":       "ConfigMap",
							"metadata": map[string]interface{}{
								"name":      "test-config",
								"namespace": "default",
							},
							"data": map[string]string{
								"key": "value",
							},
						},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/work", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	// Add account ID to context
	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	// Check response
	if w.Code != http.StatusCreated {
		t.Errorf("Expected status code %d, got %d", http.StatusCreated, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Verify response fields
	if resp["id"] == nil {
		t.Error("Expected id in response")
	}
	if resp["kind"] != "ManifestWork" {
		t.Errorf("Expected kind to be ManifestWork, got %v", resp["kind"])
	}
	if resp["cluster_id"] != "test-cluster-123" {
		t.Errorf("Expected cluster_id to be test-cluster-123, got %v", resp["cluster_id"])
	}
	if resp["name"] != "test-work" {
		t.Errorf("Expected name to be test-work, got %v", resp["name"])
	}
}

func TestWorkHandler_Create_MissingClusterID(t *testing.T) {
	mockClient := &mockWorkMaestroClient{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := NewWorkHandler(mockClient, logger)

	reqBody := map[string]interface{}{
		"data": map[string]interface{}{
			"apiVersion": "work.open-cluster-management.io/v1",
			"kind":       "ManifestWork",
		},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/work", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["code"] != "missing-cluster-id" {
		t.Errorf("Expected error code 'missing-cluster-id', got %v", resp["code"])
	}
}

func TestWorkHandler_Create_MissingData(t *testing.T) {
	mockClient := &mockWorkMaestroClient{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := NewWorkHandler(mockClient, logger)

	reqBody := map[string]interface{}{
		"cluster_id": "test-cluster-123",
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/work", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["code"] != "missing-data" {
		t.Errorf("Expected error code 'missing-data', got %v", resp["code"])
	}
}

func TestWorkHandler_Create_InvalidManifestWork(t *testing.T) {
	mockClient := &mockWorkMaestroClient{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := NewWorkHandler(mockClient, logger)

	reqBody := map[string]interface{}{
		"cluster_id": "test-cluster-123",
		"data": map[string]interface{}{
			"invalid": "data",
		},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/work", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["code"] != "invalid-manifestwork" {
		t.Errorf("Expected error code 'invalid-manifestwork', got %v", resp["code"])
	}
}

func TestWorkHandler_Create_WrongKind(t *testing.T) {
	mockClient := &mockWorkMaestroClient{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := NewWorkHandler(mockClient, logger)

	// Send a valid Deployment instead of ManifestWork
	reqBody := map[string]interface{}{
		"cluster_id": "test-cluster-123",
		"data": map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name": "test-deployment",
			},
		},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/work", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// The decoder fails because Deployment is not registered in the scheme,
	// so it returns "invalid-manifestwork" error
	if resp["code"] != "invalid-manifestwork" {
		t.Errorf("Expected error code 'invalid-manifestwork', got %v", resp["code"])
	}
}

func TestWorkHandler_Create_MaestroError(t *testing.T) {
	mockClient := &mockWorkMaestroClient{
		createManifestWorkFunc: func(ctx context.Context, clusterName string, manifestWork *workv1.ManifestWork) (*workv1.ManifestWork, error) {
			return nil, &maestro.Error{
				Code:   "MAESTRO-500",
				Reason: "Internal Maestro error",
			}
		},
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := NewWorkHandler(mockClient, logger)

	reqBody := map[string]interface{}{
		"cluster_id": "test-cluster-123",
		"data": map[string]interface{}{
			"apiVersion": "work.open-cluster-management.io/v1",
			"kind":       "ManifestWork",
			"metadata": map[string]interface{}{
				"name": "test-work",
			},
			"spec": map[string]interface{}{
				"workload": map[string]interface{}{
					"manifests": []map[string]interface{}{},
				},
			},
		},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/work", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadGateway {
		t.Errorf("Expected status code %d, got %d", http.StatusBadGateway, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["code"] != "MAESTRO-500" {
		t.Errorf("Expected error code 'MAESTRO-500', got %v", resp["code"])
	}
}

func TestWorkHandler_Create_GenericError(t *testing.T) {
	mockClient := &mockWorkMaestroClient{
		createManifestWorkFunc: func(ctx context.Context, clusterName string, manifestWork *workv1.ManifestWork) (*workv1.ManifestWork, error) {
			return nil, errors.New("network error")
		},
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := NewWorkHandler(mockClient, logger)

	reqBody := map[string]interface{}{
		"cluster_id": "test-cluster-123",
		"data": map[string]interface{}{
			"apiVersion": "work.open-cluster-management.io/v1",
			"kind":       "ManifestWork",
			"metadata": map[string]interface{}{
				"name": "test-work",
			},
			"spec": map[string]interface{}{
				"workload": map[string]interface{}{
					"manifests": []map[string]interface{}{},
				},
			},
		},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/work", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status code %d, got %d", http.StatusInternalServerError, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["code"] != "manifestwork-creation-failed" {
		t.Errorf("Expected error code 'manifestwork-creation-failed', got %v", resp["code"])
	}
}

func TestWorkHandler_Create_InvalidJSON(t *testing.T) {
	mockClient := &mockWorkMaestroClient{}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := NewWorkHandler(mockClient, logger)

	req := httptest.NewRequest(http.MethodPost, "/api/v0/work", bytes.NewReader([]byte("invalid json")))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["code"] != "invalid-request" {
		t.Errorf("Expected error code 'invalid-request', got %v", resp["code"])
	}
}

func TestWorkHandler_Create_WithDeployment(t *testing.T) {
	mockClient := &mockWorkMaestroClient{
		createManifestWorkFunc: func(ctx context.Context, clusterName string, manifestWork *workv1.ManifestWork) (*workv1.ManifestWork, error) {
			return &workv1.ManifestWork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      manifestWork.Name,
					Namespace: clusterName,
					UID:       "test-uid-456",
				},
				Spec: manifestWork.Spec,
				Status: workv1.ManifestWorkStatus{
					Conditions: []metav1.Condition{},
				},
			}, nil
		},
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	handler := NewWorkHandler(mockClient, logger)

	reqBody := map[string]interface{}{
		"cluster_id": "test-cluster-456",
		"data": map[string]interface{}{
			"apiVersion": "work.open-cluster-management.io/v1",
			"kind":       "ManifestWork",
			"metadata": map[string]interface{}{
				"name": "nginx-work",
			},
			"spec": map[string]interface{}{
				"workload": map[string]interface{}{
					"manifests": []map[string]interface{}{
						{
							"apiVersion": "apps/v1",
							"kind":       "Deployment",
							"metadata": map[string]interface{}{
								"name":      "nginx",
								"namespace": "default",
							},
							"spec": map[string]interface{}{
								"replicas": 1,
								"selector": map[string]interface{}{
									"matchLabels": map[string]interface{}{
										"app": "nginx",
									},
								},
								"template": map[string]interface{}{
									"metadata": map[string]interface{}{
										"labels": map[string]interface{}{
											"app": "nginx",
										},
									},
									"spec": map[string]interface{}{
										"containers": []map[string]interface{}{
											{
												"name":  "nginx",
												"image": "nginx:latest",
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v0/work", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, "test-account-123")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.Create(w, req)

	if w.Code != http.StatusCreated {
		t.Errorf("Expected status code %d, got %d", http.StatusCreated, w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["cluster_id"] != "test-cluster-456" {
		t.Errorf("Expected cluster_id to be test-cluster-456, got %v", resp["cluster_id"])
	}
	if resp["name"] != "nginx-work" {
		t.Errorf("Expected name to be nginx-work, got %v", resp["name"])
	}
}
