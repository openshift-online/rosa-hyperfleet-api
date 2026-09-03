package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// identityMiddleware is a test helper that wires up Identity with a discard logger.
func identityMiddleware(next http.Handler) http.Handler {
	return Identity(slog.New(slog.NewTextHandler(nil, nil)))(next)
}

func TestIdentity_AllHeaders(t *testing.T) {
	handler := identityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		accountID := GetAccountID(ctx)
		if accountID != "123456789012" {
			t.Errorf("expected account_id=123456789012, got %s", accountID)
		}

		callerARN := GetCallerARN(ctx)
		if callerARN != "arn:aws:iam::123456789012:user/testuser" {
			t.Errorf("expected caller_arn=arn:aws:iam::123456789012:user/testuser, got %s", callerARN)
		}

		requestID := GetRequestID(ctx)
		if requestID != "test-request-123" {
			t.Errorf("expected request_id=test-request-123, got %s", requestID)
		}

		userID := ctx.Value(ContextKeyUserID)
		if userID != "test-user-id-0123456" {
			t.Errorf("expected user_id=test-user-id-0123456, got %v", userID)
		}

		sourceIP := ctx.Value(ContextKeySourceIP)
		if sourceIP != "192.168.1.1" {
			t.Errorf("expected source_ip=192.168.1.1, got %v", sourceIP)
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(HeaderAccountID, "123456789012")
	req.Header.Set(HeaderCallerARN, "arn:aws:iam::123456789012:user/testuser")
	req.Header.Set(HeaderUserID, "test-user-id-0123456")
	req.Header.Set(HeaderSourceIP, "192.168.1.1")
	req.Header.Set(HeaderRequestID, "test-request-123")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestIdentity_NoHeaders(t *testing.T) {
	handler := identityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		accountID := GetAccountID(ctx)
		if accountID != "" {
			t.Errorf("expected empty account_id, got %s", accountID)
		}

		callerARN := GetCallerARN(ctx)
		if callerARN != "" {
			t.Errorf("expected empty caller_arn, got %s", callerARN)
		}

		requestID := GetRequestID(ctx)
		if requestID != "" {
			t.Errorf("expected empty request_id, got %s", requestID)
		}

		userID := ctx.Value(ContextKeyUserID)
		if userID != nil {
			t.Errorf("expected nil user_id, got %v", userID)
		}

		sourceIP := ctx.Value(ContextKeySourceIP)
		if sourceIP != nil {
			t.Errorf("expected nil source_ip, got %v", sourceIP)
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestIdentity_PartialHeaders(t *testing.T) {
	tests := []struct {
		name            string
		setHeaders      map[string]string
		expectAccountID string
		expectCallerARN string
		expectRequestID string
	}{
		{
			name: "only account ID",
			setHeaders: map[string]string{
				HeaderAccountID: "123456789012",
			},
			expectAccountID: "123456789012",
			expectCallerARN: "",
			expectRequestID: "",
		},
		{
			name: "only caller ARN",
			setHeaders: map[string]string{
				HeaderCallerARN: "arn:aws:iam::123456789012:user/testuser",
			},
			expectAccountID: "",
			expectCallerARN: "arn:aws:iam::123456789012:user/testuser",
			expectRequestID: "",
		},
		{
			name: "only request ID",
			setHeaders: map[string]string{
				HeaderRequestID: "req-123",
			},
			expectAccountID: "",
			expectCallerARN: "",
			expectRequestID: "req-123",
		},
		{
			name: "account ID and caller ARN",
			setHeaders: map[string]string{
				HeaderAccountID: "123456789012",
				HeaderCallerARN: "arn:aws:iam::123456789012:user/testuser",
			},
			expectAccountID: "123456789012",
			expectCallerARN: "arn:aws:iam::123456789012:user/testuser",
			expectRequestID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := identityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()

				accountID := GetAccountID(ctx)
				if accountID != tt.expectAccountID {
					t.Errorf("expected account_id=%s, got %s", tt.expectAccountID, accountID)
				}

				callerARN := GetCallerARN(ctx)
				if callerARN != tt.expectCallerARN {
					t.Errorf("expected caller_arn=%s, got %s", tt.expectCallerARN, callerARN)
				}

				requestID := GetRequestID(ctx)
				if requestID != tt.expectRequestID {
					t.Errorf("expected request_id=%s, got %s", tt.expectRequestID, requestID)
				}

				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			for header, value := range tt.setHeaders {
				req.Header.Set(header, value)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", w.Code)
			}
		})
	}
}

func TestIdentity_EmptyHeaderValues(t *testing.T) {
	handler := identityMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		accountID := GetAccountID(ctx)
		if accountID != "" {
			t.Errorf("expected empty account_id for empty header, got %s", accountID)
		}

		callerARN := GetCallerARN(ctx)
		if callerARN != "" {
			t.Errorf("expected empty caller_arn for empty header, got %s", callerARN)
		}

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(HeaderAccountID, "")
	req.Header.Set(HeaderCallerARN, "")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestIdentity_AccountIDValidation(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		accountID      string
		expectStatus   int
		expectNextCall bool
	}{
		{"123456789012", http.StatusOK, true},           // valid 12-digit
		{"1234", http.StatusBadRequest, false},          // too short
		{"12345678901a", http.StatusBadRequest, false},  // non-numeric
		{"1234567890123", http.StatusBadRequest, false}, // 13 digits
		{"", http.StatusOK, true},                       // absent header — allowed (auth middleware checks separately)
	}

	for _, tt := range tests {
		t.Run(tt.accountID, func(t *testing.T) {
			nextCalled = false
			handler := identityMiddleware(next)
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			if tt.accountID != "" {
				req.Header.Set(HeaderAccountID, tt.accountID)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tt.expectStatus {
				t.Errorf("account_id=%q: expected status %d, got %d", tt.accountID, tt.expectStatus, w.Code)
			}
			if nextCalled != tt.expectNextCall {
				t.Errorf("account_id=%q: nextCalled=%v, want %v", tt.accountID, nextCalled, tt.expectNextCall)
			}
		})
	}
}

func TestGetAccountID_EmptyContext(t *testing.T) {
	ctx := context.Background()
	accountID := GetAccountID(ctx)
	if accountID != "" {
		t.Errorf("expected empty account_id from empty context, got %s", accountID)
	}
}

func TestGetCallerARN_EmptyContext(t *testing.T) {
	ctx := context.Background()
	callerARN := GetCallerARN(ctx)
	if callerARN != "" {
		t.Errorf("expected empty caller_arn from empty context, got %s", callerARN)
	}
}

func TestGetRequestID_EmptyContext(t *testing.T) {
	ctx := context.Background()
	requestID := GetRequestID(ctx)
	if requestID != "" {
		t.Errorf("expected empty request_id from empty context, got %s", requestID)
	}
}

func TestGetAccountID_WithValue(t *testing.T) {
	ctx := context.WithValue(context.Background(), ContextKeyAccountID, "123456789012")
	accountID := GetAccountID(ctx)
	if accountID != "123456789012" {
		t.Errorf("expected account_id=123456789012, got %s", accountID)
	}
}

func TestGetCallerARN_WithValue(t *testing.T) {
	ctx := context.WithValue(context.Background(), ContextKeyCallerARN, "arn:aws:iam::123456789012:user/test")
	callerARN := GetCallerARN(ctx)
	if callerARN != "arn:aws:iam::123456789012:user/test" {
		t.Errorf("expected caller_arn=arn:aws:iam::123456789012:user/test, got %s", callerARN)
	}
}

func TestGetRequestID_WithValue(t *testing.T) {
	ctx := context.WithValue(context.Background(), ContextKeyRequestID, "req-abc-123")
	requestID := GetRequestID(ctx)
	if requestID != "req-abc-123" {
		t.Errorf("expected request_id=req-abc-123, got %s", requestID)
	}
}
