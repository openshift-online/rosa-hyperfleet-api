package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestAuthorization_RequireAllowedAccount_Allowed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	auth := NewAuthorization([]string{"123456789012", "987654321098"}, logger)

	nextCalled := false
	handler := auth.RequireAllowedAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAccountID, "123456789012")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !nextCalled {
		t.Error("expected next handler to be called")
	}

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestAuthorization_RequireAllowedAccount_NotAllowed(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	auth := NewAuthorization([]string{"123456789012", "987654321098"}, logger)

	nextCalled := false
	handler := auth.RequireAllowedAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAccountID, "999999999999")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called")
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
	if !strings.Contains(msg, ErrAccountNotAllowed.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrAccountNotAllowed.Code, msg)
	}
	if !strings.Contains(msg, "account not allowed") {
		t.Errorf("expected message to contain reason, got %q", msg)
	}

	if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", contentType)
	}
}

func TestAuthorization_RequireAllowedAccount_MissingAccountID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	auth := NewAuthorization([]string{"123456789012"}, logger)

	nextCalled := false
	handler := auth.RequireAllowedAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called")
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
	if !strings.Contains(msg, ErrMissingAccountID.Code) {
		t.Errorf("expected message to contain %s, got %q", ErrMissingAccountID.Code, msg)
	}
	if !strings.Contains(msg, "Account ID header is required") {
		t.Errorf("expected message to contain reason, got %q", msg)
	}
}

func TestAuthorization_RequireAllowedAccount_EmptyAccountID(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	auth := NewAuthorization([]string{"123456789012"}, logger)

	nextCalled := false
	handler := auth.RequireAllowedAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAccountID, "")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called")
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

func TestAuthorization_RequireAllowedAccount_MultipleAllowedAccounts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	allowedAccounts := []string{
		"111111111111",
		"222222222222",
		"333333333333",
	}
	auth := NewAuthorization(allowedAccounts, logger)

	tests := []struct {
		name      string
		accountID string
		allowed   bool
	}{
		{
			name:      "first account allowed",
			accountID: "111111111111",
			allowed:   true,
		},
		{
			name:      "second account allowed",
			accountID: "222222222222",
			allowed:   true,
		},
		{
			name:      "third account allowed",
			accountID: "333333333333",
			allowed:   true,
		},
		{
			name:      "unlisted account not allowed",
			accountID: "444444444444",
			allowed:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			handler := auth.RequireAllowedAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			ctx := context.WithValue(req.Context(), ContextKeyAccountID, tt.accountID)
			req = req.WithContext(ctx)

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if tt.allowed && !nextCalled {
				t.Error("expected next handler to be called for allowed account")
			}

			if !tt.allowed && nextCalled {
				t.Error("expected next handler NOT to be called for disallowed account")
			}

			if tt.allowed && w.Code != http.StatusOK {
				t.Errorf("expected status 200 for allowed account, got %d", w.Code)
			}

			if !tt.allowed && w.Code != http.StatusForbidden {
				t.Errorf("expected status 403 for disallowed account, got %d", w.Code)
			}
		})
	}
}

func TestAuthorization_RequireAllowedAccount_EmptyAllowlist(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	auth := NewAuthorization([]string{}, logger)

	nextCalled := false
	handler := auth.RequireAllowedAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	ctx := context.WithValue(req.Context(), ContextKeyAccountID, "123456789012")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if nextCalled {
		t.Error("expected next handler NOT to be called with empty allowlist")
	}

	if w.Code != http.StatusForbidden {
		t.Errorf("expected status 403, got %d", w.Code)
	}
}

func TestNewAuthorization(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	accounts := []string{"123456789012", "987654321098", "555555555555"}
	auth := NewAuthorization(accounts, logger)

	if auth == nil {
		t.Fatal("expected non-nil Authorization")
	}

	if auth.logger == nil {
		t.Error("expected non-nil logger")
	}

	if auth.allowedAccounts == nil {
		t.Error("expected non-nil allowedAccounts map")
	}

	if len(auth.allowedAccounts) != 3 {
		t.Errorf("expected 3 allowed accounts, got %d", len(auth.allowedAccounts))
	}

	for _, acc := range accounts {
		if _, exists := auth.allowedAccounts[acc]; !exists {
			t.Errorf("expected account %s to be in allowlist", acc)
		}
	}
}

