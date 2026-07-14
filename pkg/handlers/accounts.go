package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
)

// AccountsHandler handles account management endpoints
type AccountsHandler struct {
	authorizer authz.Service
	logger     *slog.Logger
}

// NewAccountsHandler creates a new AccountsHandler
func NewAccountsHandler(authorizer authz.Service, logger *slog.Logger) *AccountsHandler {
	return &AccountsHandler{
		authorizer: authorizer,
		logger:     logger,
	}
}

// EnableAccountRequest is the request body for enabling an account
type EnableAccountRequest struct {
	AccountID  string `json:"accountId"`
	Privileged bool   `json:"privileged"`
}

// AccountResponse is the response for account operations
type AccountResponse struct {
	Kind          string `json:"kind"`
	AccountID     string `json:"accountId"`
	PolicyStoreID string `json:"policyStoreId,omitempty"`
	Privileged    bool   `json:"privileged"`
	CreatedAt     string `json:"createdAt"`
	CreatedBy     string `json:"createdBy"`
}

// AccountListResponse is the response for listing accounts
type AccountListResponse struct {
	Kind  string            `json:"kind"`
	Items []AccountResponse `json:"items"`
	Total int               `json:"total"`
}

// Create handles POST /api/v0/accounts (enable an account)
func (h *AccountsHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	callerARN := middleware.GetCallerARN(ctx)

	h.logger.Info("enabling account", "caller_arn", callerARN)

	var req EnableAccountRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	if req.AccountID == "" {
		h.writeError(w, http.StatusBadRequest, "missing-account-id", "accountId is required")
		return
	}

	// Check if account already exists
	existing, err := h.authorizer.GetAccount(ctx, req.AccountID)
	if err != nil {
		h.logger.Error("failed to check existing account", "error", err, "account_id", req.AccountID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to check account status")
		return
	}
	if existing != nil {
		h.writeError(w, http.StatusConflict, "account-exists", "Account is already enabled")
		return
	}

	account, err := h.authorizer.EnableAccount(ctx, req.AccountID, callerARN, req.Privileged)
	if err != nil {
		h.logger.Error("failed to enable account", "error", err, "account_id", req.AccountID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to enable account")
		return
	}

	h.logger.Info("account enabled", "account_id", req.AccountID, "privileged", req.Privileged)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(AccountResponse{
		Kind:          "Account",
		AccountID:     account.AccountID,
		PolicyStoreID: account.PolicyStoreID,
		Privileged:    account.Privileged,
		CreatedAt:     account.CreatedAt,
		CreatedBy:     account.CreatedBy,
	})
}

// List handles GET /api/v0/accounts
func (h *AccountsHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	accounts, err := h.authorizer.ListAccounts(ctx)
	if err != nil {
		h.logger.Error("failed to list accounts", "error", err)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to list accounts")
		return
	}

	items := make([]AccountResponse, len(accounts))
	for i, acc := range accounts {
		items[i] = AccountResponse{
			Kind:          "Account",
			AccountID:     acc.AccountID,
			PolicyStoreID: acc.PolicyStoreID,
			Privileged:    acc.Privileged,
			CreatedAt:     acc.CreatedAt,
			CreatedBy:     acc.CreatedBy,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AccountListResponse{
		Kind:  "AccountList",
		Items: items,
		Total: len(items),
	})
}

// Get handles GET /api/v0/accounts/{id}
func (h *AccountsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	accountID := vars["id"]

	account, err := h.authorizer.GetAccount(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to get account", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to get account")
		return
	}

	if account == nil {
		h.writeError(w, http.StatusNotFound, "not-found", "Account not found")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(AccountResponse{
		Kind:          "Account",
		AccountID:     account.AccountID,
		PolicyStoreID: account.PolicyStoreID,
		Privileged:    account.Privileged,
		CreatedAt:     account.CreatedAt,
		CreatedBy:     account.CreatedBy,
	})
}

// Delete handles DELETE /api/v0/accounts/{id}
func (h *AccountsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	accountID := vars["id"]
	callerARN := middleware.GetCallerARN(ctx)

	h.logger.Info("disabling account", "account_id", accountID, "caller_arn", callerARN)

	err := h.authorizer.DisableAccount(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to disable account", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to disable account")
		return
	}

	h.logger.Info("account disabled", "account_id", accountID)

	w.WriteHeader(http.StatusNoContent)
}

func (h *AccountsHandler) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}

	_ = json.NewEncoder(w).Encode(resp)
}
