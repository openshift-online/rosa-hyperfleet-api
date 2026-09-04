package handlers

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/validation"
)

// ClusterHandler handles cluster-related HTTP requests
type ClusterHandler struct {
	db                       *hyperfleetdb.Client
	oidcIssuerBaseURL        string
	defaultClusterExpiration time.Duration
	validator                *validation.FieldValidator
	logger                   *slog.Logger
	generateID               func() string
}

// NewClusterHandler creates a new cluster handler
func NewClusterHandler(db *hyperfleetdb.Client, oidcIssuerBaseURL string, defaultClusterExpiration time.Duration, logger *slog.Logger) *ClusterHandler {
	return &ClusterHandler{
		db:                       db,
		oidcIssuerBaseURL:        oidcIssuerBaseURL,
		defaultClusterExpiration: defaultClusterExpiration,
		validator:                validation.NewFieldValidator("Cluster"),
		logger:                   logger,
		generateID:               func() string { return uuid.New().String() },
	}
}

// List handles GET /api/v0/clusters
func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

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

	h.logger.Info("listing clusters", "account_id", accountID, "limit", limit, "offset", offset)

	list, err := h.db.ListClusters(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to list clusters", "error", err, "account_id", accountID)
		writeAPIError(w, ErrClusterList, h.logger)
		return
	}

	clusters := make([]*public.Cluster, 0, len(list.Items))
	for i := range list.Items {
		clusters = append(clusters, hyperfleetdb.InternalToPublicCluster(&list.Items[i]))
	}

	total := len(clusters)

	// Apply offset/limit pagination in-memory.
	if offset >= len(clusters) {
		clusters = []*public.Cluster{}
	} else {
		end := min(offset+limit, len(clusters))
		clusters = clusters[offset:end]
	}

	response := map[string]any{
		"items":  clusters,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}

	if err := api.Write(w, http.StatusOK, response); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Create handles POST /api/v0/clusters
