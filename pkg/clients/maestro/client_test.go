package maestro

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/openshift/rosa-hyperfleet-api/pkg/config"
)

func TestNewClient(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: "http://localhost:8001",
		Timeout: 30 * time.Second,
	}

	client := NewClient(cfg, logger)

	if client == nil {
		t.Fatal("expected non-nil client")
	}

	if client.baseURL != cfg.BaseURL {
		t.Errorf("expected baseURL=%s, got %s", cfg.BaseURL, client.baseURL)
	}

	if client.httpClient == nil {
		t.Error("expected non-nil httpClient")
	}

	if client.logger == nil {
		t.Error("expected non-nil logger")
	}
}

func TestClient_CreateConsumer_Success(t *testing.T) {
	now := time.Now()
	expectedConsumer := &Consumer{
		ID:        "consumer-123",
		Kind:      "Consumer",
		Href:      "/api/maestro/v1/consumers/consumer-123",
		Name:      "test-consumer",
		Labels:    map[string]string{"env": "test"},
		CreatedAt: &now,
		UpdatedAt: &now,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST request, got %s", r.Method)
		}

		if r.URL.Path != "/api/maestro/v1/consumers" {
			t.Errorf("expected path /api/maestro/v1/consumers, got %s", r.URL.Path)
		}

		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}

		var req ConsumerCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("failed to decode request: %v", err)
		}

		if req.Name != "test-consumer" {
			t.Errorf("expected name=test-consumer, got %s", req.Name)
		}

		w.WriteHeader(http.StatusCreated)
		err := json.NewEncoder(w).Encode(expectedConsumer)
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	req := &ConsumerCreateRequest{
		Name:   "test-consumer",
		Labels: map[string]string{"env": "test"},
	}

	consumer, err := client.CreateConsumer(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if consumer.ID != "consumer-123" {
		t.Errorf("expected ID=consumer-123, got %s", consumer.ID)
	}

	if consumer.Name != "test-consumer" {
		t.Errorf("expected name=test-consumer, got %s", consumer.Name)
	}
}

func TestClient_CreateConsumer_MaestroError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		err := json.NewEncoder(w).Encode(&Error{
			Kind:   "Error",
			Code:   "invalid-request",
			Reason: "Name is required",
		})
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	req := &ConsumerCreateRequest{}
	consumer, err := client.CreateConsumer(context.Background(), req)

	if consumer != nil {
		t.Error("expected nil consumer on error")
	}

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	maestroErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}

	if maestroErr.Code != "invalid-request" {
		t.Errorf("expected code=invalid-request, got %s", maestroErr.Code)
	}

	if maestroErr.Reason != "Name is required" {
		t.Errorf("expected reason='Name is required', got %s", maestroErr.Reason)
	}
}

func TestClient_CreateConsumer_UnexpectedStatusCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, err := w.Write([]byte("Internal server error"))
		if err != nil {
			t.Fatalf("failed to write response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	req := &ConsumerCreateRequest{Name: "test"}
	consumer, err := client.CreateConsumer(context.Background(), req)

	if consumer != nil {
		t.Error("expected nil consumer on error")
	}

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "unexpected status code 500") {
		t.Errorf("expected error message to contain 'unexpected status code 500', got %s", err.Error())
	}
}

func TestClient_ListConsumers_Success(t *testing.T) {
	now := time.Now()
	expectedList := &ConsumerList{
		Kind:  "ConsumerList",
		Page:  1,
		Size:  10,
		Total: 2,
		Items: []Consumer{
			{
				ID:        "consumer-1",
				Kind:      "Consumer",
				Name:      "test-1",
				CreatedAt: &now,
			},
			{
				ID:        "consumer-2",
				Kind:      "Consumer",
				Name:      "test-2",
				CreatedAt: &now,
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET request, got %s", r.Method)
		}

		if r.URL.Path != "/api/maestro/v1/consumers" {
			t.Errorf("expected path /api/maestro/v1/consumers, got %s", r.URL.Path)
		}

		page := r.URL.Query().Get("page")
		if page != "1" {
			t.Errorf("expected page=1, got %s", page)
		}

		size := r.URL.Query().Get("size")
		if size != "10" {
			t.Errorf("expected size=10, got %s", size)
		}

		w.WriteHeader(http.StatusOK)
		err := json.NewEncoder(w).Encode(expectedList)
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	list, err := client.ListConsumers(context.Background(), 1, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if list.Total != 2 {
		t.Errorf("expected total=2, got %d", list.Total)
	}

	if len(list.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(list.Items))
	}
}

func TestClient_ListConsumers_WithoutPagination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		if page != "" {
			t.Errorf("expected no page parameter, got %s", page)
		}

		size := r.URL.Query().Get("size")
		if size != "" {
			t.Errorf("expected no size parameter, got %s", size)
		}

		w.WriteHeader(http.StatusOK)
		err := json.NewEncoder(w).Encode(&ConsumerList{
			Kind:  "ConsumerList",
			Page:  0,
			Size:  0,
			Total: 0,
			Items: []Consumer{},
		})
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	_, err := client.ListConsumers(context.Background(), 0, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_ListConsumers_MaestroError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		err := json.NewEncoder(w).Encode(&Error{
			Kind:   "Error",
			Code:   "server-error",
			Reason: "Database connection failed",
		})
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	list, err := client.ListConsumers(context.Background(), 1, 10)

	if list != nil {
		t.Error("expected nil list on error")
	}

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	maestroErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}

	if maestroErr.Code != "server-error" {
		t.Errorf("expected code=server-error, got %s", maestroErr.Code)
	}
}

func TestClient_GetConsumer_Success(t *testing.T) {
	now := time.Now()
	expectedConsumer := &Consumer{
		ID:        "consumer-123",
		Kind:      "Consumer",
		Href:      "/api/maestro/v1/consumers/consumer-123",
		Name:      "test-consumer",
		CreatedAt: &now,
		UpdatedAt: &now,
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET request, got %s", r.Method)
		}

		if r.URL.Path != "/api/maestro/v1/consumers/consumer-123" {
			t.Errorf("expected path /api/maestro/v1/consumers/consumer-123, got %s", r.URL.Path)
		}

		w.WriteHeader(http.StatusOK)
		err := json.NewEncoder(w).Encode(expectedConsumer)
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	consumer, err := client.GetConsumer(context.Background(), "consumer-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if consumer.ID != "consumer-123" {
		t.Errorf("expected ID=consumer-123, got %s", consumer.ID)
	}

	if consumer.Name != "test-consumer" {
		t.Errorf("expected name=test-consumer, got %s", consumer.Name)
	}
}

func TestClient_GetConsumer_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	consumer, err := client.GetConsumer(context.Background(), "nonexistent")

	if consumer != nil {
		t.Error("expected nil consumer for 404")
	}

	if err != nil {
		t.Errorf("expected no error for 404, got %v", err)
	}
}

