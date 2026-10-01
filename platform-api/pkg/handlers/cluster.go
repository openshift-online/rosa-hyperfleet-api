package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/validation"
)

// rollbackTimeout bounds the detached rollback of a cluster whose OidcConfig claim was lost, so a canceled request can't skip it.
const rollbackTimeout = 5 * time.Second

// ClusterHandler handles cluster-related HTTP requests
type ClusterHandler struct {
	db                       *hyperfleetdb.Client
	oidcIssuerBaseURL        string
	defaultClusterExpiration time.Duration
	validator                *validation.FieldValidator
	logger                   *slog.Logger
}

// NewClusterHandler creates a new cluster handler
func NewClusterHandler(db *hyperfleetdb.Client, oidcIssuerBaseURL string, defaultClusterExpiration time.Duration, logger *slog.Logger) *ClusterHandler {
	return &ClusterHandler{
		db:                       db,
		oidcIssuerBaseURL:        oidcIssuerBaseURL,
		defaultClusterExpiration: defaultClusterExpiration,
		validator:                validation.NewFieldValidator("Cluster"),
		logger:                   logger,
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

	sel, err := labelSelector(r)
	if err != nil {
		writeAPIError(w, ErrClusterListInvalidSelector.WithReason(err.Error()), h.logger)
		return
	}

	h.logger.Info("listing clusters", "account_id", accountID, "limit", limit, "offset", offset)

	list, err := h.db.ListClusters(ctx, accountID, sel)
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
	// Reject semantically empty specs (nil, empty decoded maps, whitespace variants).
	var rawSpec map[string]any
	if err := json.Unmarshal(envelope.Spec, &rawSpec); err != nil {
		writeAPIError(w, ErrClusterCreateInvalidBody, h.logger)
		return
	}
	if len(rawSpec) == 0 {
		writeAPIError(w, ErrClusterCreateMissingFields, h.logger)
		return
	}

	namespace, err := resolveNamespace(req.Namespace, accountID)
	if err != nil {
		writeAPIError(w, ErrClusterCreateForbiddenNamespace.WithReason(err.Error()), h.logger)
		return
	}
	if err := hyperfleetv1alpha1.ValidateClusterName(req.Name); err != nil {
		writeAPIError(w, ErrClusterCreateInvalidName.WithReason(err.Error()), h.logger)
		return
	}
	if err := validateClientMetadata(req.Labels, req.Annotations); err != nil {
		writeAPIError(w, ErrClusterCreateInvalidMetadata.WithReason(err.Error()), h.logger)
		return
	}

	if errs := h.validator.ValidateCreate(&req.Spec, featuregate.Default); errs != nil {
		writeAPIError(w, ErrClusterValidation.WithErrors(errs), h.logger)
		return
	}

	h.logger.Info("creating cluster", "account_id", accountID, "cluster_name", req.Name)

	// If oidcConfigId is set, check the OidcConfig up front; it is claimed once the
	// cluster exists, because the claim records the cluster's database-minted uid.
	// Otherwise fall back to the legacy auto-generated issuerURL.
	oidcConfig, apiErr := h.resolveOidcConfig(ctx, accountID, req.Spec.OidcConfigID)
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
	}

	if h.defaultClusterExpiration > 0 && req.Spec.ExpirationTimestamp == nil {
		expiry := metav1.NewTime(time.Now().Add(h.defaultClusterExpiration))
		req.Spec.ExpirationTimestamp = &expiry
	}

	cr := hyperfleetdb.PublicToInternalCluster(&req, accountID)
	cr.Namespace = namespace

	// Set service-set fields on the internal CRD (not visible in public request/response)
	if callerARN := middleware.GetCallerARN(ctx); callerARN != "" {
		cr.Spec.CreatorARN = callerARN
	}
	// issuerURL is service-set; derive it here rather than accept it from the caller.
	// The legacy issuer's last path segment becomes the HostedCluster InfraID (the
	// S3 prefix HyperShift uploads discovery documents to), so it must be unique.
	if oidcConfig != nil {
		cr.Spec.HostedCluster.IssuerURL = oidcConfig.Spec.IssuerUrl
	} else if h.oidcIssuerBaseURL != "" {
		cr.Spec.HostedCluster.IssuerURL = h.oidcIssuerBaseURL + "/" + uuid.New().String()
	}

	if err := h.db.CreateCluster(ctx, cr); err != nil {
		if hyperfleetdb.IsAlreadyExists(err) {
			writeAPIError(w, ErrClusterCreateNameConflict.WithReason(req.Name), h.logger)
			return
		}
		h.logger.Error("failed to create cluster", "error", err, "account_id", accountID)
		writeAPIError(w, ErrClusterCreateFailed, h.logger)
		return
	}

	if oidcConfig != nil {
		if apiErr := h.claimOidcConfig(ctx, accountID, req.Spec.OidcConfigID, cr.UID); apiErr != nil {
			// The cluster must not outlive a claim it lost; its finalizer only
			// releases a claim carrying its own uid, so the winner's is untouched.
			h.rollbackCluster(ctx, cr)
			writeAPIError(w, *apiErr, h.logger)
			return
		}
		if err := h.db.UpdateOidcConfigLastUsedTimestamp(ctx, accountID, req.Spec.OidcConfigID, metav1.Now()); err != nil {
			h.logger.Warn("failed to update oidc config lastUsedTimestamp", "error", err, "account_id", accountID, "oidc_config_id", req.Spec.OidcConfigID)
		}
	}

	h.logger.Info("created cluster", "account_id", accountID, "cluster_name", cr.Name, "cluster_uid", cr.UID)
	if err := api.Write(w, http.StatusCreated, hyperfleetdb.InternalToPublicCluster(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// resolveOidcConfig returns the OidcConfig referenced by oidcConfigID after
// checking it is ready and not yet claimed. It does not claim it: see claimOidcConfig.
func (h *ClusterHandler) resolveOidcConfig(ctx context.Context, accountID, oidcConfigID string) (*hyperfleetv1alpha1.OidcConfig, *APIError) {
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

	if oidcConfig.Labels[hyperfleetv1alpha1.ClaimedByClusterUIDLabel] != "" {
		err := ErrClusterCreateOidcConfigInUse.WithReason(oidcConfigID)
		return nil, &err
	}
	return oidcConfig, nil
}

// claimOidcConfig binds the OidcConfig to the cluster with clusterUID by setting
// its claimed-by-cluster-uid label, enforcing the 1:1 cluster-to-OidcConfig binding.
// Each attempt is a CAS update, so of two concurrent claims exactly one wins.
func (h *ClusterHandler) claimOidcConfig(ctx context.Context, accountID, oidcConfigID string, clusterUID types.UID) *APIError {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		oc, err := h.db.GetOidcConfig(ctx, accountID, oidcConfigID)
		if err != nil {
			return err
		}
		switch oc.Labels[hyperfleetv1alpha1.ClaimedByClusterUIDLabel] {
		case string(clusterUID):
			return nil
		case "":
		default:
			return errOidcConfigClaimed
		}
		if oc.Labels == nil {
			oc.Labels = map[string]string{}
		}
		oc.Labels[hyperfleetv1alpha1.ClaimedByClusterUIDLabel] = string(clusterUID)
		return h.db.UpdateOidcConfigObject(ctx, oc)
	})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, errOidcConfigClaimed):
		apiErr := ErrClusterCreateOidcConfigInUse.WithReason(oidcConfigID)
		return &apiErr
	case hyperfleetdb.IsNotFound(err):
		return &ErrClusterCreateOidcConfigNotFound
	default:
		h.logger.Error("failed to claim oidc config", "error", err, "account_id", accountID, "oidc_config_id", oidcConfigID)
		return &ErrClusterCreateOidcConfigLookupFailed
	}
}