// Request body: public.Cluster (K8s-native). Name comes from metadata.name;
// accountID is taken from the authenticated identity (middleware), not from the body.
func (h *ClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, ErrClusterCreateInvalidBody, h.logger)
		return
	}

	var req public.Cluster
	if err := json.Unmarshal(body, &req); err != nil {
		writeAPIError(w, ErrClusterCreateInvalidBody, h.logger)
		return
	}

	// Require both metadata.name and a non-empty, non-null spec key to be present.
	var envelope struct {
		Spec json.RawMessage `json:"spec"`
	}
	_ = json.Unmarshal(body, &envelope)
	specStr := string(envelope.Spec)
	if req.Name == "" || len(envelope.Spec) == 0 || specStr == "{}" || specStr == "null" {
		writeAPIError(w, ErrClusterCreateMissingFields, h.logger)
		return
	}

	if len(req.Name) > hyperfleetdb.MaxClusterNameLen {
		writeAPIError(w, ErrClusterCreateNameTooLong, h.logger)
		return
	}

	if errs := h.validator.ValidateCreate(&req.Spec, featuregate.Default); errs != nil {
		writeAPIError(w, ErrClusterValidation.WithErrors(errs), h.logger)
		return
	}

	existing, err := h.db.ListClusters(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to check cluster name uniqueness", "error", err, "account_id", accountID)
		writeAPIError(w, ErrClusterCreateNameCheck, h.logger)
		return
	}
	for i := range existing.Items {
		if existing.Items[i].Name == req.Name {
			writeAPIError(w, ErrClusterCreateNameConflict.WithReason(req.Name), h.logger)
			return
		}
	}

	// If oidcConfigId is set, resolve it and derive issuerURL from it below.
	// Otherwise fall back to the legacy auto-generated issuerURL.
	oidcConfig, apiErr := h.resolveOidcConfig(ctx, accountID, req.Spec.OidcConfigID, existing)
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
	}

	clusterID := h.generateID()

	const maxHash4Retries = 5
	for attempt := 0; attempt < maxHash4Retries; attempt++ {
		h.logger.Info("creating cluster", "account_id", accountID, "cluster_name", req.Name, "cluster_id", clusterID)

		if h.defaultClusterExpiration > 0 && req.Spec.ExpirationTimestamp == nil {
			expiry := metav1.NewTime(time.Now().Add(h.defaultClusterExpiration))
			req.Spec.ExpirationTimestamp = &expiry
		}

		cr := hyperfleetdb.PublicToInternalCluster(&req, accountID, clusterID)

		// Set service-set fields on the internal CRD (not visible in public request/response)
		if callerARN := middleware.GetCallerARN(ctx); callerARN != "" {
			cr.Spec.CreatorARN = callerARN
		}
		// issuerURL is service-set; derive it here rather than accept it from the caller.
		if oidcConfig != nil {
			cr.Spec.HostedCluster.IssuerURL = oidcConfig.Spec.IssuerUrl
		} else if h.oidcIssuerBaseURL != "" {
			cr.Spec.HostedCluster.IssuerURL = h.oidcIssuerBaseURL + "/" + clusterID
		}

		if err := h.db.CreateCluster(ctx, accountID, cr); err != nil {
			if hyperfleetdb.IsAlreadyExists(err) {
				// AlreadyExists here means either (a) the generated clusterID
				// collided (hash4 collision), or (b) a concurrent request already
				// claimed this oidcConfigId (idx_cluster_oidcconfig_id). Retrying
				// with a new clusterID only fixes (a) — for (b) it would just
				// collide again, eventually masking a real conflict as a 500.
				// Re-check now so we can return the correct 409 instead.
				if req.Spec.OidcConfigID != "" {
					if inUse, checkErr := h.isOidcConfigInUse(ctx, accountID, req.Spec.OidcConfigID); checkErr != nil {
						h.logger.Error("failed to re-check oidc config usage after create conflict", "error", checkErr, "account_id", accountID, "oidc_config_id", req.Spec.OidcConfigID)
					} else if inUse {
						apiErr := ErrClusterCreateOidcConfigInUse.WithReason(req.Spec.OidcConfigID)
						writeAPIError(w, apiErr, h.logger)
						return
					}
				}
				if attempt < maxHash4Retries-1 {
					clusterID = h.generateID()
					continue
				}
				h.logger.Error("failed to create cluster", "error", err, "account_id", accountID)
				writeAPIError(w, ErrClusterCreateIDExhausted, h.logger)
				return
			}
			h.logger.Error("failed to create cluster", "error", err, "account_id", accountID)
			writeAPIError(w, ErrClusterCreateFailed, h.logger)
			return
		}

		if oidcConfig != nil {
			if err := h.db.UpdateOidcConfigLastUsedTimestamp(ctx, accountID, req.Spec.OidcConfigID, metav1.Now()); err != nil {
				h.logger.Warn("failed to update oidc config lastUsedTimestamp", "error", err, "account_id", accountID, "oidc_config_id", req.Spec.OidcConfigID)
			}
		}

		if err := api.Write(w, http.StatusCreated, hyperfleetdb.InternalToPublicCluster(cr)); err != nil {
			h.logger.Error("failed to write response", "error", err)
		}
		return
	}
}

// resolveOidcConfig validates the OidcConfig referenced by oidcConfigID.
func (h *ClusterHandler) resolveOidcConfig(ctx context.Context, accountID, oidcConfigID string, existingClusters *hyperfleetv1alpha1.ClusterList) (*hyperfleetv1alpha1.OidcConfig, *APIError) {
	if oidcConfigID == "" {
		return nil, nil
	}

	oidcConfig, err := h.db.GetOidcConfig(ctx, accountID, oidcConfigID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			return nil, &ErrClusterCreateOidcConfigNotFound
		}
		h.logger.Error("failed to look up oidc config", "error", err, "account_id", accountID, "oidc_config_id", oidcConfigID)
		return nil, &ErrClusterCreateOidcConfigLookupFailed
	}

	notReady := oidcConfig.Status.Phase == hyperfleetv1alpha1.OidcConfigPhaseError ||
		(oidcConfig.Spec.Type == hyperfleetv1alpha1.OidcConfigTypeUnmanaged && oidcConfig.Status.Phase != hyperfleetv1alpha1.OidcConfigPhaseReady)
	if notReady {
		return nil, &ErrClusterCreateOidcConfigNotReady
	}

	if oidcConfigReferencedBy(existingClusters, oidcConfigID) {
		err := ErrClusterCreateOidcConfigInUse.WithReason(oidcConfigID)
		return nil, &err
	}

	return oidcConfig, nil
}

