package server

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/openshift/rosa-hyperfleet-api/pkg/config"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
)

func TestNew(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.NewConfig()

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	if server == nil {
		t.Fatal("expected non-nil server")
	}

	if server.cfg == nil {
		t.Error("expected non-nil config")
	}

	if server.logger == nil {
		t.Error("expected non-nil logger")
	}

	if server.apiServer == nil {
		t.Error("expected non-nil apiServer")
	}

	if server.healthServer == nil {
		t.Error("expected non-nil healthServer")
	}

	if server.metricsServer == nil {
		t.Error("expected non-nil metricsServer")
	}

	if server.healthHandler == nil {
		t.Error("expected non-nil healthHandler")
	}
}

func TestNew_WithCustomConfig(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := &config.Config{
		Server: config.ServerConfig{
			APIBindAddress:     "127.0.0.1",
			APIPort:            9000,
			HealthBindAddress:  "127.0.0.1",
			HealthPort:         9001,
			MetricsBindAddress: "127.0.0.1",
			MetricsPort:        9002,
			ShutdownTimeout:    15 * time.Second,
		},
		Maestro: config.MaestroConfig{
			BaseURL: "http://localhost:8001",
			Timeout: 30 * time.Second,
		},
		Logging: config.LoggingConfig{
			Level:  "debug",
			Format: "text",
		},
		AllowedAccounts: []string{"123456789012"},
	}

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	if server.apiServer.Addr != "127.0.0.1:9000" {
		t.Errorf("expected apiServer.Addr=127.0.0.1:9000, got %s", server.apiServer.Addr)
	}

	if server.healthServer.Addr != "127.0.0.1:9001" {
		t.Errorf("expected healthServer.Addr=127.0.0.1:9001, got %s", server.healthServer.Addr)
	}

	if server.metricsServer.Addr != "127.0.0.1:9002" {
		t.Errorf("expected metricsServer.Addr=127.0.0.1:9002, got %s", server.metricsServer.Addr)
	}
}

