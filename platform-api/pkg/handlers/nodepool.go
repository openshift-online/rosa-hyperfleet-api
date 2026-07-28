package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/conversion"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/types"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/validation"
)

type NodePoolHandler struct {
	db         *hyperfleetdb.Client
	validator  *validation.FieldValidator
	featureSet featuregate.FeatureSet
	logger     *slog.Logger
}

func NewNodePoolHandler(db *hyperfleetdb.Client, validator *validation.FieldValidator, featureSet featuregate.FeatureSet, logger *slog.Logger) *NodePoolHandler {
	return &NodePoolHandler{
		db:         db,
		validator:  validator,
		featureSet: featureSet,
		logger:     logger,
	}
}

func (h *NodePoolHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	clusterID := r.URL.Query().Get("clusterId")

	limit := 50
	offset := 0

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

	list, err := h.db.ListNodePools(ctx, accountID, clusterID)
	if err != nil {
		h.logger.Error("failed to list nodepools", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-LIST-001", "Failed to list nodepools")
		return
	}

	nodepools := make([]*types.NodePool, 0, len(list.Items))
	for i := range list.Items {
		nodepools = append(nodepools, hyperfleetdb.NodePoolCRToPlatform(&list.Items[i]))
	}

	total := len(nodepools)

	if offset >= len(nodepools) {
		nodepools = nil
	} else {
		end := min(offset+limit, len(nodepools))
		nodepools = nodepools[offset:end]
	}

	response := map[string]any{
		"items":  nodepools,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}

	h.writeJSON(w, http.StatusOK, response)
}

func (h *NodePoolHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	var req types.NodePoolCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.writeError(w, http.StatusBadRequest, "NODEPOOLS-MGMT-CREATE-001", "Invalid request body")
		return
	}

	if req.Name == "" || req.ClusterID == "" || req.Spec == nil {
		h.writeError(w, http.StatusBadRequest, "NODEPOOLS-MGMT-CREATE-002", "Missing required fields: name, cluster_id, and spec")
		return
	}

	if errs := h.validator.ValidateNodePoolCreate(req.Spec, h.featureSet); errs != nil {
		h.writeValidationError(w, "NODEPOOLS-MGMT-VALIDATION-001", errs)
		return
	}

	if _, err := h.db.GetCluster(ctx, accountID, req.ClusterID); err != nil {
		if hyperfleetdb.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "NODEPOOLS-MGMT-CREATE-004", "Referenced cluster not found")
			return
		}
		h.logger.Error("failed to verify cluster exists", "error", err, "account_id", accountID, "cluster_id", req.ClusterID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-CREATE-005", "Failed to validate cluster reference")
		return
	}

	h.logger.Info("creating nodepool", "account_id", accountID, "cluster_id", req.ClusterID, "nodepool_name", req.Name)

	cr, err := hyperfleetdb.PlatformCreateToNodePoolCR(accountID, &req)
	if err != nil {
		h.logger.Error("failed to convert nodepool spec", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusBadRequest, "NODEPOOLS-MGMT-CREATE-002", "Invalid nodepool spec")
		return
	}

	if err := h.db.CreateNodePool(ctx, accountID, cr); err != nil {
		h.logger.Error("failed to create nodepool", "error", err, "account_id", accountID)
		if hyperfleetdb.IsAlreadyExists(err) {
			h.writeError(w, http.StatusConflict, "NODEPOOLS-MGMT-CREATE-003", "NodePool already exists")
			return
		}
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-CREATE-003", "Failed to create nodepool")
		return
	}

	h.writeJSON(w, http.StatusCreated, hyperfleetdb.NodePoolCRToPlatform(cr))
}

func (h *NodePoolHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	h.logger.Info("getting nodepool", "account_id", accountID, "nodepool_id", nodepoolID)

	cr, err := h.db.GetNodePool(ctx, accountID, nodepoolID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "NODEPOOLS-MGMT-GET-001", "NodePool not found")
			return
		}
		h.logger.Error("failed to get nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-GET-002", "Failed to get nodepool")
		return
	}

	h.writeJSON(w, http.StatusOK, hyperfleetdb.NodePoolCRToPlatform(cr))
}

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

	cr, err := h.db.GetNodePool(ctx, accountID, nodepoolID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "NODEPOOLS-MGMT-UPDATE-003", "NodePool not found")
			return
		}
		h.logger.Error("failed to get nodepool for update", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-UPDATE-004", "Failed to update nodepool")
		return
	}

	if errs := h.validator.ValidateNodePoolUpdate(req.Spec, &cr.Spec, h.featureSet); errs != nil {
		h.writeValidationError(w, "NODEPOOLS-MGMT-VALIDATION-002", errs)
		return
	}

	snapshot := cr.Spec

	if err := hyperfleetdb.ApplyPlatformUpdateToNodePoolCR(cr, &req); err != nil {
		h.logger.Error("failed to merge nodepool spec", "error", err)
		h.writeError(w, http.StatusBadRequest, "NODEPOOLS-MGMT-UPDATE-002", "Invalid nodepool spec")
		return
	}

	conversion.PreserveNodePoolServiceSet(&cr.Spec, &snapshot)

	if err := h.db.UpdateNodePool(ctx, cr); err != nil {
		h.logger.Error("failed to update nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-UPDATE-004", "Failed to update nodepool")
		return
	}

	h.writeJSON(w, http.StatusOK, hyperfleetdb.NodePoolCRToPlatform(cr))
}

func (h *NodePoolHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	h.logger.Info("deleting nodepool", "account_id", accountID, "nodepool_id", nodepoolID)

	err := h.db.DeleteNodePool(ctx, accountID, nodepoolID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "NODEPOOLS-MGMT-DELETE-001", "NodePool not found")
			return
		}
		h.logger.Error("failed to delete nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-DELETE-002", "Failed to delete nodepool")
		return
	}

	response := map[string]any{
		"message":     "NodePool deletion initiated",
		"nodepool_id": nodepoolID,
	}

	h.writeJSON(w, http.StatusAccepted, response)
}

func (h *NodePoolHandler) GetStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	h.logger.Info("getting nodepool status", "account_id", accountID, "nodepool_id", nodepoolID)

	cr, err := h.db.GetNodePool(ctx, accountID, nodepoolID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			h.writeError(w, http.StatusNotFound, "NODEPOOLS-MGMT-STATUS-001", "NodePool not found")
			return
		}
		h.logger.Error("failed to get nodepool status", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		h.writeError(w, http.StatusInternalServerError, "NODEPOOLS-MGMT-STATUS-002", "Failed to get nodepool status")
		return
	}

	h.writeJSON(w, http.StatusOK, hyperfleetdb.NodePoolStatusFromCR(cr))
}

func (h *NodePoolHandler) writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (h *NodePoolHandler) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	resp := map[string]any{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}
	_ = json.NewEncoder(w).Encode(resp)
}

func (h *NodePoolHandler) writeValidationError(w http.ResponseWriter, code string, errs validation.ValidationErrors) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	resp := map[string]any{
		"kind":    "Error",
		"code":    code,
		"reason":  "Request validation failed",
		"details": errs,
	}
	_ = json.NewEncoder(w).Encode(resp)
}
