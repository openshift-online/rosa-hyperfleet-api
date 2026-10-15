package handlers

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
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

// validateRootVolumeNullOnUpdate rejects an explicit null anywhere along
// nodePool.platform.aws.rootVolume when the existing RootVolume is non-nil.
func validateRootVolumeNullOnUpdate(rawSpec map[string]any, existing *hyperfleetv1alpha1.NodePoolSpec) validation.ValidationErrors {
	if existing == nil || existing.NodePool.Platform.AWS == nil || existing.NodePool.Platform.AWS.RootVolume == nil {
		return nil
	}

	node := rawSpec
	for _, key := range []string{"nodePool", "platform", "aws", "rootVolume"} {
		val, exists := node[key]
		if !exists {
			return nil
		}
		if val == nil {
			return rootVolumeSizeImmutableError()
		}
		next, ok := val.(map[string]any)
		if !ok {
			return nil
		}
		node = next
	}
	return nil
}

func rootVolumeSizeImmutableError() validation.ValidationErrors {
	return validation.ValidationErrors{
		&validation.ValidationError{
			Field:  "spec.nodePool.platform.aws.rootVolume.size",
			Reason: "field is immutable and cannot be changed after creation",
		},
	}
}

func (h *NodePoolHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	prepared, attempt := authorizeCollection(w, r, h.authorizer, h.logger, authz.ListNodePools, authz.Resource{Kind: authz.Collection, CollectionKind: authz.NodePool, AccountID: accountID})
	if prepared == nil {
		return
	}

	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	clusterUID, apiErr := listClusterUID(r.URL.Query())
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
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

	list, err := h.db.ListNodePools(ctx, clusterUID)
	if err != nil {
		h.logger.Error("failed to list nodepools", "error", err, "account_id", accountID)
		attempt.Finish(authz.OutcomeError, authz.StageResourceLoading)
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
		np := hyperfleetdb.InternalToPublicNodePool(&list.Items[i])
		if !decision.Allowed {
			np = &public.NodePool{ObjectMeta: listMeta(np.ObjectMeta), Status: public.NodePoolStatus{Phase: np.Status.Phase}}
		}
		nodepools = append(nodepools, np)
	}
	attempt.Finish(authz.OutcomeAllow, authz.StageNone)

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

// listClusterUID resolves the optional parent Cluster UID filter from clusterUID,
// clusterId, or a cluster-uid labelSelector.
func listClusterUID(query url.Values) (string, *APIError) {
	clusterUID := query.Get("clusterUID")
	clusterID := query.Get("clusterId")
	if clusterUID != "" && clusterID != "" && clusterUID != clusterID {
		return "", &ErrNodePoolListInvalidClusterUID
	}
	if clusterUID == "" {
		// The existing OpenAPI list parameter is named clusterId. Its value is
		// now the parent Cluster UID and is applied as the cluster-uid label.
		clusterUID = clusterID
	}
	if selector := query.Get("labelSelector"); selector != "" {
		parsed, err := labels.Parse(selector)
		if err != nil {
			return "", &ErrNodePoolListInvalidLabelSelector
		}
		requirements, selectable := parsed.Requirements()
		if !selectable || len(requirements) != 1 || requirements[0].Key() != clusterUIDLabel ||
			(requirements[0].Operator() != selection.Equals && requirements[0].Operator() != selection.DoubleEquals) ||
			requirements[0].Values().Len() != 1 {
			return "", &ErrNodePoolListInvalidLabelSelector
		}
		selectedUID := requirements[0].Values().List()[0]
		if clusterUID != "" && clusterUID != selectedUID {
			return "", &ErrNodePoolListInvalidLabelSelector
		}
		clusterUID = selectedUID
	}
	if clusterUID != "" {
		parsedUID, err := uuid.Parse(clusterUID)
		if err != nil || parsedUID.String() != clusterUID {
			return "", &ErrNodePoolListInvalidClusterUID
		}
	}
	return clusterUID, nil
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

	cluster, err := h.db.GetCluster(ctx, clusterName)
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
	vars := mux.Vars(r)
	nodepoolName := vars["id"]

	h.logger.Info("getting nodepool", "account_id", accountID, "nodepool_name", nodepoolName)

	cr, err := h.db.GetNodePool(ctx, nodepoolName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get nodepool", "error", err, "account_id", accountID, "nodepool_name", nodepoolName)
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
		writeAPIError(w, ErrNodePoolUpdateInvalidNamespace, h.logger)
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

	var rawSpec map[string]any
	if err := json.Unmarshal(envelope.Spec, &rawSpec); err != nil {
		writeAPIError(w, ErrNodePoolUpdateInvalidBody, h.logger)
		return
	}
	if len(rawSpec) == 0 {
		writeAPIError(w, ErrNodePoolUpdateMissingFields, h.logger)
		return
	}

	// Retry conflicts like upstream. Each attempt re-reads, re-validates, and
	// re-authorizes, so no write uses a snapshot that was not checked.
	var updated *hyperfleetv1alpha1.NodePool
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var attemptErr error
		updated, attemptErr = h.updateOnce(w, r, nodepoolName, &req.Spec, rawSpec, envelope.Spec)
		return attemptErr
	})
	if errors.Is(err, errResponded) {
		return
	}
	if err != nil {
		writeAPIError(w, ErrNodePoolUpdateConflict, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicNodePool(updated)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// updateOnce makes one checked update attempt; see ClusterHandler.updateOnce.
func (h *NodePoolHandler) updateOnce(w http.ResponseWriter, r *http.Request, nodepoolName string, reqSpec *public.NodePoolSpec, rawSpec map[string]any, spec json.RawMessage) (*hyperfleetv1alpha1.NodePool, error) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	cr, err := h.db.GetNodePool(ctx, nodepoolName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolUpdateNotFound, h.logger)
			return nil, errResponded
		}
		h.logger.Error("failed to get nodepool for update", "error", err, "account_id", accountID, "nodepool_name", nodepoolName)
		writeAPIError(w, ErrNodePoolUpdateFailed, h.logger)
		return nil, errResponded
	}

	errs := h.validator.ValidateUpdate(rawSpec, &cr.Spec, featuregate.Default)
	errs = append(errs, validateNodePoolReplicas(reqSpec)...)
	errs = append(errs, validateRootVolumeNullOnUpdate(rawSpec, &cr.Spec)...)
	if len(errs) > 0 {
		writeAPIError(w, ErrNodePoolValidation.WithErrors(errs), h.logger)
		return nil, errResponded
	}
	candidate := cr.DeepCopy()
	if err := hyperfleetdb.MergeSpecJSON(&candidate.Spec, spec); err != nil {
		h.logger.Error("failed to merge nodepool spec", "error", err)
		writeAPIError(w, ErrNodePoolUpdateInvalidSpec, h.logger)
		return nil, errResponded
	}
	if err := h.validator.PreserveServiceSet(&candidate.Spec, &cr.Spec, featuregate.Default); err != nil {
		h.logger.Error("failed to preserve service-set fields", "error", err, "account_id", accountID, "nodepool_name", nodepoolName)
		writeAPIError(w, ErrNodePoolUpdateFailed, h.logger)
		return nil, errResponded
	}

	// Authorize the stored snapshot and every changed-field action before its CAS write.
	if !h.authorizeNodePool(w, r, authz.UpdateNodePool, cr, nodePoolUpdateActions(&cr.Spec, &candidate.Spec)...) {
		return nil, errResponded
	}
	if err := h.db.UpdateNodePool(ctx, candidate); err != nil {
		if hyperfleetdb.IsConflict(err) {
			return nil, err
		}
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolUpdateNotFound, h.logger)
			return nil, errResponded
		}
		h.logger.Error("failed to update nodepool", "error", err, "account_id", accountID, "nodepool_name", nodepoolName)
		writeAPIError(w, ErrNodePoolUpdateFailed, h.logger)
		return nil, errResponded
	}
	return candidate, nil
}

func (h *NodePoolHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	nodepoolName := vars["id"]

	h.logger.Info("deleting nodepool", "account_id", accountID, "nodepool_name", nodepoolName)

	cr, err := h.db.GetNodePool(ctx, nodepoolName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrNodePoolDeleteNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get nodepool for delete", "error", err, "account_id", accountID, "nodepool_name", nodepoolName)
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