func TestClient_GetConsumer_MaestroError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		err := json.NewEncoder(w).Encode(&Error{
			Kind:   "Error",
			Code:   "forbidden",
			Reason: "Access denied",
		})
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	consumer, err := client.GetConsumer(context.Background(), "consumer-123")

	if consumer != nil {
		t.Error("expected nil consumer on error")
	}

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	maestroErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}

	if maestroErr.Code != "forbidden" {
		t.Errorf("expected code=forbidden, got %s", maestroErr.Code)
	}
}

func TestClient_ListResourceBundles_Success(t *testing.T) {
	now := time.Now()
	expectedList := &ResourceBundleList{
		Kind:  "ResourceBundleList",
		Page:  1,
		Size:  10,
		Total: 2,
		Items: []ResourceBundle{
			{
				ID:           "rb-1",
				Kind:         "ResourceBundle",
				Name:         "bundle-1",
				ConsumerName: "consumer-1",
				Version:      1,
				CreatedAt:    &now,
			},
			{
				ID:           "rb-2",
				Kind:         "ResourceBundle",
				Name:         "bundle-2",
				ConsumerName: "consumer-2",
				Version:      2,
				CreatedAt:    &now,
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET request, got %s", r.Method)
		}

		if r.URL.Path != "/api/maestro/v1/resource-bundles" {
			t.Errorf("expected path /api/maestro/v1/resource-bundles, got %s", r.URL.Path)
		}

		page := r.URL.Query().Get("page")
		if page != "1" {
			t.Errorf("expected page=1, got %s", page)
		}

		size := r.URL.Query().Get("size")
		if size != "10" {
			t.Errorf("expected size=10, got %s", size)
		}

		w.WriteHeader(http.StatusOK)
		err := json.NewEncoder(w).Encode(expectedList)
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	list, err := client.ListResourceBundles(context.Background(), 1, 10, "", "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if list.Total != 2 {
		t.Errorf("expected total=2, got %d", list.Total)
	}

	if len(list.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(list.Items))
	}

	if list.Items[0].ID != "rb-1" {
		t.Errorf("expected first item ID=rb-1, got %s", list.Items[0].ID)
	}
}

func TestClient_ListResourceBundles_WithFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		search := r.URL.Query().Get("search")
		if search != "name='test'" {
			t.Errorf("expected search=name='test', got %s", search)
		}

		orderBy := r.URL.Query().Get("orderBy")
		if orderBy != "created_at desc" {
			t.Errorf("expected orderBy='created_at desc', got %s", orderBy)
		}

		fields := r.URL.Query().Get("fields")
		if fields != "id,name,version" {
			t.Errorf("expected fields=id,name,version, got %s", fields)
		}

		w.WriteHeader(http.StatusOK)
		err := json.NewEncoder(w).Encode(&ResourceBundleList{
			Kind:  "ResourceBundleList",
			Page:  1,
			Size:  10,
			Total: 0,
			Items: []ResourceBundle{},
		})
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	_, err := client.ListResourceBundles(context.Background(), 1, 10, "name='test'", "created_at desc", "id,name,version")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClient_ListResourceBundles_MaestroError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		err := json.NewEncoder(w).Encode(&Error{
			Kind:   "Error",
			Code:   "invalid-search",
			Reason: "Invalid search syntax",
		})
		if err != nil {
			t.Fatalf("failed to encode response: %v", err)
		}
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.MaestroConfig{
		BaseURL: server.URL,
		Timeout: 10 * time.Second,
	}
	client := NewClient(cfg, logger)

	list, err := client.ListResourceBundles(context.Background(), 1, 10, "invalid", "", "")

	if list != nil {
		t.Error("expected nil list on error")
	}

	if err == nil {
		t.Fatal("expected error, got nil")
	}

	maestroErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", err)
	}

	if maestroErr.Code != "invalid-search" {
		t.Errorf("expected code=invalid-search, got %s", maestroErr.Code)
	}
}

func TestError_Error(t *testing.T) {
	err := &Error{
		Kind:   "Error",
		Code:   "test-error",
		Reason: "This is a test error",
	}

	if err.Error() != "This is a test error" {
		t.Errorf("expected error message='This is a test error', got %s", err.Error())
	}
}

