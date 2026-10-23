package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"

	"github.com/gorilla/mux"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/clients/seaplanedb"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/middleware"
)

var accountIDPattern = regexp.MustCompile(`^\d{12}$`)

// AccountsHandler manages the temporary account allowlist for the platform's own account.
type AccountsHandler struct {
	db                *seaplanedb.Client
	platformAccountID string
	logger            *slog.Logger
}

// NewAccountsHandler creates an AccountsHandler. An empty platformAccountID denies every call.
func NewAccountsHandler(db *seaplanedb.Client, platformAccountID string, logger *slog.Logger) *AccountsHandler {
	return &AccountsHandler{db: db, platformAccountID: platformAccountID, logger: logger}
}

// Enroll handles POST /api/v0/accounts with {"accountId": "<12 digits>"}.
func (h *AccountsHandler) Enroll(w http.ResponseWriter, r *http.Request) {
	if !h.isPlatformCaller(w, r) {
		return
	}
	var req struct {
		AccountID string `json:"accountId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !accountIDPattern.MatchString(req.AccountID) {
		writeAPIError(w, ErrAccountsInvalidID, h.logger)
		return
	}
	if err := h.db.EnrollAccount(r.Context(), req.AccountID); err != nil {
		h.logger.Error("failed to enroll account", "error", err, "account_id", req.AccountID)
		writeAPIError(w, ErrAccountsFailed, h.logger)
		return
	}
	h.logger.Info("account enrolled", "account_id", req.AccountID, "caller_arn", middleware.GetCallerARN(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

// Unenroll handles DELETE /api/v0/accounts/{id}.
func (h *AccountsHandler) Unenroll(w http.ResponseWriter, r *http.Request) {
	if !h.isPlatformCaller(w, r) {
		return
	}
	accountID := mux.Vars(r)["id"]
	if !accountIDPattern.MatchString(accountID) {
		writeAPIError(w, ErrAccountsInvalidID, h.logger)
		return
	}
	if err := h.db.UnenrollAccount(r.Context(), accountID); err != nil {
		h.logger.Error("failed to unenroll account", "error", err, "account_id", accountID)
		writeAPIError(w, ErrAccountsFailed, h.logger)
		return
	}
	h.logger.Info("account unenrolled", "account_id", accountID, "caller_arn", middleware.GetCallerARN(r.Context()))
	w.WriteHeader(http.StatusNoContent)
}

func (h *AccountsHandler) isPlatformCaller(w http.ResponseWriter, r *http.Request) bool {
	if h.platformAccountID == "" || middleware.GetAccountID(r.Context()) != h.platformAccountID {
		h.logger.Warn("account allowlist change denied", "account_id", middleware.GetAccountID(r.Context()), "caller_arn", middleware.GetCallerARN(r.Context()))
		writeAPIError(w, ErrAccountsForbidden, h.logger)
		return false
	}
	return true
}
