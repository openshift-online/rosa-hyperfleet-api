package handlers

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/hyperfleet"
	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	"github.com/openshift/rosa-hyperfleet-api/pkg/types"
)

// ClusterHandler handles cluster-related HTTP requests
type ClusterHandler struct {
	hyperfleetClient *hyperfleet.Client
	maestroClient    *maestro.Client
	logger           *slog.Logger
}

// NewClusterHandler creates a new cluster handler
func NewClusterHandler(hyperfleetClient *hyperfleet.Client, maestroClient *maestro.Client, logger *slog.Logger) *ClusterHandler {
	return &ClusterHandler{
		hyperfleetClient: hyperfleetClient,
		maestroClient:    maestroClient,
		logger:           logger,
	}
}

// List handles GET /api/v0/clusters
func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	status := r.URL.Query().Get("status")

	limit := 50 // default
	offset := 0 // default

	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	h.logger.Info("listing clusters", "account_id", accountID, "limit", limit, "offset", offset, "status", status)

	// Call Hyperfleet to list clusters
	clusters, total, err := h.hyperfleetClient.ListClusters(ctx, accountID, limit, offset, status)
	if err != nil {
		h.logger.Error("failed to list clusters", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-LIST-001", "Failed to list clusters")
		return
	}

	response := map[string]interface{}{
		"items":  clusters,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}

	h.writeJSON(w, http.StatusOK, response)
}

// Create handles POST /api/v0/clusters
func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	userEmail := middleware.GetUserID(ctx) // May be empty if not provided

	var req types.ClusterCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "CLUSTERS-MGMT-CREATE-001", "Invalid request body")
		return
	}

	// Validate required fields
	if req.Name == "" || req.Spec == nil {
		h.writeError(w, http.StatusBadRequest, "CLUSTERS-MGMT-CREATE-002", "Missing required fields: name and spec")
		return
	}

	// Get CloudFront URL from the first management cluster before creating the cluster
	managementClusters, err := h.maestroClient.ListConsumers(ctx, 1, 1)
	if err != nil {
		h.logger.Error("failed to list management clusters for cloudUrl", "error", err)
		h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-CREATE-004", "Failed to retrieve CloudFront URL for cluster issuer")
		return
	}

	if len(managementClusters.Items) == 0 {
		h.logger.Error("no management clusters found for cloudUrl")
		h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-CREATE-005", "No management clusters found to retrieve CloudFront URL")
		return
	}

	cloudfrontURL := managementClusters.Items[0].Labels["cloudfront_url"]
	if cloudfrontURL == "" {
		h.logger.Error("cloudfront_url label not found or empty in management cluster", "cluster_id", managementClusters.Items[0].ID)
		h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-CREATE-006", "CloudFront URL not configured in management cluster")
		return
	}

	// Add cloudUrl (CloudFront URL only) to the spec before creating the cluster
	req.Spec["cloudUrl"] = cloudfrontURL

	// Auto-populate placement from management cluster if not provided by the client
	if req.Spec["placement"] == nil || req.Spec["placement"] == "" {
		placementName := managementClusters.Items[0].Name
		if placementName == "" {
			h.logger.Error("management cluster has no name for placement", "cluster_id", managementClusters.Items[0].ID)
			h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-CREATE-007", "Management cluster name not available for placement")
			return
		}
		req.Spec["placement"] = placementName
		h.logger.Info("auto-assigned placement", "placement", placementName)
	}

	if callerARN := middleware.GetCallerARN(ctx); callerARN != "" {
		req.Spec["creatorARN"] = callerARN
	}

	h.logger.Info("creating cluster", "account_id", accountID, "cluster_name", req.Name)

	cluster, err := h.hyperfleetClient.CreateCluster(ctx, accountID, userEmail, &req)
	if err != nil {
		h.logger.Error("failed to create cluster", "error", err, "account_id", accountID)
		// Check if it's a conflict error (cluster already exists)
		if hyperfleet.IsConflict(err) {
			// Extract the actual error details from Hyperfleet
			if hfErr, ok := err.(*hyperfleet.Error); ok {
				reason := hfErr.Detail
				if reason == "" {
					reason = hfErr.Reason
				}
				if reason == "" {
					reason = "Cluster already exists"
				}
				h.writeError(w, http.StatusConflict, hfErr.Code, reason)
				return
			}
			h.writeError(w, http.StatusConflict, "CLUSTERS-MGMT-CREATE-003", "Cluster already exists")
			return
		}
		h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-CREATE-003", "Failed to create cluster")
		return
	}

	// Append cluster ID to cloudUrl in the response (but not in hyperfleet)
	if cluster.Spec == nil {
		cluster.Spec = make(map[string]interface{})
	}
	cluster.Spec["cloudUrl"] = fmt.Sprintf("%s/%s", cloudfrontURL, cluster.ID)

	h.logger.Info("cluster created with cloudUrl", "cluster_id", cluster.ID, "cloudUrl", cluster.Spec["cloudUrl"])

	h.writeJSON(w, http.StatusCreated, cluster)
}

