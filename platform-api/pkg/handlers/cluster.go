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

// clusterNamespaceLabel is set on an OidcConfig when a cluster claims it via claimOidcConfig, enforcing the 1:1 cluster-to-OidcConfig binding (mirrors hyperfleet-operator's cluster_controller.go constant).
const clusterNamespaceLabel = "hyperfleet.io/cluster-namespace"

// releaseClaimTimeout bounds releaseOidcConfigClaim's detached rollback so a canceled request can't skip it.
const releaseClaimTimeout = 5 * time.Second

// ClusterHandler handles cluster-related HTTP requests
type ClusterHandler struct {
	db                       *hyperfleetdb.Client
	oidcIssuerBaseURL        string
	defaultClusterExpiration time.Duration
	validator                *validation.FieldValidator
	logger                   *slog.Logger
	generateID               func() string
	authorizer               *authz.Authorizer
	metrics                  *authz.Metrics
}

// NewClusterHandler creates a new cluster handler
func NewClusterHandler(db *hyperfleetdb.Client, oidcIssuerBaseURL string, defaultClusterExpiration time.Duration, authorizer *authz.Authorizer, logger *slog.Logger) *ClusterHandler {
	if authorizer == nil {
		panic("cluster authorizer is required")
	}
	return &ClusterHandler{
		db:                       db,
		oidcIssuerBaseURL:        oidcIssuerBaseURL,
		defaultClusterExpiration: defaultClusterExpiration,
		validator:                validation.NewFieldValidator("Cluster"),
		logger:                   logger,
		generateID:               func() string { return uuid.New().String() },
		authorizer:               authorizer,
		metrics:                  authz.DefaultMetrics,
	}
}

// List handles GET /api/v0/clusters
func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	prepared, attempt := authorizeCollection(w, r, h.authorizer, h.metrics, h.logger, authz.ListClusters, authz.Resource{
		Kind: authz.Collection, CollectionKind: authz.Cluster, AccountID: accountID,
	})
	if prepared == nil {
		return
	}

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

	list, err := h.db.ListClusters(ctx)
	if err != nil {
		h.logger.Error("failed to list clusters", "error", err, "account_id", accountID)
		_ = attempt.Finish(authz.OutcomeError, authz.StageResourceLoading)
		writeAPIError(w, ErrClusterList, h.logger)
		return
	}

	// Check every candidate before paging so a late failure cannot leak partial success.
	visible := make([]*hyperfleetv1alpha1.Cluster, 0, len(list.Items))
	for i := range list.Items {
		cr := &list.Items[i]
		decision, err := prepared.Check(ctx, authz.DescribeCluster, h.clusterResource(cr))
		if err != nil {
			writeResourceAuthzError(w, r, attempt, authz.ListClusters, err, h.logger)
			return
		}
		if decision.Allowed {
			visible = append(visible, cr)
		}
	}
	_ = attempt.Finish(authz.OutcomeAllow, authz.StageNone)

	total := len(visible)
	if offset >= total {
		visible = nil
	} else {
		end := offset + min(limit, total-offset)
		visible = visible[offset:end]
	}
	clusters := make([]*public.Cluster, 0, len(visible))
	for _, cr := range visible {
		clusters = append(clusters, hyperfleetdb.InternalToPublicCluster(cr))
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

	if len(req.Name) > hyperfleetdb.MaxClusterNameLen {
		writeAPIError(w, ErrClusterCreateNameTooLong, h.logger)
		return
	}

	if errs := h.validator.ValidateCreate(&req.Spec, featuregate.Default); errs != nil {
		writeAPIError(w, ErrClusterValidation.WithErrors(errs), h.logger)
		return
	}

	existing, err := h.db.ListClusters(ctx)
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

	clusterID := h.generateID()

	h.logger.Info("creating cluster", "account_id", accountID, "cluster_name", req.Name, "cluster_id", clusterID)

	// Read readiness before candidate authorization; no claim writes are allowed yet.
	oidcConfig, apiErr := h.resolveOidcConfig(ctx, accountID, req.Spec.OidcConfigID)
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
	}

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

	if !authorizeResource(w, r, h.authorizer, h.logger, authz.CreateCluster, h.clusterResource(cr)) {
		return
	}
	if oidcConfig != nil {
		if apiErr := h.claimOidcConfig(ctx, accountID, req.Spec.OidcConfigID, clusterID, oidcConfig); apiErr != nil {
			writeAPIError(w, *apiErr, h.logger)
			return
		}
	}

	if err := h.db.CreateCluster(ctx, cr); err != nil {
		// Release the claim taken above if it isn't left permanently bound to a cluster that was never actually created.
		if oidcConfig != nil {
			h.releaseOidcConfigClaim(ctx, accountID, req.Spec.OidcConfigID)
		}
		h.logger.Error("failed to create cluster", "error", err, "account_id", accountID)
		writeAPIError(w, ErrClusterCreateFailed, h.logger)
		return
	}

	if oidcConfig != nil {
		if err := h.db.UpdateOidcConfigLastUsedTimestamp(ctx, req.Spec.OidcConfigID, metav1.Now()); err != nil {
			h.logger.Warn("failed to update oidc config lastUsedTimestamp", "error", err, "account_id", accountID, "oidc_config_id", req.Spec.OidcConfigID)
		}
	}

	if err := api.Write(w, http.StatusCreated, hyperfleetdb.InternalToPublicCluster(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// resolveOidcConfig validates the account-scoped lifecycle reference without writing.
func (h *ClusterHandler) resolveOidcConfig(ctx context.Context, accountID, oidcConfigID string) (*hyperfleetv1alpha1.OidcConfig, *APIError) {
	if oidcConfigID == "" {
		return nil, nil
	}

	oidcConfig, err := h.db.GetOidcConfig(ctx, oidcConfigID)
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

	if oidcConfig.Labels[clusterNamespaceLabel] != "" {
		err := ErrClusterCreateOidcConfigInUse.WithReason(oidcConfigID)
		return nil, &err
	}

	return oidcConfig, nil
}

// claimOidcConfig CAS-writes the readiness snapshot only after CreateCluster allows.
func (h *ClusterHandler) claimOidcConfig(ctx context.Context, accountID, oidcConfigID, clusterID string, oidcConfig *hyperfleetv1alpha1.OidcConfig) *APIError {
	if oidcConfig.Labels == nil {
		oidcConfig.Labels = map[string]string{}
	}
	oidcConfig.Labels[clusterNamespaceLabel] = hyperfleetdb.ClusterNSPrefix + clusterID
	if err := h.db.UpdateOidcConfigObject(ctx, oidcConfig); err != nil {
		if hyperfleetdb.IsConflict(err) {
			// A concurrent request won the claim between our Get and Update.
			err := ErrClusterCreateOidcConfigInUse.WithReason(oidcConfigID)
			return &err
		}
		h.logger.Error("failed to claim oidc config", "error", err, "account_id", accountID, "oidc_config_id", oidcConfigID)
		return &ErrClusterCreateOidcConfigLookupFailed
	}

	return nil
}

// releaseOidcConfigClaim removes the clusterNamespaceLabel claim from the given OidcConfig, best-effort
// so a Cluster create that failed after winning the claim doesn't leave it permanently claimed
func (h *ClusterHandler) releaseOidcConfigClaim(ctx context.Context, accountID, oidcConfigID string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseClaimTimeout)
	defer cancel()

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		oc, err := h.db.GetOidcConfig(releaseCtx, oidcConfigID)
		if err != nil {
			return err
		}
		if oc.Labels == nil {
			return nil
		}
		delete(oc.Labels, clusterNamespaceLabel)
		return h.db.UpdateOidcConfigObject(releaseCtx, oc)
	})
	if err != nil {
		h.logger.Error("failed to release oidc config claim", "error", err, "account_id", accountID, "oidc_config_id", oidcConfigID)
	}
}