// oidcConfigReferencedBy reports whether any cluster in clusters already
// sets spec.oidcConfigId to oidcConfigID.
func oidcConfigReferencedBy(clusters *hyperfleetv1alpha1.ClusterList, oidcConfigID string) bool {
	for i := range clusters.Items {
		if clusters.Items[i].Spec.OidcConfigID == oidcConfigID {
			return true
		}
	}
	return false
}

// isOidcConfigInUse re-lists accountID's clusters and reports whether
// oidcConfigID is now referenced by one of them.
func (h *ClusterHandler) isOidcConfigInUse(ctx context.Context, accountID, oidcConfigID string) (bool, error) {
	clusters, err := h.db.ListClusters(ctx, accountID)
	if err != nil {
		return false, err
	}
	return oidcConfigReferencedBy(clusters, oidcConfigID), nil
}

// Get handles GET /api/v0/clusters/{id}
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterID := vars["id"]

	h.logger.Info("getting cluster", "account_id", accountID, "cluster_id", clusterID)

	cr, err := h.db.GetCluster(ctx, accountID, clusterID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster", "error", err, "account_id", accountID, "cluster_id", clusterID)
		writeAPIError(w, ErrClusterGetFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicCluster(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Update handles PUT/PATCH /api/v0/clusters/{id}
// Request body: public.Cluster (K8s-native). Only spec fields are merged; metadata is ignored.
func (h *ClusterHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterID := vars["id"]

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, ErrClusterUpdateInvalidBody, h.logger)
		return
	}

	var req public.Cluster
	if err := json.Unmarshal(body, &req); err != nil {
		writeAPIError(w, ErrClusterUpdateInvalidBody, h.logger)
		return
	}

	h.logger.Info("updating cluster", "account_id", accountID, "cluster_id", clusterID)

	cr, err := h.db.GetCluster(ctx, accountID, clusterID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterUpdateNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster for update", "error", err, "account_id", accountID, "cluster_id", clusterID)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return
	}

	if errs := h.validator.ValidateUpdate(&req.Spec, &cr.Spec, featuregate.Default); errs != nil {
		writeAPIError(w, ErrClusterValidation.WithErrors(errs), h.logger)
		return
	}

	// Extract raw "spec" JSON from the request body so the merge only
	// overwrites fields the caller actually sent, preserving service-set
	// fields that lack omitempty (e.g. hostedCluster, nodePool).
	var envelope struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeAPIError(w, ErrClusterUpdateInvalidBody, h.logger)
		return
	}

	// Reject absent spec (nil) and empty spec object ({}) — both are no-ops
	// that indicate a malformed request rather than a deliberate partial update.
	if len(envelope.Spec) == 0 || string(envelope.Spec) == "{}" {
		writeAPIError(w, ErrClusterUpdateMissingFields, h.logger)
		return
	}
	if err := hyperfleetdb.MergeSpecJSON(&cr.Spec, envelope.Spec); err != nil {
		h.logger.Error("failed to merge cluster spec", "error", err)
		writeAPIError(w, ErrClusterUpdateInvalidSpec, h.logger)
		return
	}

	if err := h.db.UpdateCluster(ctx, cr); err != nil {
		h.logger.Error("failed to update cluster", "error", err, "account_id", accountID, "cluster_id", clusterID)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicCluster(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Delete handles DELETE /api/v0/clusters/{id}
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterID := vars["id"]

	h.logger.Info("deleting cluster", "account_id", accountID, "cluster_id", clusterID)

	err := h.db.DeleteCluster(ctx, accountID, clusterID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterDeleteNotFound, h.logger)
			return
		}
		h.logger.Error("failed to delete cluster", "error", err, "account_id", accountID, "cluster_id", clusterID)
		writeAPIError(w, ErrClusterDeleteFailed, h.logger)
		return
	}

	response := map[string]any{
		"message":    "Cluster deletion initiated",
		"cluster_id": clusterID,
	}

	if err := api.Write(w, http.StatusAccepted, response); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}