func TestNewAuthorization_NilAccounts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	auth := NewAuthorization(nil, logger)

	if auth == nil {
		t.Fatal("expected non-nil Authorization")
	}

	if auth.allowedAccounts == nil {
		t.Error("expected non-nil allowedAccounts map")
	}

	if len(auth.allowedAccounts) != 0 {
		t.Errorf("expected 0 allowed accounts, got %d", len(auth.allowedAccounts))
	}
}

func TestAuthorization_RequireAllowedAccount_TwentyAccounts(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	// Create 20 allowed accounts
	allowedAccounts := []string{
		"100000000001",
		"100000000002",
		"100000000003",
		"100000000004",
		"100000000005",
		"100000000006",
		"100000000007",
		"100000000008",
		"100000000009",
		"100000000010",
		"100000000011",
		"100000000012",
		"100000000013",
		"100000000014",
		"100000000015",
		"100000000016",
		"100000000017",
		"100000000018",
		"100000000019",
		"100000000020",
	}

	auth := NewAuthorization(allowedAccounts, logger)

	// Verify the allowlist was created correctly
	if len(auth.allowedAccounts) != 20 {
		t.Fatalf("expected 20 allowed accounts, got %d", len(auth.allowedAccounts))
	}

	// Test that all 20 accounts are allowed
	for i, accountID := range allowedAccounts {
		t.Run("account_"+accountID+"_allowed", func(t *testing.T) {
			nextCalled := false
			handler := auth.RequireAllowedAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			ctx := context.WithValue(req.Context(), ContextKeyAccountID, accountID)
			req = req.WithContext(ctx)

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if !nextCalled {
				t.Errorf("expected next handler to be called for account %s (index %d)", accountID, i)
			}

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200 for account %s, got %d", accountID, w.Code)
			}
		})
	}

	// Test that an account not in the list is denied
	t.Run("unlisted_account_denied", func(t *testing.T) {
		nextCalled := false
		handler := auth.RequireAllowedAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nextCalled = true
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(req.Context(), ContextKeyAccountID, "999999999999")
		req = req.WithContext(ctx)

		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)

		if nextCalled {
			t.Error("expected next handler NOT to be called for unlisted account")
		}

		if w.Code != http.StatusForbidden {
			t.Errorf("expected status 403 for unlisted account, got %d", w.Code)
		}

		var errorResp map[string]any
		if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
			t.Fatalf("failed to decode error response: %v", err)
		}

		msg, _ := errorResp["message"].(string)
		if !strings.Contains(msg, ErrAccountNotAllowed.Code) {
			t.Errorf("expected message to contain %s, got %q", ErrAccountNotAllowed.Code, msg)
		}
	})
}

func TestAuthorization_WriteError(t *testing.T) {
	tests := []struct {
		name           string
		def            APIError
		expectedStatus int
		expectedCode   string
		expectedReason string
	}{
		{
			name:           "account not allowed",
			def:            ErrAccountNotAllowed,
			expectedStatus: http.StatusForbidden,
			expectedCode:   ErrAccountNotAllowed.Code,
			expectedReason: ErrAccountNotAllowed.Message,
		},
		{
			name:           "missing account ID",
			def:            ErrMissingAccountID,
			expectedStatus: http.StatusForbidden,
			expectedCode:   ErrMissingAccountID.Code,
			expectedReason: ErrMissingAccountID.Message,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeError(w, tt.def, slog.Default())

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if contentType := w.Header().Get("Content-Type"); contentType != "application/json" {
				t.Errorf("expected Content-Type application/json, got %s", contentType)
			}

			var errorResp map[string]any
			if err := json.NewDecoder(w.Body).Decode(&errorResp); err != nil {
				t.Fatalf("failed to decode error response: %v", err)
			}

			if errorResp["kind"] != "Status" {
				t.Errorf("expected kind=Status, got %v", errorResp["kind"])
			}

			msg, _ := errorResp["message"].(string)
			if !strings.Contains(msg, tt.expectedCode) {
				t.Errorf("expected message to contain code %s, got %q", tt.expectedCode, msg)
			}
			if !strings.Contains(msg, tt.expectedReason) {
				t.Errorf("expected message to contain reason %s, got %q", tt.expectedReason, msg)
			}
		})
	}
}
