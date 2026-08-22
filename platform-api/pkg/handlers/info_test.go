package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInfoHandler_Success(t *testing.T) {
	t.Setenv("TARGET_GROUP_ARN", "arn:aws:elasticloadbalancing:us-east-1:123456789012:targetgroup/rosa-api/abc123")

	handler := NewInfoHandler(slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/api/v0/info", nil)
	w := httptest.NewRecorder()
	handler.Info(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}

	var result map[string]string
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	expected := "arn:aws:iam::123456789012:role/LambdaExecutor"
	if result["arn"] != expected {
		t.Errorf("expected arn=%s, got %s", expected, result["arn"])
	}
}

func TestInfoHandler_MissingEnvVar(t *testing.T) {
	t.Setenv("TARGET_GROUP_ARN", "")

	handler := NewInfoHandler(slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/api/v0/info", nil)
	w := httptest.NewRecorder()
	handler.Info(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}

	var result map[string]any
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	msg, _ := result["message"].(string)
	if !strings.Contains(msg, ErrInfoRegionalAccountUnavailable.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrInfoRegionalAccountUnavailable.Code, msg)
	}
}

func TestInfoHandler_MalformedARN(t *testing.T) {
	t.Setenv("TARGET_GROUP_ARN", "not-a-valid-arn")

	handler := NewInfoHandler(slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/api/v0/info", nil)
	w := httptest.NewRecorder()
	handler.Info(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", w.Code)
	}

	var result map[string]any
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	msg, _ := result["message"].(string)
	if !strings.Contains(msg, ErrInfoRegionalAccountUnavailable.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrInfoRegionalAccountUnavailable.Code, msg)
	}
}
