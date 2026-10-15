package middleware

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdentity_AllHeaders(t *testing.T) {
	handler := Identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	handler := Identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			handler := Identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	handler := Identity(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

func TestRequireIdentity(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name, path, account, caller, code, message string
		wantStatus                                 int
	}{
		{"live", "/api/v0/live", "", "", "", "", http.StatusNoContent},
		{"ready", "/api/v0/ready", "", "", "", "", http.StatusNoContent},
		{"info", "/api/v0/info", "", "", "", "", http.StatusNoContent},
		{"missing identity", "/api/v0/clusters", "", "", "AUTH-001", "Caller account ID and ARN are required", http.StatusForbidden},
		{"missing account", "/api/v0/clusters", "", "arn:aws:iam::123456789012:user/test", "AUTH-001", "Caller account ID and ARN are required", http.StatusForbidden},
		{"missing ARN", "/api/v0/clusters", "123456789012", "", "AUTH-001", "Caller account ID and ARN are required", http.StatusForbidden},
		{"enrolled user", "/api/v0/clusters", "123456789012", "arn:aws:iam::123456789012:user/test", "", "", http.StatusNoContent},
		{"enrolled session", "/api/v0/clusters", "123456789012", "arn:aws:sts::123456789012:assumed-role/reader/session", "", "", http.StatusNoContent},
		{"unregistered", "/api/v0/clusters", "999999999999", "arn:aws:iam::999999999999:user/test", "AUTH-002", "Account is not registered", http.StatusForbidden},
		{"mismatch", "/api/v0/clusters", "123456789012", "arn:aws:iam::999999999999:user/test", "AUTH-001", "Caller identity is invalid", http.StatusForbidden},
		{"malformed ARN", "/api/v0/clusters", "123456789012", "not-an-arn", "AUTH-001", "Caller identity is invalid", http.StatusForbidden},
		{"malformed account", "/api/v0/clusters", "123", "arn:aws:iam::123:user/test", "AUTH-001", "Caller identity is invalid", http.StatusForbidden},
		{"role is not caller", "/api/v0/clusters", "123456789012", "arn:aws:iam::123456789012:role/reader", "AUTH-001", "Caller identity is invalid", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			lookupCalled := false
			lookup := func(ctx context.Context, account string) bool {
				lookupCalled = true
				if GetAccountID(ctx) != account {
					t.Fatal("enrollment lookup lost gateway identity context")
				}
				return account == "123456789012"
			}
			handler := Identity(RequireIdentity(logger, lookup)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			})))
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set(HeaderAccountID, tc.account)
			req.Header.Set(HeaderCallerARN, tc.caller)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)
			if w.Code != tc.wantStatus || called != (tc.wantStatus == http.StatusNoContent) {
				t.Fatalf("status=%d, continuation=%t, body=%s", w.Code, called, w.Body.String())
			}
			wantLookup := tc.name == "enrolled user" || tc.name == "enrolled session" || tc.name == "unregistered"
			if lookupCalled != wantLookup {
				t.Fatalf("enrollment lookup called=%t, want %t", lookupCalled, wantLookup)
			}
			if tc.code == "" {
				return
			}
			var status struct {
				Kind, Status, Reason, Message string
				Code                          int
			}
			if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
				t.Fatal(err)
			}
			if status.Kind != "Status" || status.Status != "Failure" || status.Code != http.StatusForbidden || status.Reason != "Forbidden" || status.Message != tc.code+": "+tc.message {
				t.Fatalf("unexpected structured denial: %+v", status)
			}
		})
	}
}
