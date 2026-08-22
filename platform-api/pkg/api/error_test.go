package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
)

var base = api.APIError{
	Code:       "TEST-001",
	HTTPStatus: http.StatusBadRequest,
	Message:    "something went wrong",
}

// structuredError has exported fields so it marshals to non-empty JSON.
type structuredError struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

func (e *structuredError) Error() string { return e.Detail }

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return out
}

func write(def api.APIError) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	if err := api.WriteError(w, def); err != nil {
		panic("WriteError: " + err.Error())
	}
	return w
}

// --- WithErrors ---

func TestWithErrors_SetsErrors(t *testing.T) {
	payload := []string{"a", "b"}
	got := base.WithErrors(payload)
	if got.Errors == nil {
		t.Fatal("expected Errors to be set")
	}
}

func TestWithErrors_DoesNotMutateBase(t *testing.T) {
	_ = base.WithErrors("x")
	if base.Errors != nil {
		t.Fatal("WithErrors must not mutate the receiver")
	}
}

// --- WithReason ---

func TestWithReason_AppliesTemplate(t *testing.T) {
	e := api.APIError{Code: "X", HTTPStatus: 400, Message: "m", Reason: "hello %s"}
	got := e.WithReason("world")
	if got.Errors == nil {
		t.Fatal("expected Errors to be set")
	}
	if got.Errors.(error).Error() != "hello world" {
		t.Fatalf("unexpected reason: %v", got.Errors)
	}
}

func TestWithReason_WrapsErrorWithW(t *testing.T) {
	sentinel := errors.New("sentinel")
	e := api.APIError{Code: "X", HTTPStatus: 500, Message: "m", Reason: "%w"}
	got := e.WithReason(sentinel)
	if !errors.Is(got.Errors.(error), sentinel) {
		t.Fatal("expected error chain to be preserved via %w")
	}
}

func TestWithReason_PanicsWithoutTemplate(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when Reason is empty")
		}
	}()
	base.WithReason("arg")
}

func TestWithReason_DoesNotMutateBase(t *testing.T) {
	e := api.APIError{Code: "X", HTTPStatus: 400, Message: "m", Reason: "%s"}
	_ = e.WithReason("x")
	if e.Errors != nil {
		t.Fatal("WithReason must not mutate the receiver")
	}
}

// --- Write: HTTP envelope ---

func TestWrite_StatusCode(t *testing.T) {
	w := write(api.APIError{Code: "X", HTTPStatus: http.StatusNotFound, Message: "m"})
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestWrite_ContentType(t *testing.T) {
	w := write(base)
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}
}

// TestWrite_KindIsStatus verifies the response is a metav1.Status envelope.
func TestWrite_KindIsStatus(t *testing.T) {
	w := write(base)
	resp := decode(t, w)
	if resp["kind"] != "Status" {
		t.Fatalf("expected kind=Status, got %v", resp["kind"])
	}
	if resp["apiVersion"] != "v1" {
		t.Fatalf("expected apiVersion=v1, got %v", resp["apiVersion"])
	}
	if resp["status"] != "Failure" {
		t.Fatalf("expected status=Failure, got %v", resp["status"])
	}
}

func TestWrite_MessageContainsCode(t *testing.T) {
	w := write(base)
	resp := decode(t, w)
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "TEST-001") {
		t.Fatalf("expected message to contain code TEST-001, got %q", msg)
	}
	if !strings.Contains(msg, "something went wrong") {
		t.Fatalf("expected message to contain reason, got %q", msg)
	}
}

func TestWrite_ReasonMapsFromHTTPStatus(t *testing.T) {
	cases := []struct {
		httpStatus int
		wantReason string
	}{
		{http.StatusBadRequest, "BadRequest"},
		{http.StatusNotFound, "NotFound"},
		{http.StatusConflict, "Conflict"},
		{http.StatusUnprocessableEntity, "Invalid"},
		{http.StatusTooManyRequests, "TooManyRequests"},
	}
	for _, tc := range cases {
		w := write(api.APIError{Code: "X", HTTPStatus: tc.httpStatus, Message: "m"})
		resp := decode(t, w)
		if resp["reason"] != tc.wantReason {
			t.Errorf("status %d: reason=%v, want %q", tc.httpStatus, resp["reason"], tc.wantReason)
		}
	}
}

func TestWrite_CodeField(t *testing.T) {
	w := write(api.APIError{Code: "X", HTTPStatus: http.StatusNotFound, Message: "m"})
	resp := decode(t, w)
	if resp["code"] != float64(http.StatusNotFound) {
		t.Fatalf("expected code=%d, got %v", http.StatusNotFound, resp["code"])
	}
}

// --- Write: plain error (no exported fields) ---

func TestWrite_PlainError_MessageFromError(t *testing.T) {
	e := api.APIError{Code: "TEST-001", HTTPStatus: http.StatusNotFound, Message: "not found", Reason: "cluster %q not found"}
	w := write(e.WithReason("abc"))
	resp := decode(t, w)
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, `cluster "abc" not found`) {
		t.Fatalf("unexpected message: %v", msg)
	}
}

