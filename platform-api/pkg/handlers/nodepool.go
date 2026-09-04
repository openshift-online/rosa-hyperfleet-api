package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/validation"
)

type NodePoolHandler struct {
	db        *hyperfleetdb.Client
	validator *validation.FieldValidator
	logger    *slog.Logger
}

func NewNodePoolHandler(db *hyperfleetdb.Client, logger *slog.Logger) *NodePoolHandler {
	return &NodePoolHandler{
		db:        db,
		validator: validation.NewFieldValidator("NodePool"),
		logger:    logger,
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
		writeAPIError(w, ErrNodePoolList, h.logger)
		return
	}

	nodepools := make([]*public.NodePool, 0, len(list.Items))
	for i := range list.Items {
		nodepools = append(nodepools, hyperfleetdb.InternalToPublicNodePool(&list.Items[i]))
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

	if err := api.Write(w, http.StatusOK, response); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Create handles POST /api/v0/nodepools
// Request body: public.NodePool (K8s-native). Name comes from metadata.name;
// cluster association comes from metadata.namespace which must be the canonical
// "cluster-<uuid>" form (e.g. "cluster-550e8400-e29b-41d4-a716-446655440000").
func (h *NodePoolHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	var req public.NodePool
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, ErrNodePoolCreateInvalidBody, h.logger)
		return
	}

	if req.Name == "" || req.Namespace == "" {
		writeAPIError(w, ErrNodePoolCreateMissingFields, h.logger)
		return
	}

	// Namespace must be the exact canonical form "cluster-<uuid>" (lowercase).
	// Parse the suffix and round-trip through uuid.String() to reject non-canonical
	// forms (e.g. uppercase) that would pass uuid.Parse but fail GetCluster lookup.
	clusterIDRaw := hyperfleetdb.ClusterIDFromNamespace(req.Namespace)
	parsedUUID, err := uuid.Parse(clusterIDRaw)
	if err != nil || req.Namespace != hyperfleetdb.ClusterNSPrefix+parsedUUID.String() {
		writeAPIError(w, ErrNodePoolCreateInvalidNamespace, h.logger)
		return
	}
	clusterID := parsedUUID.String()

	if errs := h.validator.ValidateCreate(&req.Spec, featuregate.Default); errs != nil {
		writeAPIError(w, ErrNodePoolValidation.WithErrors(errs), h.logger)
		return
	}

	if _, err := h.db.GetCluster(ctx, accountID, clusterID); err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolCreateClusterNotFound, h.logger)
			return
		}
		h.logger.Error("failed to verify cluster exists", "error", err, "account_id", accountID, "cluster_id", clusterID)
		writeAPIError(w, ErrNodePoolCreateClusterCheck, h.logger)
		return
	}

	h.logger.Info("creating nodepool", "account_id", accountID, "cluster_id", clusterID, "nodepool_name", req.Name)

	// internalPoolID is a platform-assigned UUID stored as a service-set field.
	// The public-facing UID (used by SDK callers) is the NodePool name, set by
	// InternalToPublicNodePool from cr.Name.
	internalPoolID := uuid.New().String()
	cr := hyperfleetdb.PublicToInternalNodePool(&req, accountID, clusterID, internalPoolID)

	if err := h.db.CreateNodePool(ctx, accountID, cr); err != nil {
		h.logger.Error("failed to create nodepool", "error", err, "account_id", accountID)
		if hyperfleetdb.IsAlreadyExists(err) {
			writeAPIError(w, ErrNodePoolCreateNameConflict, h.logger)
			return
		}
		writeAPIError(w, ErrNodePoolCreateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusCreated, hyperfleetdb.InternalToPublicNodePool(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
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
			writeAPIError(w, ErrNodePoolGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		writeAPIError(w, ErrNodePoolGetFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicNodePool(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Update handles PUT /api/v0/nodepools/{id}
// Request body: public.NodePool (K8s-native). Only spec fields are merged; metadata is ignored.
func (h *NodePoolHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, ErrNodePoolUpdateInvalidBody, h.logger)
		return
	}

	var req public.NodePool
	if err := json.Unmarshal(body, &req); err != nil {
		writeAPIError(w, ErrNodePoolUpdateInvalidBody, h.logger)
		return
	}

	h.logger.Info("updating nodepool", "account_id", accountID, "nodepool_id", nodepoolID)

	cr, err := h.db.GetNodePool(ctx, accountID, nodepoolID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolUpdateNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get nodepool for update", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		writeAPIError(w, ErrNodePoolUpdateFailed, h.logger)
		return
	}

	if errs := h.validator.ValidateUpdate(&req.Spec, &cr.Spec, featuregate.Default); errs != nil {
		writeAPIError(w, ErrNodePoolValidation.WithErrors(errs), h.logger)
		return
	}

	var envelope struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeAPIError(w, ErrNodePoolUpdateInvalidBody, h.logger)
		return
	}

	// Reject semantically empty specs (nil, empty decoded maps, whitespace variants).
	var rawSpec map[string]any
	if err := json.Unmarshal(envelope.Spec, &rawSpec); err != nil {
		writeAPIError(w, ErrNodePoolUpdateInvalidBody, h.logger)
		return
	}
	if len(rawSpec) == 0 {
		writeAPIError(w, ErrNodePoolUpdateMissingFields, h.logger)
		return
	}
	if err := hyperfleetdb.MergeSpecJSON(&cr.Spec, envelope.Spec); err != nil {
		h.logger.Error("failed to merge nodepool spec", "error", err)
		writeAPIError(w, ErrNodePoolUpdateInvalidSpec, h.logger)
		return
	}

	if err := h.db.UpdateNodePool(ctx, cr); err != nil {
		h.logger.Error("failed to update nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		writeAPIError(w, ErrNodePoolUpdateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicNodePool(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
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
			writeAPIError(w, ErrNodePoolDeleteNotFound, h.logger)
			return
		}
		h.logger.Error("failed to delete nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		writeAPIError(w, ErrNodePoolDeleteFailed, h.logger)
		return
	}

	response := map[string]any{
		"message":     "NodePool deletion initiated",
		"nodepool_id": nodepoolID,
	}

	if err := api.Write(w, http.StatusAccepted, response); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}
