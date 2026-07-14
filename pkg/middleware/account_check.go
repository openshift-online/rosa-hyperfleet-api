package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz"
)

// AccountCheck provides middleware for checking account provisioning status
type AccountCheck struct {
	authorizer authz.Checker
	logger     *slog.Logger
}

// NewAccountCheck creates a new AccountCheck middleware
func NewAccountCheck(authorizer authz.Checker, logger *slog.Logger) *AccountCheck {
	return &AccountCheck{
		authorizer: authorizer,
		logger:     logger,
	}
}

// RequireProvisioned returns 403 if the account is not provisioned
// Privileged accounts are always considered provisioned
func (a *AccountCheck) RequireProvisioned(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		accountID := GetAccountID(ctx)

		if accountID == "" {
			a.writeError(w, http.StatusForbidden, "missing-account-id", "Account ID header is required")
			return
		}

		// Check if privileged first (from context if available)
		if GetPrivileged(ctx) {
			next.ServeHTTP(w, r)
			return
		}

		// Check if account is provisioned
		provisioned, err := a.authorizer.IsAccountProvisioned(ctx, accountID)
		if err != nil {
			a.logger.Error("failed to check account provisioning status", "error", err, "account_id", accountID)
			a.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to check account status")
			return
		}

		if !provisioned {
			a.logger.Warn("account not provisioned", "account_id", accountID)
			a.writeError(w, http.StatusForbidden, "account-not-provisioned",
				"Account is not provisioned for ROSA authorization. Contact your administrator.")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *AccountCheck) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}

	_ = json.NewEncoder(w).Encode(resp)
}
