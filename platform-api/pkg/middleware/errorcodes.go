package middleware

import (
	"log/slog"
	"net/http"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
)

// APIError is an alias for api.APIError so middleware code uses the short form.
type APIError = api.APIError

func writeError(w http.ResponseWriter, def APIError, logger *slog.Logger) {
	if err := api.WriteError(w, def); err != nil {
		logger.Error("failed to write error response", "error", err)
	}
}

// Auth middleware error codes
var (
	ErrMissingAccountID      APIError
	ErrInvalidAccountID      APIError
	ErrMissingCallerARN      APIError
	ErrInternalError         APIError
	ErrAccountNotProvisioned APIError
	ErrNotAdmin              APIError
	ErrNotPrivileged         APIError
	ErrAccountNotAllowed     APIError
	ErrAuthorizationFailed   APIError
	ErrAccessDenied          APIError

	ErrAdminCheckFailed       APIError
	ErrPrivilegedCheckFailed  APIError
	ErrProvisionedCheckFailed APIError
)

func init() {
	ErrMissingAccountID = APIError{Code: "AUTH-001", HTTPStatus: http.StatusForbidden, Message: "Account ID header is required"}
	ErrInvalidAccountID = APIError{Code: "AUTH-013", HTTPStatus: http.StatusBadRequest, Message: "Account ID must be a 12-digit AWS account number"}
	ErrMissingCallerARN = APIError{Code: "AUTH-002", HTTPStatus: http.StatusForbidden, Message: "Caller ARN header is required"}
	ErrInternalError = APIError{Code: "AUTH-003", HTTPStatus: http.StatusInternalServerError, Message: "Internal server error"}
	ErrAccountNotProvisioned = APIError{Code: "AUTH-004", HTTPStatus: http.StatusForbidden, Message: "Account is not provisioned for ROSA authorization. Contact your administrator."}
	ErrNotAdmin = APIError{Code: "AUTH-005", HTTPStatus: http.StatusForbidden, Message: "This operation requires admin privileges"}
	ErrNotPrivileged = APIError{Code: "AUTH-006", HTTPStatus: http.StatusForbidden, Message: "This operation requires a privileged account"}
	ErrAccountNotAllowed = APIError{Code: "AUTH-007", HTTPStatus: http.StatusForbidden, Message: "account not allowed"}
	ErrAuthorizationFailed = APIError{Code: "AUTH-008", HTTPStatus: http.StatusInternalServerError, Message: "Authorization check failed"}
	ErrAccessDenied = APIError{Code: "AUTH-009", HTTPStatus: http.StatusForbidden, Message: "You do not have permission to perform this action"}

	ErrAdminCheckFailed = APIError{Code: "AUTH-010", HTTPStatus: http.StatusInternalServerError, Message: "Failed to check admin status"}
	ErrPrivilegedCheckFailed = APIError{Code: "AUTH-011", HTTPStatus: http.StatusInternalServerError, Message: "Failed to check privileged status"}
	ErrProvisionedCheckFailed = APIError{Code: "AUTH-012", HTTPStatus: http.StatusInternalServerError, Message: "Failed to check account provisioning status"}
}
