package middleware

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gorilla/mux"

	"github.com/openshift/rosa-hyperfleet-api/pkg/authz"
)

// Authz provides Cedar/AVP-based authorization middleware
type Authz struct {
	authorizer authz.Checker
	logger     *slog.Logger
	enabled    bool
	region     string
}

// NewAuthz creates a new Authz middleware
func NewAuthz(authorizer authz.Checker, enabled bool, region string, logger *slog.Logger) *Authz {
	if region == "" {
		region = "us-east-1"
	}
	return &Authz{
		authorizer: authorizer,
		logger:     logger,
		enabled:    enabled,
		region:     region,
	}
}

// Authorize performs AVP-based authorization
// This middleware should run after Identity and Privileged middleware
func (a *Authz) Authorize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.enabled {
			next.ServeHTTP(w, r)
			return
		}

		ctx := r.Context()
		accountID := GetAccountID(ctx)
		callerARN := GetCallerARN(ctx)

		if accountID == "" {
			a.writeError(w, http.StatusForbidden, "missing-account-id", "Account ID header is required")
			return
		}

		if callerARN == "" {
			a.writeError(w, http.StatusForbidden, "missing-caller-arn", "Caller ARN header is required")
			return
		}

		// Privileged accounts bypass authorization
		if GetPrivileged(ctx) {
			next.ServeHTTP(w, r)
			return
		}

		// Build authorization request
		req := a.buildAuthzRequest(r, accountID, callerARN)

		// Perform authorization check
		allowed, err := a.authorizer.Authorize(ctx, req)
		if err != nil {
			a.logger.Error("authorization check failed", "error", err, "account_id", accountID, "action", req.Action)
			// Check if it's a "not provisioned" error
			if strings.Contains(err.Error(), "not provisioned") {
				a.writeError(w, http.StatusForbidden, "account-not-provisioned",
					"Account is not provisioned for ROSA authorization")
				return
			}
			a.writeError(w, http.StatusInternalServerError, "authorization-error", "Authorization check failed")
			return
		}

		if !allowed {
			a.logger.Info("authorization denied",
				"account_id", accountID,
				"caller_arn", callerARN,
				"action", req.Action,
				"resource", req.Resource,
			)
			a.writeError(w, http.StatusForbidden, "access-denied",
				"You do not have permission to perform this action")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// buildAuthzRequest creates an authorization request from the HTTP request
func (a *Authz) buildAuthzRequest(r *http.Request, accountID, callerARN string) *authz.AuthzRequest {
	action := a.deriveAction(r)
	resource := a.deriveResource(r)

	return &authz.AuthzRequest{
		AccountID:    accountID,
		CallerARN:    callerARN,
		Action:       action,
		Resource:     resource,
		ResourceTags: make(map[string]string), // Populated from the actual resource when available
		RequestTags:  make(map[string]string), // Populated from the request body when available
		Context:      make(map[string]any),
	}
}

// deriveAction derives the ROSA action from the HTTP request
func (a *Authz) deriveAction(r *http.Request) string {
	// Map HTTP method + path to ROSA action
	path := r.URL.Path
	method := r.Method

	// Extract resource type from path
	// e.g., /api/v0/clusters -> Cluster
	//       /api/v0/clusters/{id}/nodepools -> NodePool
	parts := strings.Split(strings.Trim(path, "/"), "/")

	// Default action based on method
	var actionPrefix string
	switch method {
	case http.MethodPost:
		actionPrefix = "Create"
	case http.MethodGet:
		actionPrefix = "Describe"
	case http.MethodPut, http.MethodPatch:
		actionPrefix = "Update"
	case http.MethodDelete:
		actionPrefix = "Delete"
	default:
		actionPrefix = "Unknown"
	}

	// Determine resource type
	resourceType := "Resource"
	if len(parts) >= 3 {
		switch parts[2] {
		case "clusters":
			resourceType = "Cluster"
			// Check for sub-resources
			if len(parts) >= 5 {
				switch parts[4] {
				case "nodepools":
					resourceType = "NodePool"
				case "access_entries":
					resourceType = "AccessEntry"
				}
			}
		case "nodepools":
			resourceType = "NodePool"
		case "access_entries":
			resourceType = "AccessEntry"
		}
	}

	// Special case: List operations
	if method == http.MethodGet {
		vars := mux.Vars(r)
		if _, hasID := vars["id"]; !hasID {
			actionPrefix = "List"
			// Pluralize for list operations
			resourceType = resourceType + "s"
		}
	}

	return actionPrefix + resourceType
}

// deriveResource derives the ROSA resource ARN from the HTTP request
func (a *Authz) deriveResource(r *http.Request) string {
	vars := mux.Vars(r)

	// Check for resource ID in path
	if id, ok := vars["id"]; ok {
		accountID := GetAccountID(r.Context())
		// Build ARN based on resource type
		path := r.URL.Path
		if strings.Contains(path, "/nodepools/") {
			return a.buildARN(accountID, "nodepool", id)
		}
		if strings.Contains(path, "/access_entries/") {
			return a.buildARN(accountID, "accessentry", id)
		}
		if strings.Contains(path, "/clusters/") || strings.Contains(path, "/clusters") {
			return a.buildARN(accountID, "cluster", id)
		}
		return a.buildARN(accountID, "resource", id)
	}

	// No specific resource - use wildcard
	return "*"
}

// buildARN creates a ROSA ARN using the configured region
func (a *Authz) buildARN(accountID, resourceType, resourceID string) string {
	return "arn:aws:rosa:" + a.region + ":" + accountID + ":" + resourceType + "/" + resourceID
}

// WithResourceContext is a helper to add resource context to the authorization request
// Use this when you have loaded the actual resource and want to add its tags
func WithResourceContext(ctx context.Context, tags map[string]string) context.Context {
	return context.WithValue(ctx, contextKeyResourceTags, tags)
}

// WithRequestContext is a helper to add request context (e.g., tags from create request)
func WithRequestContext(ctx context.Context, tags map[string]string) context.Context {
	return context.WithValue(ctx, contextKeyRequestTags, tags)
}

const (
	contextKeyResourceTags contextKey = "resource_tags"
	contextKeyRequestTags  contextKey = "request_tags"
)

func (a *Authz) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}

	_ = json.NewEncoder(w).Encode(resp)
}
