package middleware

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz"
)

// AdminCheck provides middleware for checking admin status
type AdminCheck struct {
	authorizer authz.Checker
	logger     *slog.Logger
}

// NewAdminCheck creates a new AdminCheck middleware
func NewAdminCheck(authorizer authz.Checker, logger *slog.Logger) *AdminCheck {
	return &AdminCheck{
		authorizer: authorizer,
		logger:     logger,
	}
}

// RequireAdmin returns 403 if the caller is not an admin for the account.
// Privileged accounts bypass this check.
func (a *AdminCheck) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		accountID := GetAccountID(ctx)

		if accountID == "" {
			a.writeError(w, http.StatusForbidden, "missing-account-id", "Account ID header is required")
			return
		}

		// Privileged accounts bypass admin check
		if GetPrivileged(ctx) {
			next.ServeHTTP(w, r)
			return
		}

		callerARN := GetCallerARN(ctx)
		if callerARN == "" {
			a.writeError(w, http.StatusForbidden, "missing-caller-arn", "Caller ARN header is required")
			return
		}

		isAdmin, err := a.authorizer.IsAdmin(ctx, accountID, callerARN)
		if err != nil {
			a.logger.Error("failed to check admin status", "error", err, "account_id", accountID, "caller_arn", callerARN)
			a.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to check admin status")
			return
		}

		if !isAdmin {
			a.logger.Warn("admin access denied", "account_id", accountID, "caller_arn", callerARN)
			a.writeError(w, http.StatusForbidden, "not-admin", "This operation requires admin privileges")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (a *AdminCheck) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}

	_ = json.NewEncoder(w).Encode(resp)
}