// Get handles GET /api/v0/clusters/{id}
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterID := vars["id"]

	h.logger.Info("getting cluster", "account_id", accountID, "cluster_id", clusterID)

	cluster, err := h.hyperfleetClient.GetCluster(ctx, accountID, clusterID)
	if err != nil {
		if hyperfleet.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "CLUSTERS-MGMT-GET-001", "Cluster not found")
			return
		}
		h.logger.Error("failed to get cluster", "error", err, "account_id", accountID, "cluster_id", clusterID)
		h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-GET-002", "Failed to get cluster")
		return
	}

	h.writeJSON(w, http.StatusOK, cluster)
}

// Update handles PUT /api/v0/clusters/{id}
func (h *ClusterHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterID := vars["id"]

	var req types.ClusterUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "CLUSTERS-MGMT-UPDATE-001", "Invalid request body")
		return
	}

	if req.Spec == nil {
		h.writeError(w, http.StatusBadRequest, "CLUSTERS-MGMT-UPDATE-002", "Missing required field: spec")
		return
	}

	h.logger.Info("updating cluster", "account_id", accountID, "cluster_id", clusterID)

	cluster, err := h.hyperfleetClient.UpdateCluster(ctx, accountID, clusterID, &req)
	if err != nil {
		if hyperfleet.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "CLUSTERS-MGMT-UPDATE-003", "Cluster not found")
			return
		}
		h.logger.Error("failed to update cluster", "error", err, "account_id", accountID, "cluster_id", clusterID)
		h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-UPDATE-004", "Failed to update cluster")
		return
	}

	h.writeJSON(w, http.StatusOK, cluster)
}

// Delete handles DELETE /api/v0/clusters/{id}
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterID := vars["id"]

	forceStr := r.URL.Query().Get("force")
	force := forceStr == "true"

	h.logger.Info("deleting cluster", "account_id", accountID, "cluster_id", clusterID, "force", force)

	err := h.hyperfleetClient.DeleteCluster(ctx, accountID, clusterID, force)
	if err != nil {
		if hyperfleet.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "CLUSTERS-MGMT-DELETE-001", "Cluster not found")
			return
		}
		h.logger.Error("failed to delete cluster", "error", err, "account_id", accountID, "cluster_id", clusterID)
		h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-DELETE-002", "Failed to delete cluster")
		return
	}

	response := map[string]interface{}{
		"message":    "Cluster deletion initiated",
		"cluster_id": clusterID,
	}

	h.writeJSON(w, http.StatusAccepted, response)
}

// GetStatus handles GET /api/v0/clusters/{id}/statuses
func (h *ClusterHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterID := vars["id"]

	h.logger.Info("getting cluster status", "account_id", accountID, "cluster_id", clusterID)

	status, err := h.hyperfleetClient.GetClusterStatus(ctx, accountID, clusterID)
	if err != nil {
		if hyperfleet.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "CLUSTERS-MGMT-STATUS-001", "Cluster not found")
			return
		}
		h.logger.Error("failed to get cluster status", "error", err, "account_id", accountID, "cluster_id", clusterID)
		h.writeError(w, http.StatusInternalServerError, "CLUSTERS-MGMT-STATUS-002", "Failed to get cluster status")
		return
	}

	h.writeJSON(w, http.StatusOK, status)
}

// Helper methods
func (h *ClusterHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *ClusterHandler) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}
	_ = json.NewEncoder(w).Encode(resp)
}