// Get handles GET /api/v0/clusters/{id}
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterID := vars["id"]

	h.logger.Info("getting cluster", "account_id", accountID, "cluster_id", clusterID)

	attempt, _ := h.metrics.Start(authz.DescribeCluster)
	prepared, err := prepareResourceAuthz(r, h.authorizer)
	if err != nil {
		writeResourceAuthzError(w, r, attempt, authz.DescribeCluster, err, h.logger)
		return
	}
	cr, err := h.db.GetCluster(ctx, clusterID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			_ = attempt.Finish(authz.OutcomeDeny, authz.StageNone)
			writeAPIError(w, ErrClusterGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster", "error", err, "account_id", accountID, "cluster_id", clusterID)
		_ = attempt.Finish(authz.OutcomeError, authz.StageResourceLoading)
		writeAPIError(w, ErrClusterGetFailed, h.logger)
		return
	}

	decision, err := prepared.Check(ctx, authz.DescribeCluster, h.clusterResource(cr))
	if err != nil {
		writeResourceAuthzError(w, r, attempt, authz.DescribeCluster, err, h.logger)
		return
	}
	if !decision.Allowed {
		_ = attempt.Finish(authz.OutcomeDeny, authz.StageNone)
		writeAPIError(w, errClusterAuthzDenied, h.logger)
		return
	}
	_ = attempt.Finish(authz.OutcomeAllow, authz.StageNone)

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

	h.logger.Info("updating cluster", "account_id", accountID, "cluster_id", clusterID)

	cr, err := h.db.GetCluster(ctx, clusterID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterUpdateNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster for update", "error", err, "account_id", accountID, "cluster_id", clusterID)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return
	}

	// Decode only to reject semantically empty specs before typed update analysis.
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
	candidate := cr.DeepCopy()
	if err := hyperfleetdb.MergeSpecJSON(&candidate.Spec, envelope.Spec); err != nil {
		h.logger.Error("failed to merge cluster spec", "error", err)
		writeAPIError(w, ErrClusterUpdateInvalidSpec, h.logger)
		return
	}
	actions, errs := h.validator.AnalyzeUpdate(envelope.Spec, &cr.Spec, &candidate.Spec, featuregate.Default)
	if errs != nil {
		writeAPIError(w, ErrClusterValidation.WithErrors(errs), h.logger)
		return
	}
	if !authorizeResource(w, r, h.authorizer, h.logger, authz.UpdateCluster, h.clusterResource(cr), actions...) {
		return
	}

	if err := h.db.UpdateCluster(ctx, candidate); err != nil {
		if hyperfleetdb.IsConflict(err) {
			writeAPIError(w, ErrResourceConflict, h.logger)
			return
		}
		h.logger.Error("failed to update cluster", "error", err, "account_id", accountID, "cluster_id", clusterID)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicCluster(candidate)); err != nil {
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

	cr, err := h.db.GetCluster(ctx, clusterID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterDeleteNotFound, h.logger)
			return
		}
		writeAPIError(w, ErrClusterDeleteFailed, h.logger)
		return
	}
	if !authorizeResource(w, r, h.authorizer, h.logger, authz.DeleteCluster, h.clusterResource(cr)) {
		return
	}
	err = h.db.DeleteClusterObject(ctx, cr)
	if err != nil {
		if hyperfleetdb.IsConflict(err) {
			writeAPIError(w, ErrResourceConflict, h.logger)
			return
		}
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
