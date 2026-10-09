package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

// ManagementClusterHandler handles management cluster endpoints.
type ManagementClusterHandler struct {
	db         *hyperfleetdb.Client
	logger     *slog.Logger
	authorizer *authz.Authorizer
}

// NewManagementClusterHandler creates a new ManagementClusterHandler.
func NewManagementClusterHandler(db *hyperfleetdb.Client, authorizer *authz.Authorizer, logger *slog.Logger) *ManagementClusterHandler {
	if authorizer == nil {
		panic("management cluster authorizer is required")
	}
	return &ManagementClusterHandler{
		authorizer: authorizer,
		db:         db,
		logger:     logger,
	}
}

// ManagementClusterCreateRequest is the request body for creating an MC registration.
type ManagementClusterCreateRequest struct {
	ID        string `json:"id"`
	Region    string `json:"region"`
	AccountID string `json:"accountId"`
}

// ManagementClusterResponse is the JSON response for a management cluster.
type ManagementClusterResponse struct {
	ID        string `json:"id"`
	Region    string `json:"region"`
	AccountID string `json:"accountId"`
}

// Create handles POST /api/v0/management_clusters
func (h *ManagementClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	h.logger.Info("creating management cluster", "account_id", accountID)

	var req ManagementClusterCreateRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeAPIError(w, ErrMCCreateInvalidBody, h.logger)
			return
		}
	}

	if req.ID == "" {
		writeAPIError(w, ErrMCCreateMissingID, h.logger)
		return
	}
	if req.Region == "" {
		writeAPIError(w, ErrMCCreateMissingReg, h.logger)
		return
	}
	if req.AccountID == "" {
		writeAPIError(w, ErrMCCreateMissingAcct, h.logger)
		return
	}

	mc := &hyperfleetv1alpha1.ManagementCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: req.ID,
		},
		Spec: hyperfleetv1alpha1.ManagementClusterSpec{
			Region:    req.Region,
			AccountID: req.AccountID,
		},
	}

	if !authorizeResource(w, r, h.authorizer, h.logger, authz.CreateManagementCluster, h.managementClusterResource(mc)) {
		return
	}
	if err := h.db.CreateManagementCluster(ctx, mc); err != nil {
		if hyperfleetdb.IsAlreadyExists(err) {
			writeAPIError(w, ErrMCCreateExists.WithReason(req.ID), h.logger)
			return
		}
		h.logger.Error("failed to create management cluster", "error", err)
		writeAPIError(w, ErrMCCreateFailed, h.logger)
		return
	}

	h.logger.Info("management cluster created", "id", redact(mc.Name), "account_id", redact(accountID))

	if err := api.Write(w, http.StatusCreated, mcToResponse(mc)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// List handles GET /api/v0/management_clusters
func (h *ManagementClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	h.logger.Debug("listing management clusters", "account_id", accountID)
	prepared, attempt := authorizeCollection(w, r, h.authorizer, authz.DefaultMetrics, h.logger, authz.ListManagementClusters, authz.Resource{Kind: authz.ServiceCollection})
	if prepared == nil {
		return
	}

	list, err := h.db.ListManagementClusters(ctx)
	if err != nil {
		h.logger.Error("failed to list management clusters", "error", err)
		_ = attempt.Finish(authz.OutcomeError, authz.StageResourceLoading)
		writeAPIError(w, ErrMCListFailed, h.logger)
		return
	}

	clusters := make([]ManagementClusterResponse, 0, len(list.Items))
	for i := range list.Items {
		decision, err := prepared.Check(ctx, authz.DescribeManagementCluster, h.managementClusterResource(&list.Items[i]))
		if err != nil {
			writeResourceAuthzError(w, r, attempt, authz.ListManagementClusters, err, h.logger)
			return
		}
		if decision.Allowed {
			clusters = append(clusters, mcToResponse(&list.Items[i]))
		}
	}
	_ = attempt.Finish(authz.OutcomeAllow, authz.StageNone)

	h.logger.Debug("management clusters listed", "total", len(clusters), "account_id", accountID)

	if err := api.Write(w, http.StatusOK, map[string]any{
		"kind":  "ManagementClusterList",
		"items": clusters,
		"total": len(clusters),
	}); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Get handles GET /api/v0/management_clusters/{id}
func (h *ManagementClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	id := vars["id"]

	h.logger.Debug("getting management cluster", "id", id, "account_id", accountID)

	mc, err := h.db.GetManagementCluster(ctx, id)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrMCGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get management cluster", "error", err, "id", id)
		writeAPIError(w, ErrMCGetFailed, h.logger)
		return
	}

	if !authorizeResource(w, r, h.authorizer, h.logger, authz.DescribeManagementCluster, h.managementClusterResource(mc)) {
		return
	}
	h.logger.Debug("management cluster retrieved", "id", mc.Name, "account_id", accountID)

	if err := api.Write(w, http.StatusOK, mcToResponse(mc)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

func mcToResponse(mc *hyperfleetv1alpha1.ManagementCluster) ManagementClusterResponse {
	return ManagementClusterResponse{
		ID:        mc.Name,
		Region:    mc.Spec.Region,
		AccountID: mc.Spec.AccountID,
	}
}
