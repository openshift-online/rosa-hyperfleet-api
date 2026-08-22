package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
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
	AdminArn   string `json:"adminArn,omitempty"`
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
		writeAPIError(w, ErrAccountCreateInvalidBody, h.logger)
		return
	}

	if req.AccountID == "" {
		writeAPIError(w, ErrAccountCreateMissingID, h.logger)
		return
	}

	if !req.Privileged && req.AdminArn == "" {
		writeAPIError(w, ErrAccountCreateMissingAdminArn, h.logger)
		return
	}

	// Check if account already exists
	existing, err := h.authorizer.GetAccount(ctx, req.AccountID)
	if err != nil {
		h.logger.Error("failed to check existing account", "error", err, "account_id", req.AccountID)
		writeAPIError(w, ErrAccountCreateCheckFailed, h.logger)
		return
	}
	if existing != nil {
		writeAPIError(w, ErrAccountCreateExists, h.logger)
		return
	}

	account, err := h.authorizer.EnableAccount(ctx, req.AccountID, callerARN, req.Privileged)
	if err != nil {
		h.logger.Error("failed to enable account", "error", err, "account_id", req.AccountID)
		writeAPIError(w, ErrAccountCreateFailed, h.logger)
		return
	}

	h.logger.Info("account enabled", "account_id", redact(req.AccountID), "privileged", req.Privileged)

	if req.AdminArn != "" && !req.Privileged {
		if err := h.authorizer.AddAdmin(ctx, req.AccountID, req.AdminArn, callerARN); err != nil {
			h.logger.Error("failed to add initial admin", "error", err, "account_id", req.AccountID, "admin_arn", req.AdminArn)
		} else {
			h.logger.Info("initial admin added", "account_id", req.AccountID, "admin_arn", req.AdminArn)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	err = json.NewEncoder(w).Encode(AccountResponse{
		Kind:          "Account",
		AccountID:     account.AccountID,
		PolicyStoreID: account.PolicyStoreID,
		Privileged:    account.Privileged,
		CreatedAt:     account.CreatedAt,
		CreatedBy:     account.CreatedBy,
	})
	if err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// List handles GET /api/v0/accounts
func (h *AccountsHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	accounts, err := h.authorizer.ListAccounts(ctx)
	if err != nil {
		h.logger.Error("failed to list accounts", "error", err)
		writeAPIError(w, ErrAccountListFailed, h.logger)
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

	if err := api.Write(w, http.StatusOK, AccountListResponse{
		Kind:  "AccountList",
		Items: items,
		Total: len(items),
	}); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Get handles GET /api/v0/accounts/{id}
func (h *AccountsHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	vars := mux.Vars(r)
	accountID := vars["id"]

	account, err := h.authorizer.GetAccount(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to get account", "error", err, "account_id", accountID)
		writeAPIError(w, ErrAccountGetFailed, h.logger)
		return
	}

	if account == nil {
		writeAPIError(w, ErrAccountGetNotFound, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, AccountResponse{
		Kind:          "Account",
		AccountID:     account.AccountID,
		PolicyStoreID: account.PolicyStoreID,
		Privileged:    account.Privileged,
		CreatedAt:     account.CreatedAt,
		CreatedBy:     account.CreatedBy,
	}); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
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
		writeAPIError(w, ErrAccountDeleteFailed, h.logger)
		return
	}

	h.logger.Info("account disabled", "account_id", accountID)

	if err := api.Write(w, http.StatusNoContent, nil); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}
