package handlers

import (
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"time"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

var (
	errClusterAuthzDenied = api.APIError{Code: "AUTHZ-DENIED-001", HTTPStatus: http.StatusForbidden, Message: "Access denied"}
	errClusterAuthzFailed = api.APIError{Code: "AUTHZ-FAILED-001", HTTPStatus: http.StatusInternalServerError, Message: "Authorization failed"}
)

func (h *ClusterHandler) clusterResource(cr *hyperfleetv1alpha1.Cluster) authz.Resource {
	id := hyperfleetdb.ClusterIDFromNamespace(cr.Namespace)
	// A missing namespace prefix is not a stable Cluster identity.
	if id == cr.Namespace {
		id = ""
	}
	return authz.Resource{
		Kind:      authz.Cluster,
		ID:        id,
		AccountID: cr.Labels["hyperfleet.io/account-id"],
		Labels:    maps.Clone(cr.Labels),
	}
}

func writeResourceAuthzError(w http.ResponseWriter, r *http.Request, attempt *authz.Attempt, operation authz.Action, err error, logger *slog.Logger) {
	stage := authz.StageEvaluation
	attrs := []any{
		"operation", operation,
		"account_id", middleware.GetAccountID(r.Context()),
		"caller_arn", middleware.GetCallerARN(r.Context()),
		"error", err,
	}
	// Failure.Error deliberately hides the diagnostics. Log their internal fields separately.
	var failure *authz.Failure
	if errors.As(err, &failure) {
		stage = failure.Stage
		attrs = append(attrs, "cause", failure.Err, "provenance", failure.Provenance, "diagnostics", failure.Diagnostics)
	}
	attrs = append(attrs, "stage", stage)
	logger.Error("cluster authorization failed", attrs...)
	_ = attempt.Finish(authz.OutcomeError, stage)
	writeAPIError(w, errClusterAuthzFailed, logger)
}

func prepareResourceAuthz(r *http.Request, authorizer *authz.Authorizer) (*authz.Prepared, error) {
	return authorizer.Prepare(r.Context(), authz.Identity{
		AccountID: middleware.GetAccountID(r.Context()), CallerARN: middleware.GetCallerARN(r.Context()),
	}, authz.RequestContext{SourceIP: middleware.GetSourceIP(r.Context()), UserAgent: r.UserAgent(), RequestTime: time.Now()})
}

// authorizeCollection prepares one frozen policy/context snapshot for all item checks.
func authorizeCollection(w http.ResponseWriter, r *http.Request, authorizer *authz.Authorizer, metrics *authz.Metrics, logger *slog.Logger, operation authz.Action, resource authz.Resource) (*authz.Prepared, *authz.Attempt) {
	attempt, _ := metrics.Start(operation)
	prepared, err := prepareResourceAuthz(r, authorizer)
	if err != nil {
		writeResourceAuthzError(w, r, attempt, operation, err, logger)
		return nil, nil
	}
	decision, err := prepared.Check(r.Context(), operation, resource)
	if err != nil {
		writeResourceAuthzError(w, r, attempt, operation, err, logger)
		return nil, nil
	}
	if !decision.Allowed {
		_ = attempt.Finish(authz.OutcomeDeny, authz.StageNone)
		writeAPIError(w, errClusterAuthzDenied, logger)
		return nil, nil
	}
	return prepared, attempt
}

// authorizeResource checks the base operation and every effective changed-field action before a write.
func authorizeResource(w http.ResponseWriter, r *http.Request, authorizer *authz.Authorizer, logger *slog.Logger, operation authz.Action, resource authz.Resource, additional ...string) bool {
	attempt, _ := authz.DefaultMetrics.Start(operation)
	prepared, err := prepareResourceAuthz(r, authorizer)
	if err != nil {
		writeResourceAuthzError(w, r, attempt, operation, err, logger)
		return false
	}
	actions := []string{string(operation)}
	actions = append(actions, additional...)
	slices.Sort(actions)
	actions = slices.Compact(actions)
	for _, action := range actions {
		decision, err := prepared.Check(r.Context(), authz.Action(action), resource)
		if err != nil {
			writeResourceAuthzError(w, r, attempt, operation, err, logger)
			return false
		}
		if !decision.Allowed {
			_ = attempt.Finish(authz.OutcomeDeny, authz.StageNone)
			writeAPIError(w, errClusterAuthzDenied, logger)
			return false
		}
	}
	_ = attempt.Finish(authz.OutcomeAllow, authz.StageNone)
	return true
}
