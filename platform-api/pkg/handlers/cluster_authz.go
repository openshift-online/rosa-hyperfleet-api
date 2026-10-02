package handlers

import (
	"errors"
	"net/http"

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

func (h *ClusterHandler) prepareClusterAuthz(r *http.Request) (*authz.Prepared, error) {
	return h.authorizer.Prepare(r.Context(), authz.Identity{
		AccountID: middleware.GetAccountID(r.Context()),
		CallerARN: middleware.GetCallerARN(r.Context()),
		Region:    h.region,
	})
}

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
		Region:    h.region,
		Labels:    cr.Labels,
	}
}

func (h *ClusterHandler) writeClusterAuthzError(w http.ResponseWriter, r *http.Request, attempt *authz.Attempt, operation authz.Action, err error) {
	stage := authz.StageEvaluation
	attrs := []any{
		"operation", operation,
		"account_id", middleware.GetAccountID(r.Context()),
		"caller_arn", middleware.GetCallerARN(r.Context()),
		"region", h.region,
		"error", err,
	}
	// Failure.Error deliberately hides the diagnostics. Log their internal fields separately.
	var failure *authz.Failure
	if errors.As(err, &failure) {
		stage = failure.Stage
		attrs = append(attrs, "cause", failure.Err, "provenance", failure.Provenance, "diagnostics", failure.Diagnostics)
	}
	attrs = append(attrs, "stage", stage)
	h.logger.Error("cluster authorization failed", attrs...)
	_ = attempt.Finish(authz.OutcomeError, stage)
	writeAPIError(w, errClusterAuthzFailed, h.logger)
}
