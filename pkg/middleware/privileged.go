package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz"
)

// contextKey for privileged status
const ContextKeyPrivileged contextKey = "privileged"

// Privileged provides middleware for checking privileged account status
type Privileged struct {
	authorizer authz.Checker
	logger     *slog.Logger
}

// NewPrivileged creates a new Privileged middleware
func NewPrivileged(authorizer authz.Checker, logger *slog.Logger) *Privileged {
	return &Privileged{
		authorizer: authorizer,
		logger:     logger,
	}
}

// CheckPrivileged adds privileged status to the context
// This middleware should run after Identity middleware
func (p *Privileged) CheckPrivileged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		accountID := GetAccountID(ctx)

		if accountID == "" {
			next.ServeHTTP(w, r)
			return
		}

		isPrivileged, err := p.authorizer.IsPrivileged(ctx, accountID)
		if err != nil {
			p.logger.Error("failed to check privileged status", "error", err, "account_id", accountID)
			// Continue without privileged status on error
			next.ServeHTTP(w, r)
			return
		}

		ctx = context.WithValue(ctx, ContextKeyPrivileged, isPrivileged)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePrivileged returns 403 if the account is not privileged
func (p *Privileged) RequirePrivileged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		accountID := GetAccountID(ctx)

		if accountID == "" {
			p.writeError(w, http.StatusForbidden, "missing-account-id", "Account ID header is required")
			return
		}

		isPrivileged := GetPrivileged(ctx)
		if !isPrivileged {
			// Double-check in case CheckPrivileged wasn't run
			var err error
			isPrivileged, err = p.authorizer.IsPrivileged(ctx, accountID)
			if err != nil {
				p.logger.Error("failed to check privileged status", "error", err, "account_id", accountID)
				p.writeError(w, http.StatusInternalServerError, "internal-error", "Failed to check account status")
				return
			}
		}

		if !isPrivileged {
			p.logger.Warn("privileged access denied", "account_id", accountID)
			p.writeError(w, http.StatusForbidden, "not-privileged", "This operation requires a privileged account")
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (p *Privileged) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}

	_ = json.NewEncoder(w).Encode(resp)
}

// GetPrivileged retrieves the privileged status from context
func GetPrivileged(ctx context.Context) bool {
	if v := ctx.Value(ContextKeyPrivileged); v != nil {
		return v.(bool)
	}
	return false
}