var errOidcConfigClaimed = errors.New("oidc config is claimed by another cluster")

// rollbackCluster deletes a just-created cluster, best-effort, on a context
// detached from the request so a canceled request can't skip it.
func (h *ClusterHandler) rollbackCluster(ctx context.Context, cr *hyperfleetv1alpha1.Cluster) {
	rollbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	if err := h.db.DeleteClusterObject(rollbackCtx, cr); err != nil && !hyperfleetdb.IsNotFound(err) {
		h.logger.Error("failed to roll back cluster", "error", err, "namespace", cr.Namespace, "cluster_name", cr.Name, "cluster_uid", cr.UID)
	}
}

// Get handles GET /api/v0/clusters/{name}
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	name := mux.Vars(r)["name"]

	h.logger.Info("getting cluster", "account_id", accountID, "cluster_name", name)

	cr, err := h.db.GetCluster(ctx, accountID, name)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster", "error", err, "account_id", accountID, "cluster_name", name)
		writeAPIError(w, ErrClusterGetFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicCluster(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Update handles PUT/PATCH /api/v0/clusters/{name}
// Request body: public.Cluster (K8s-native). Only spec fields are merged; metadata is ignored.
func (h *ClusterHandler) Update(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	name := mux.Vars(r)["name"]

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, ErrClusterUpdateInvalidBody, h.logger)
		return
	}

	// Extract raw "spec" JSON first so we know which fields the caller actually
	// sent. This is used for both the empty-spec guard and field-presence-aware
	// validation (avoids false positives from zero-valued absent fields in a
	// decoded struct).
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

	h.logger.Info("updating cluster", "account_id", accountID, "cluster_name", name)

	cr, err := h.db.GetCluster(ctx, accountID, name)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterUpdateNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster for update", "error", err, "account_id", accountID, "cluster_name", name)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return
	}

	// Validate using the raw spec map so only fields the client actually sent
	// are checked — avoids false positives from zero-valued absent fields when
	// the body is decoded into a full struct.
	var rawSpec map[string]any
	if err := json.Unmarshal(envelope.Spec, &rawSpec); err != nil {
		writeAPIError(w, ErrClusterUpdateInvalidBody, h.logger)
		return
	}
	// Reject semantically empty specs (nil, empty decoded maps, whitespace variants).
	if len(rawSpec) == 0 {
		writeAPIError(w, ErrClusterUpdateMissingFields, h.logger)
		return
	}
	if errs := h.validator.ValidateUpdate(rawSpec, &cr.Spec, featuregate.Default); errs != nil {
		writeAPIError(w, ErrClusterValidation.WithErrors(errs), h.logger)
		return
	}
	if err := hyperfleetdb.MergeSpecJSON(&cr.Spec, envelope.Spec); err != nil {
		h.logger.Error("failed to merge cluster spec", "error", err)
		writeAPIError(w, ErrClusterUpdateInvalidSpec, h.logger)
		return
	}

	if err := h.db.UpdateCluster(ctx, cr); err != nil {
		h.logger.Error("failed to update cluster", "error", err, "account_id", accountID, "cluster_name", name)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicCluster(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Delete handles DELETE /api/v0/clusters/{name}
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	name := mux.Vars(r)["name"]

	h.logger.Info("deleting cluster", "account_id", accountID, "cluster_name", name)

	err := h.db.DeleteCluster(ctx, accountID, name)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterDeleteNotFound, h.logger)
			return
		}
		h.logger.Error("failed to delete cluster", "error", err, "account_id", accountID, "cluster_name", name)
		writeAPIError(w, ErrClusterDeleteFailed, h.logger)
		return
	}

	response := map[string]any{
		"message": "Cluster deletion initiated",
		"name":    name,
	}

	if err := api.Write(w, http.StatusAccepted, response); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}
