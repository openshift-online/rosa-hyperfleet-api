package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
)

// mockChecker implements authz.Checker for testing
type mockChecker struct {
	isAdminFn              func(ctx context.Context, accountID, principalARN string) (bool, error)
	isPrivilegedFn         func(ctx context.Context, accountID string) (bool, error)
	isAccountProvisionedFn func(ctx context.Context, accountID string) (bool, error)
	authorizeFn            func(ctx context.Context, req *authz.AuthzRequest) (bool, error)
}

func (m *mockChecker) IsAdmin(ctx context.Context, accountID, principalARN string) (bool, error) {
	if m.isAdminFn != nil {
		return m.isAdminFn(ctx, accountID, principalARN)
	}
	return false, nil
}

func (m *mockChecker) IsPrivileged(ctx context.Context, accountID string) (bool, error) {
	if m.isPrivilegedFn != nil {
		return m.isPrivilegedFn(ctx, accountID)
	}
	return false, nil
}

func (m *mockChecker) IsAccountProvisioned(ctx context.Context, accountID string) (bool, error) {
	if m.isAccountProvisionedFn != nil {
		return m.isAccountProvisionedFn(ctx, accountID)
	}
	return false, nil
}

func (m *mockChecker) Authorize(ctx context.Context, req *authz.AuthzRequest) (bool, error) {
	if m.authorizeFn != nil {
		return m.authorizeFn(ctx, req)
	}
	return false, nil
}

func TestAdminCheck_RequireAdmin_AdminCaller(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	checker := &mockChecker{
		isAdminFn: func(ctx context.Context, accountID, principalARN string) (bool, error) {
			return true, nil
		},
	}
	ac := NewAdminCheck(checker, logger)

	nextCalled := false
	handler := ac.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAccountID, "123456789012")
	ctx = context.WithValue(ctx, ContextKeyCallerARN, "arn:aws:iam::123456789012:user/admin")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !nextCalled {
		t.Error("expected next handler to be called for admin caller")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestAdminCheck_RequireAdmin_NonAdminCaller(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	checker := &mockChecker{
		isAdminFn: func(ctx context.Context, accountID, principalARN string) (bool, error) {
			return false, nil
		},
	}
	ac := NewAdminCheck(checker, logger)

	nextCalled := false
	handler := ac.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAccountID, "123456789012")
	ctx = context.WithValue(ctx, ContextKeyCallerARN, "arn:aws:iam::123456789012:user/regular")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called for non-admin caller")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}

	var errorResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errorResp["kind"] != "Status" {
		t.Errorf("expected kind=Status, got %v", errorResp["kind"])
	}
	msg, _ := errorResp["message"].(string)
	if !strings.Contains(msg, ErrNotAdmin.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrNotAdmin.Code, msg)
	}
	if !strings.Contains(msg, "This operation requires admin privileges") {
		t.Errorf("expected message to contain reason, got %q", msg)
	}
	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}

func TestAdminCheck_RequireAdmin_PrivilegedBypass(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	checker := &mockChecker{
		isAdminFn: func(ctx context.Context, accountID, principalARN string) (bool, error) {
			t.Error("IsAdmin should not be called for privileged accounts")
			return false, nil
		},
	}
	ac := NewAdminCheck(checker, logger)

	nextCalled := false
	handler := ac.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAccountID, "123456789012")
	ctx = context.WithValue(ctx, ContextKeyPrivileged, true)
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !nextCalled {
		t.Error("expected next handler to be called for privileged account")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestAdminCheck_RequireAdmin_MissingCallerARN(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	checker := &mockChecker{}
	ac := NewAdminCheck(checker, logger)

	nextCalled := false
	handler := ac.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAccountID, "123456789012")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called with missing caller ARN")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}

	var errorResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	msg, _ := errorResp["message"].(string)
	if !strings.Contains(msg, ErrMissingCallerARN.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrMissingCallerARN.Code, msg)
	}
}

func TestAdminCheck_RequireAdmin_IsAdminError(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	checker := &mockChecker{
		isAdminFn: func(ctx context.Context, accountID, principalARN string) (bool, error) {
			return false, errors.New("dynamodb connection failed")
		},
	}
	ac := NewAdminCheck(checker, logger)

	nextCalled := false
	handler := ac.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAccountID, "123456789012")
	ctx = context.WithValue(ctx, ContextKeyCallerARN, "arn:aws:iam::123456789012:user/someone")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called on IsAdmin error")
	}
	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}

	var errorResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	msg, _ := errorResp["message"].(string)
	if !strings.Contains(msg, ErrAdminCheckFailed.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrAdminCheckFailed.Code, msg)
	}
}

func TestAdminCheck_RequireAdmin_MissingAccountID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	checker := &mockChecker{}
	ac := NewAdminCheck(checker, logger)

	nextCalled := false
	handler := ac.RequireAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called with missing account ID")
	}
	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}

	var errorResp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	msg, _ := errorResp["message"].(string)
	if !strings.Contains(msg, ErrMissingAccountID.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrMissingAccountID.Code, msg)
	}
}