func TestServer_HealthRoutes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.NewConfig()

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{
			name:           "liveness on API server",
			path:           "/api/v0/live",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "readiness on API server",
			path:           "/api/v0/ready",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			server.apiServer.Handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestServer_ManagementClusterRoutes_Unauthorized(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.NewConfig()
	cfg.AllowedAccounts = []string{"123456789012"}

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		path           string
		accountID      string
		expectedStatus int
	}{
		{
			name:           "POST without account ID",
			method:         http.MethodPost,
			path:           "/api/v0/management_clusters",
			accountID:      "",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "GET without account ID",
			method:         http.MethodGet,
			path:           "/api/v0/management_clusters",
			accountID:      "",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "GET by ID without account ID",
			method:         http.MethodGet,
			path:           "/api/v0/management_clusters/test-id",
			accountID:      "",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "POST with unauthorized account",
			method:         http.MethodPost,
			path:           "/api/v0/management_clusters",
			accountID:      "999999999999",
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.accountID != "" {
				ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, tt.accountID)
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()

			server.apiServer.Handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestServer_ResourceBundleRoutes_Unauthorized(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.NewConfig()
	cfg.AllowedAccounts = []string{"123456789012"}

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	tests := []struct {
		name           string
		method         string
		path           string
		accountID      string
		expectedStatus int
	}{
		{
			name:           "GET without account ID",
			method:         http.MethodGet,
			path:           "/api/v0/resource_bundles",
			accountID:      "",
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "GET with unauthorized account",
			method:         http.MethodGet,
			path:           "/api/v0/resource_bundles",
			accountID:      "999999999999",
			expectedStatus: http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.accountID != "" {
				ctx := context.WithValue(req.Context(), middleware.ContextKeyAccountID, tt.accountID)
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()

			server.apiServer.Handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestServer_IdentityMiddleware(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.NewConfig()
	cfg.AllowedAccounts = []string{"123456789012"}

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	// Test that identity middleware extracts headers
	req := httptest.NewRequest(http.MethodGet, "/api/v0/live", nil)
	req.Header.Set(middleware.HeaderAccountID, "123456789012")
	req.Header.Set(middleware.HeaderCallerARN, "arn:aws:iam::123456789012:user/test")
	req.Header.Set(middleware.HeaderRequestID, "test-request-123")

	w := httptest.NewRecorder()
	server.apiServer.Handler.ServeHTTP(w, req)

	// The health endpoint should still return OK
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestServer_MetricsRoute(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.NewConfig()

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()

	server.metricsServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	// Verify it returns Prometheus metrics format
	body := w.Body.String()
	if body == "" {
		t.Error("expected non-empty metrics response")
	}
}

func TestServer_HealthServerRoutes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.NewConfig()

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{
			name:           "healthz endpoint",
			path:           "/healthz",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "readyz endpoint",
			path:           "/readyz",
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			server.healthServer.Handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestServer_InvalidRoutes(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.NewConfig()

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	tests := []struct {
		name           string
		path           string
		expectedStatus int
	}{
		{
			name:           "invalid API path",
			path:           "/api/v0/invalid",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "root path",
			path:           "/",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "invalid health path",
			path:           "/health",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			server.apiServer.Handler.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestServer_ReadinessToggle(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.NewConfig()

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	// Initially should be ready
	req := httptest.NewRequest(http.MethodGet, "/api/v0/ready", nil)
	w := httptest.NewRecorder()
	server.apiServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 when ready, got %d", w.Code)
	}

	// Set not ready
	server.healthHandler.SetReady(false)

	req = httptest.NewRequest(http.MethodGet, "/api/v0/ready", nil)
	w = httptest.NewRecorder()
	server.apiServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503 when not ready, got %d", w.Code)
	}

	// Set ready again
	server.healthHandler.SetReady(true)

	req = httptest.NewRequest(http.MethodGet, "/api/v0/ready", nil)
	w = httptest.NewRecorder()
	server.apiServer.Handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 when ready again, got %d", w.Code)
	}
}

func TestServer_ServerAddresses(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := &config.Config{
		Server: config.ServerConfig{
			APIBindAddress:     "0.0.0.0",
			APIPort:            8000,
			HealthBindAddress:  "0.0.0.0",
			HealthPort:         8080,
			MetricsBindAddress: "0.0.0.0",
			MetricsPort:        9090,
			ShutdownTimeout:    30 * time.Second,
		},
		Maestro: config.MaestroConfig{
			BaseURL: "http://maestro:8000",
			Timeout: 30 * time.Second,
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		AllowedAccounts: []string{},
	}

	server, err := New(cfg, logger)
	if err != nil {
		t.Fatalf("unexpected error creating server: %v", err)
	}

	if server.apiServer.Addr != "0.0.0.0:8000" {
		t.Errorf("expected apiServer.Addr=0.0.0.0:8000, got %s", server.apiServer.Addr)
	}

	if server.healthServer.Addr != "0.0.0.0:8080" {
		t.Errorf("expected healthServer.Addr=0.0.0.0:8080, got %s", server.healthServer.Addr)
	}

	if server.metricsServer.Addr != "0.0.0.0:9090" {
		t.Errorf("expected metricsServer.Addr=0.0.0.0:9090, got %s", server.metricsServer.Addr)
	}

	// Verify timeouts
	if server.apiServer.ReadTimeout != 30*time.Second {
		t.Errorf("expected apiServer.ReadTimeout=30s, got %v", server.apiServer.ReadTimeout)
	}

	if server.apiServer.WriteTimeout != 30*time.Second {
		t.Errorf("expected apiServer.WriteTimeout=30s, got %v", server.apiServer.WriteTimeout)
	}

	if server.healthServer.ReadTimeout != 10*time.Second {
		t.Errorf("expected healthServer.ReadTimeout=10s, got %v", server.healthServer.ReadTimeout)
	}

	if server.healthServer.WriteTimeout != 10*time.Second {
		t.Errorf("expected healthServer.WriteTimeout=10s, got %v", server.healthServer.WriteTimeout)
	}
}
