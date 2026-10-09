package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/client-go/util/retry"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
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

	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	clusterUID := r.URL.Query().Get("clusterUID")
	clusterID := r.URL.Query().Get("clusterId")
	if clusterUID != "" && clusterID != "" && clusterUID != clusterID {
		writeAPIError(w, ErrNodePoolListInvalidClusterUID, h.logger)
		return
	}
	if clusterUID == "" {
		// The existing OpenAPI list parameter is named clusterId. Its value is
		// now the parent Cluster UID and is applied as the cluster-uid label.
		clusterUID = clusterID
	}
	if selector := r.URL.Query().Get("labelSelector"); selector != "" {
		parsed, err := labels.Parse(selector)
		if err != nil {
			writeAPIError(w, ErrNodePoolListInvalidClusterUID, h.logger)
			return
		}
		requirements, selectable := parsed.Requirements()
		if !selectable || len(requirements) != 1 || requirements[0].Key() != clusterUIDLabel ||
			(requirements[0].Operator() != selection.Equals && requirements[0].Operator() != selection.DoubleEquals) ||
			requirements[0].Values().Len() != 1 {
			writeAPIError(w, ErrNodePoolListInvalidClusterUID, h.logger)
			return
		}
		selectedUID := requirements[0].Values().List()[0]
		if clusterUID != "" && clusterUID != selectedUID {
			writeAPIError(w, ErrNodePoolListInvalidClusterUID, h.logger)
			return
		}
		clusterUID = selectedUID
	}
	if clusterUID != "" {
		parsedUID, err := uuid.Parse(clusterUID)
		if err != nil || parsedUID.String() != clusterUID {
			writeAPIError(w, ErrNodePoolListInvalidClusterUID, h.logger)
			return
		}
	}

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

	h.logger.Info("listing nodepools", "account_id", accountID, "limit", limit, "offset", offset, "cluster_uid", clusterUID)

	list, err := h.db.ListNodePools(ctx, accountID, clusterUID)
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
// Request body: public.NodePool (K8s-native). Name is <cluster>.<child>; the
// prefix resolves the parent Cluster in the authenticated account namespace.
func (h *NodePoolHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	var req public.NodePool
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, ErrNodePoolCreateInvalidBody, h.logger)
		return
	}

	if req.Name == "" {
		writeAPIError(w, ErrNodePoolCreateMissingFields, h.logger)
		return
	}
	if err := validation.ValidateAccountNamespace(req.Namespace, accountID); err != nil {
		writeAPIError(w, ErrNodePoolCreateInvalidNamespace, h.logger)
		return
	}
	clusterName, _, err := validation.ParseNodePoolName(req.Name)
	if err != nil {
		writeAPIError(w, ErrNodePoolCreateInvalidName, h.logger)
		return
	}
	// ClusterName is derived from the validated metadata.name prefix; do not
	// trust a separately supplied passthrough value to select a parent.
	req.Spec.NodePool.ClusterName = clusterName

	if errs := append(h.validator.ValidateCreate(&req.Spec, featuregate.Default), validateNodePoolReplicas(&req.Spec)...); len(errs) > 0 {
		writeAPIError(w, ErrNodePoolValidation.WithErrors(errs), h.logger)
		return
	}

	cluster, err := h.db.GetCluster(ctx, accountID, clusterName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolCreateClusterNotFound, h.logger)
			return
		}
		h.logger.Error("failed to verify cluster exists", "error", err, "account_id", accountID, "cluster_name", clusterName)
		writeAPIError(w, ErrNodePoolCreateClusterCheck, h.logger)
		return
	}
	if !cluster.DeletionTimestamp.IsZero() {
		writeAPIError(w, ErrNodePoolCreateClusterDeleting, h.logger)
		return
	}
	h.logger.Info("creating nodepool", "account_id", accountID, "cluster_uid", cluster.UID, "nodepool_name", req.Name)

	// internalPoolID is retained as a service-set implementation field; FleetDB UID
	// is the public object identity.
	internalPoolID := uuid.New().String()
	cr := hyperfleetdb.PublicToInternalNodePool(&req, accountID, internalPoolID)
	cr.Spec.NodePool.ClusterName = cluster.Name
	cr.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(cluster, hyperfleetv1alpha1.GroupVersion.WithKind("Cluster"))}
	cr.Labels["hyperfleet.io/cluster-uid"] = string(cluster.UID)

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
	nodepoolName := vars["id"]

	h.logger.Info("getting nodepool", "account_id", accountID, "nodepool_name", nodepoolName)

	cr, err := h.db.GetNodePool(ctx, accountID, nodepoolName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get nodepool", "error", err, "account_id", accountID, "nodepool_name", nodepoolName)
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
	nodepoolName := vars["id"]

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
	if err := validation.ValidateAccountNamespace(req.Namespace, accountID); err != nil {
		writeAPIError(w, ErrNodePoolCreateInvalidNamespace, h.logger)
		return
	}

	h.logger.Info("updating nodepool", "account_id", accountID, "nodepool_name", nodepoolName)

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

	var updated *hyperfleetv1alpha1.NodePool
	var validationErr validation.ValidationErrors
	var mergeErr error
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := h.db.GetNodePool(ctx, accountID, nodepoolName)
		if err != nil {
			return err
		}
		if errs := append(h.validator.ValidateUpdate(&req.Spec, &current.Spec, featuregate.Default), validateNodePoolReplicas(&req.Spec)...); len(errs) > 0 {
			validationErr = errs
			return fmt.Errorf("invalid nodepool update")
		}
		if err := hyperfleetdb.MergeSpecJSON(&current.Spec, envelope.Spec); err != nil {
			mergeErr = err
			return fmt.Errorf("merge nodepool spec: %w", err)
		}
		if err := h.db.UpdateNodePool(ctx, current); err != nil {
			return err
		}
		updated = current
		return nil
	})
	if len(validationErr) != 0 {
		writeAPIError(w, ErrNodePoolValidation.WithErrors(validationErr), h.logger)
		return
	}
	if mergeErr != nil {
		h.logger.Error("failed to merge nodepool spec", "error", mergeErr)
		writeAPIError(w, ErrNodePoolUpdateInvalidSpec, h.logger)
		return
	}
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolUpdateNotFound, h.logger)
			return
		}
		if hyperfleetdb.IsConflict(err) {
			writeAPIError(w, ErrNodePoolUpdateConflict, h.logger)
			return
		}
		h.logger.Error("failed to update nodepool", "error", err, "account_id", accountID, "nodepool_name", nodepoolName)
		writeAPIError(w, ErrNodePoolUpdateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicNodePool(updated)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

func (h *NodePoolHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolName := vars["id"]

	h.logger.Info("deleting nodepool", "account_id", accountID, "nodepool_name", nodepoolName)

	err := h.db.DeleteNodePool(ctx, accountID, nodepoolName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolDeleteNotFound, h.logger)
			return
		}
		h.logger.Error("failed to delete nodepool", "error", err, "account_id", accountID, "nodepool_name", nodepoolName)
		writeAPIError(w, ErrNodePoolDeleteFailed, h.logger)
		return
	}

	response := map[string]any{
		"message":       "NodePool deletion initiated",
		"nodepool_name": nodepoolName,
	}

	if err := api.Write(w, http.StatusAccepted, response); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}