func TestWrite_PlainError_NoDetails(t *testing.T) {
	e := api.APIError{Code: "TEST-001", HTTPStatus: http.StatusBadRequest, Message: "bad", Reason: "%w"}
	w := write(e.WithReason(errors.New("oops")))
	resp := decode(t, w)
	if resp["details"] != nil {
		t.Fatal("details must be absent for plain errors")
	}
}

// --- Write: structured error (exported fields) ---

func TestWrite_StructuredError_StaticMessagePreserved(t *testing.T) {
	def := base.WithErrors(&structuredError{Field: "foo", Detail: "too long"})
	w := write(def)
	resp := decode(t, w)
	msg, _ := resp["message"].(string)
	if !strings.Contains(msg, "something went wrong") {
		t.Fatalf("expected static message, got %v", msg)
	}
}

func TestWrite_StructuredError_CausesInDetails(t *testing.T) {
	type fieldErr struct {
		Field  string `json:"field"`
		Detail string `json:"detail"`
	}
	errs := []fieldErr{{Field: "name", Detail: "required"}}
	def := base.WithErrors(errs)
	w := write(def)
	resp := decode(t, w)

	details, ok := resp["details"].(map[string]any)
	if !ok {
		t.Fatalf("expected details object, got %v", resp["details"])
	}
	causes, ok := details["causes"].([]any)
	if !ok || len(causes) == 0 {
		t.Fatalf("expected causes in details, got %v", details)
	}
	cause := causes[0].(map[string]any)
	if cause["field"] != "name" {
		t.Errorf("cause.field = %v, want name", cause["field"])
	}
	if cause["message"] != "required" {
		t.Errorf("cause.message = %v, want required", cause["message"])
	}
}

// --- Write: no errors ---

func TestWrite_NoErrors_NoDetails(t *testing.T) {
	w := write(base)
	resp := decode(t, w)
	if resp["details"] != nil {
		t.Fatal("details must be absent when not set")
	}
}

// --- Write: full response format ---

func TestWrite_ResponseFormat(t *testing.T) {
	cases := []struct {
		name            string
		def             api.APIError
		wantStatus      int
		wantReason      string
		wantMsgContains string
		wantDetails     bool
		forbidden       []string
	}{
		{
			name:            "static message no errors",
			def:             api.APIError{Code: "A-001", HTTPStatus: http.StatusBadRequest, Message: "bad request"},
			wantStatus:      http.StatusBadRequest,
			wantReason:      "BadRequest",
			wantMsgContains: "A-001",
			forbidden:       []string{"HTTPStatus", "http_status", "Reason", "Format"},
		},
		{
			name:            "plain error derives message and no details",
			def:             api.APIError{Code: "A-002", HTTPStatus: http.StatusNotFound, Message: "default", Reason: "item %q not found"}.WithReason("xyz"),
			wantStatus:      http.StatusNotFound,
			wantReason:      "NotFound",
			wantMsgContains: `item "xyz" not found`,
		},
		{
			name:            "structured error exposes causes in details",
			def:             api.APIError{Code: "A-003", HTTPStatus: http.StatusUnprocessableEntity, Message: "validation failed"}.WithErrors(&structuredError{Field: "name", Detail: "required"}),
			wantStatus:      http.StatusUnprocessableEntity,
			wantReason:      "Invalid",
			wantMsgContains: "validation failed",
			wantDetails:     true,
			forbidden:       []string{"HTTPStatus", "http_status", "Format"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := write(tc.def)

			if w.Code != tc.wantStatus {
				t.Errorf("status: got %d, want %d", w.Code, tc.wantStatus)
			}

			resp := decode(t, w)

			if resp["kind"] != "Status" {
				t.Errorf("kind: got %v, want Status", resp["kind"])
			}
			if resp["apiVersion"] != "v1" {
				t.Errorf("apiVersion: got %v, want v1", resp["apiVersion"])
			}
			if resp["status"] != "Failure" {
				t.Errorf("status field: got %v, want Failure", resp["status"])
			}
			if resp["reason"] != tc.wantReason {
				t.Errorf("reason: got %v, want %q", resp["reason"], tc.wantReason)
			}
			msg, _ := resp["message"].(string)
			if tc.wantMsgContains != "" && !strings.Contains(msg, tc.wantMsgContains) {
				t.Errorf("message %q missing %q", msg, tc.wantMsgContains)
			}
			if tc.wantDetails && resp["details"] == nil {
				t.Error("details: expected present, got absent")
			}
			if !tc.wantDetails && resp["details"] != nil {
				t.Errorf("details: expected absent, got %v", resp["details"])
			}

			for _, key := range tc.forbidden {
				if _, ok := resp[key]; ok {
					t.Errorf("internal field %q must not appear in response", key)
				}
			}
		})
	}
}
