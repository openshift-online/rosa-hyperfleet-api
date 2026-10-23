package handlers

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/clients/seaplanedb"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const platformAccount = "111111111111"

func accountsFixture(t *testing.T, platform string) (*AccountsHandler, *seaplanedb.Client) {
	t.Helper()
	db := seaplanedb.NewClientFrom(fake.NewClientBuilder().WithScheme(newTestScheme()).Build(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return NewAccountsHandler(db, platform, slog.New(slog.NewTextHandler(io.Discard, nil))), db
}

func accountsRequest(h *AccountsHandler, method, caller, id, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/v0/accounts", strings.NewReader(body)).WithContext(testContext(caller))
	w := httptest.NewRecorder()
	if method == http.MethodDelete {
		h.Unenroll(w, mux.SetURLVars(r, map[string]string{"id": id}))
	} else {
		h.Enroll(w, r)
	}
	return w
}

func TestAccountsOnlyPlatformAccount(t *testing.T) {
	for _, tc := range []struct{ name, platform, caller string }{
		{"customer caller", platformAccount, "222222222222"},
		{"platform account unset", "", platformAccount},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, db := accountsFixture(t, tc.platform)
			for _, method := range []string{http.MethodPost, http.MethodDelete} {
				if w := accountsRequest(h, method, tc.caller, "333333333333", `{"accountId":"333333333333"}`); w.Code != http.StatusForbidden {
					t.Fatalf("%s: want 403, got %d %s", method, w.Code, w.Body.String())
				}
			}
			if enrolled, err := db.IsAccountEnrolled(t.Context(), "333333333333"); err != nil || enrolled {
				t.Fatalf("forbidden call enrolled the account: %v %v", enrolled, err)
			}
		})
	}
}

func TestAccountsRejectInvalidID(t *testing.T) {
	h, _ := accountsFixture(t, platformAccount)
	for _, body := range []string{`{"accountId":"123"}`, `{"accountId":""}`, `not json`} {
		if w := accountsRequest(h, http.MethodPost, platformAccount, "", body); w.Code != http.StatusBadRequest {
			t.Fatalf("POST %s: want 400, got %d", body, w.Code)
		}
	}
	if w := accountsRequest(h, http.MethodDelete, platformAccount, "../x", ""); w.Code != http.StatusBadRequest {
		t.Fatalf("DELETE invalid id: want 400, got %d", w.Code)
	}
}

func TestAccountsEnrollAndRevokeIdempotent(t *testing.T) {
	h, db := accountsFixture(t, platformAccount)
	for range 2 {
		if w := accountsRequest(h, http.MethodPost, platformAccount, "", `{"accountId":"333333333333"}`); w.Code != http.StatusNoContent {
			t.Fatalf("enroll: want 204, got %d %s", w.Code, w.Body.String())
		}
	}
	if enrolled, err := db.IsAccountEnrolled(t.Context(), "333333333333"); err != nil || !enrolled {
		t.Fatalf("account not enrolled: %v %v", enrolled, err)
	}
	for range 2 {
		if w := accountsRequest(h, http.MethodDelete, platformAccount, "333333333333", ""); w.Code != http.StatusNoContent {
			t.Fatalf("revoke: want 204, got %d %s", w.Code, w.Body.String())
		}
	}
	if enrolled, err := db.IsAccountEnrolled(t.Context(), "333333333333"); err != nil || enrolled {
		t.Fatalf("account still enrolled: %v %v", enrolled, err)
	}
}
