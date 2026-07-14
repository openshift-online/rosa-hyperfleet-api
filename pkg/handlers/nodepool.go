package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	"github.com/openshift/rosa-hyperfleet-api/pkg/types"
)

// NodePoolHandler handles nodepool-related HTTP requests
type NodePoolHandler struct {
	maestroClient *maestro.Client
	logger        *slog.Logger
}

// NewNodePoolHandler creates a new nodepool handler
func NewNodePoolHandler(maestroClient *maestro.Client, logger *slog.Logger) *NodePoolHandler {
	return &NodePoolHandler{
		maestroClient: maestroClient,
		logger:        logger,
	}
}

// List handles GET /api/v0/nodepools
func (h *NodePoolHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	clusterID := r.URL.Query().Get("clusterId")

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

	h.logger.Info("listing nodepools", "account_id", accountID, "limit", limit, "offset", offset, "cluster_id", clusterID)

	// Call Maestro to list nodepools
	nodepools, total, err := h.maestroClient.ListNodePools(ctx, accountID, limit, offset, clusterID)
	if err != nil {
		h.logger.Error("failed to list nodepools", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-LIST-001", "Failed to list nodepools")
		return
	}

	response := map[string]interface{}{
		"items":  nodepools,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}

	h.writeJSON(w, http.StatusOK, response)
}

// Create handles POST /api/v0/nodepools
func (h *NodePoolHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	userEmail := middleware.GetUserID(ctx) // May be empty if not provided

	var req types.NodePoolCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "NODEPOOLS-MGMT-CREATE-001", "Invalid request body")
		return
	}

	// Validate required fields
	if req.Name == "" || req.ClusterID == "" || req.Spec == nil {
		h.writeError(w, http.StatusBadRequest, "NODEPOOLS-MGMT-CREATE-002", "Missing required fields: name, cluster_id, and spec")
		return
	}

	h.logger.Info("creating nodepool", "account_id", accountID, "cluster_id", req.ClusterID, "nodepool_name", req.Name)

	nodepool, err := h.maestroClient.CreateNodePool(ctx, accountID, userEmail, &req)
	if err != nil {
		h.logger.Error("failed to create nodepool", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-CREATE-003", "Failed to create nodepool")
		return
	}

	h.writeJSON(w, http.StatusCreated, nodepool)
}

// Get handles GET /api/v0/nodepools/{id}
func (h *NodePoolHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	h.logger.Info("getting nodepool", "account_id", accountID, "nodepool_id", nodepoolID)

	nodepool, err := h.maestroClient.GetNodePool(ctx, accountID, nodepoolID)
	if err != nil {
		if maestro.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "NODEPOOLS-MGMT-GET-001", "NodePool not found")
			return
		}
		h.logger.Error("failed to get nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-GET-002", "Failed to get nodepool")
		return
	}

	h.writeJSON(w, http.StatusOK, nodepool)
}

// Update handles PUT /api/v0/nodepools/{id}
func (h *NodePoolHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	var req types.NodePoolUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "NODEPOOLS-MGMT-UPDATE-001", "Invalid request body")
		return
	}

	if req.Spec == nil {
		h.writeError(w, http.StatusBadRequest, "NODEPOOLS-MGMT-UPDATE-002", "Missing required field: spec")
		return
	}

	h.logger.Info("updating nodepool", "account_id", accountID, "nodepool_id", nodepoolID)

	nodepool, err := h.maestroClient.UpdateNodePool(ctx, accountID, nodepoolID, &req)
	if err != nil {
		if maestro.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "NODEPOOLS-MGMT-UPDATE-003", "NodePool not found")
			return
		}
		h.logger.Error("failed to update nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-UPDATE-004", "Failed to update nodepool")
		return
	}

	h.writeJSON(w, http.StatusOK, nodepool)
}

// Delete handles DELETE /api/v0/nodepools/{id}
func (h *NodePoolHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	h.logger.Info("deleting nodepool", "account_id", accountID, "nodepool_id", nodepoolID)

	err := h.maestroClient.DeleteNodePool(ctx, accountID, nodepoolID)
	if err != nil {
		if maestro.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "NODEPOOLS-MGMT-DELETE-001", "NodePool not found")
			return
		}
		h.logger.Error("failed to delete nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-DELETE-002", "Failed to delete nodepool")
		return
	}

	response := map[string]interface{}{
		"message":     "NodePool deletion initiated",
		"nodepool_id": nodepoolID,
	}

	h.writeJSON(w, http.StatusAccepted, response)
}

// GetStatus handles GET /api/v0/nodepools/{id}/status
func (h *NodePoolHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	h.logger.Info("getting nodepool status", "account_id", accountID, "nodepool_id", nodepoolID)

	status, err := h.maestroClient.GetNodePoolStatus(ctx, accountID, nodepoolID)
	if err != nil {
		if maestro.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "NODEPOOLS-MGMT-STATUS-001", "NodePool not found")
			return
		}
		h.logger.Error("failed to get nodepool status", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-STATUS-002", "Failed to get nodepool status")
		return
	}

	h.writeJSON(w, http.StatusOK, status)
}

// Helper methods
func (h *NodePoolHandler) writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *NodePoolHandler) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}
	_ = json.NewEncoder(w).Encode(resp)
}
