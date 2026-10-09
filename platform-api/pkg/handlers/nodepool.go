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
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/validation"
)

type NodePoolHandler struct {
	db         *hyperfleetdb.Client
	validator  *validation.FieldValidator
	logger     *slog.Logger
	authorizer *authz.Authorizer
}

func NewNodePoolHandler(db *hyperfleetdb.Client, authorizer *authz.Authorizer, logger *slog.Logger) *NodePoolHandler {
	if authorizer == nil {
		panic("node pool authorizer is required")
	}
	return &NodePoolHandler{
		authorizer: authorizer,
		db:         db,
		validator:  validation.NewFieldValidator("NodePool"),
		logger:     logger,
	}
}

func validateNodePoolReplicas(spec *public.NodePoolSpec) validation.ValidationErrors {
	if spec == nil || spec.NodePool.Replicas == nil || *spec.NodePool.Replicas >= 0 {
		return nil
	}
	return validation.ValidationErrors{
		&validation.ValidationError{
			Field:  "spec.nodePool.replicas",
			Reason: "must be greater than or equal to 0",
		},
	}
}

func (h *NodePoolHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	prepared, attempt := authorizeCollection(w, r, h.authorizer, authz.DefaultMetrics, h.logger, authz.ListNodePools, authz.Resource{Kind: authz.Collection, CollectionKind: authz.NodePool, AccountID: accountID})
	if prepared == nil {
		return
	}

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

	list, err := h.db.ListNodePools(ctx, clusterID)
	if err != nil {
		h.logger.Error("failed to list nodepools", "error", err, "account_id", accountID)
		_ = attempt.Finish(authz.OutcomeError, authz.StageResourceLoading)
		writeAPIError(w, ErrNodePoolList, h.logger)
		return
	}

	nodepools := make([]*public.NodePool, 0, len(list.Items))
	for i := range list.Items {
		resource, err := h.nodePoolResource(ctx, &list.Items[i])
		if err != nil {
			writeResourceAuthzError(w, r, attempt, authz.ListNodePools, err, h.logger)
			return
		}
		decision, err := prepared.Check(ctx, authz.DescribeNodePool, resource)
		if err != nil {
			writeResourceAuthzError(w, r, attempt, authz.ListNodePools, err, h.logger)
			return
		}
		if decision.Allowed {
			nodepools = append(nodepools, hyperfleetdb.InternalToPublicNodePool(&list.Items[i]))
		}
	}
	_ = attempt.Finish(authz.OutcomeAllow, authz.StageNone)

	total := len(nodepools)

	if offset >= len(nodepools) {
		nodepools = nil
	} else {
		end := offset + min(limit, len(nodepools)-offset)
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

	if errs := append(h.validator.ValidateCreate(&req.Spec, featuregate.Default), validateNodePoolReplicas(&req.Spec)...); len(errs) > 0 {
		writeAPIError(w, ErrNodePoolValidation.WithErrors(errs), h.logger)
		return
	}

	if _, err := h.db.GetCluster(ctx, clusterID); err != nil {
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

	if !h.authorizeNodePool(w, r, authz.CreateNodePool, cr) {
		return
	}
	if err := h.db.CreateNodePool(ctx, cr); err != nil {
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
	clusterID := r.URL.Query().Get("clusterId")
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	h.logger.Info("getting nodepool", "account_id", accountID, "cluster_id", clusterID, "nodepool_id", nodepoolID)

	cr, err := h.db.GetNodePool(ctx, clusterID, nodepoolID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		writeAPIError(w, ErrNodePoolGetFailed, h.logger)
		return
	}

	if !h.authorizeNodePool(w, r, authz.DescribeNodePool, cr) {
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
	clusterID := r.URL.Query().Get("clusterId")
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, ErrNodePoolUpdateInvalidBody, h.logger)
		return
	}

	var envelope struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeAPIError(w, ErrNodePoolUpdateInvalidBody, h.logger)
		return
	}

	h.logger.Info("updating nodepool", "account_id", accountID, "cluster_id", clusterID, "nodepool_id", nodepoolID)

	cr, err := h.db.GetNodePool(ctx, clusterID, nodepoolID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolUpdateNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get nodepool for update", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		writeAPIError(w, ErrNodePoolUpdateFailed, h.logger)
		return
	}

	var rawSpec map[string]any
	if err := json.Unmarshal(envelope.Spec, &rawSpec); err != nil {
		writeAPIError(w, ErrNodePoolUpdateInvalidBody, h.logger)
		return
	}
	if len(rawSpec) == 0 {
		writeAPIError(w, ErrNodePoolUpdateMissingFields, h.logger)
		return
	}
	candidate := cr.DeepCopy()
	if err := hyperfleetdb.MergeSpecJSON(&candidate.Spec, envelope.Spec); err != nil {
		h.logger.Error("failed to merge nodepool spec", "error", err)
		writeAPIError(w, ErrNodePoolUpdateInvalidSpec, h.logger)
		return
	}

	publicCandidate := hyperfleetdb.InternalToPublicNodePool(candidate)
	if errs := validateNodePoolReplicas(&publicCandidate.Spec); len(errs) > 0 {
		writeAPIError(w, ErrNodePoolValidation.WithErrors(errs), h.logger)
		return
	}

	actions, errs := h.validator.AnalyzeUpdate(envelope.Spec, &cr.Spec, &candidate.Spec, featuregate.Default)
	if errs != nil {
		writeAPIError(w, ErrNodePoolValidation.WithErrors(errs), h.logger)
		return
	}
	if !h.authorizeNodePool(w, r, authz.UpdateNodePool, cr, actions...) {
		return
	}
	if err := h.db.UpdateNodePool(ctx, candidate); err != nil {
		if hyperfleetdb.IsConflict(err) {
			writeAPIError(w, ErrResourceConflict, h.logger)
			return
		}
		h.logger.Error("failed to update nodepool", "error", err, "account_id", accountID, "nodepool_id", nodepoolID)
		writeAPIError(w, ErrNodePoolUpdateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicNodePool(candidate)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

func (h *NodePoolHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	clusterID := r.URL.Query().Get("clusterId")
	vars := mux.Vars(r)
	nodepoolID := vars["id"]

	h.logger.Info("deleting nodepool", "account_id", accountID, "cluster_id", clusterID, "nodepool_id", nodepoolID)

	cr, err := h.db.GetNodePool(ctx, clusterID, nodepoolID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolDeleteNotFound, h.logger)
			return
		}
		writeAPIError(w, ErrNodePoolDeleteFailed, h.logger)
		return
	}
	if !h.authorizeNodePool(w, r, authz.DeleteNodePool, cr) {
		return
	}
	err = h.db.DeleteNodePoolObject(ctx, cr)
	if err != nil {
		if hyperfleetdb.IsConflict(err) {
			writeAPIError(w, ErrResourceConflict, h.logger)
			return
		}
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
