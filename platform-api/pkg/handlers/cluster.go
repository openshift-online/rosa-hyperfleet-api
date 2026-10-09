package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/util/retry"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/internal/codegen/featuregate"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/validation"
)

// clusterNamespaceLabel marks an account-scoped resource claimed by a cluster.
const clusterNamespaceLabel = hyperfleetv1alpha1.DNSReservationClusterNamespaceLabel

// releaseClaimTimeout bounds detached rollback of cluster resource claims.
const releaseClaimTimeout = 5 * time.Second

// ClusterHandler handles cluster-related HTTP requests
type ClusterHandler struct {
	db                       *hyperfleetdb.Client
	oidcIssuerBaseURL        string
	defaultClusterExpiration time.Duration
	dnsBaseDomainSuffix      string
	validator                *validation.FieldValidator
	logger                   *slog.Logger
	generateID               func() string
}

// NewClusterHandler creates a new cluster handler
func NewClusterHandler(db *hyperfleetdb.Client, oidcIssuerBaseURL string, defaultClusterExpiration time.Duration, logger *slog.Logger, dnsBaseDomainSuffix ...string) *ClusterHandler {
	suffix := ""
	if len(dnsBaseDomainSuffix) > 0 {
		suffix = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(dnsBaseDomainSuffix[0])), ".")
	}
	return &ClusterHandler{
		db:                       db,
		oidcIssuerBaseURL:        oidcIssuerBaseURL,
		defaultClusterExpiration: defaultClusterExpiration,
		dnsBaseDomainSuffix:      suffix,
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

	clusterID := h.generateID()

	h.logger.Info("creating cluster", "account_id", accountID, "cluster_name", req.Name, "cluster_id", clusterID)

	// If oidcConfigId is set, resolve and atomically claim it for clusterID (see resolveAndClaimOidcConfig); otherwise fall back to the legacy auto-generated issuerURL.
	oidcConfig, apiErr := h.resolveAndClaimOidcConfig(ctx, accountID, req.Spec.OidcConfigID, clusterID)
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
	}

	dnsReservation, apiErr := h.claimManagedDNSDomain(ctx, accountID, req.Spec.HostedCluster.DNS.BaseDomain, hyperfleetdb.ClusterNSPrefix+clusterID)
	if apiErr != nil {
		if oidcConfig != nil {
			h.releaseOidcConfigClaim(ctx, accountID, req.Spec.OidcConfigID)
		}
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

	if err := h.db.CreateCluster(ctx, accountID, cr); err != nil {
		// Release the claim taken above if it isn't left permanently bound to a cluster that was never actually created.
		if oidcConfig != nil {
			h.releaseOidcConfigClaim(ctx, accountID, req.Spec.OidcConfigID)
		}
		if dnsReservation != nil {
			if releaseErr := h.releaseDNSDomainClaim(ctx, accountID, dnsReservation.Name, hyperfleetdb.ClusterNSPrefix+clusterID); releaseErr != nil {
				h.logger.Error("failed to release DNS domain claim after cluster create failure", "error_type", fmt.Sprintf("%T", releaseErr), "account_id", redact(accountID), "dns_domain", redact(dnsReservation.Spec.BaseDomain))
			}
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
}

// claimManagedDNSDomain verifies and atomically claims domains in the regional managed suffix.
func (h *ClusterHandler) claimManagedDNSDomain(ctx context.Context, accountID, baseDomain, clusterNamespace string) (*hyperfleetv1alpha1.DNSReservation, *APIError) {
	if baseDomain == "" || h.dnsBaseDomainSuffix == "" || !isManagedDNSDomain(baseDomain, h.dnsBaseDomainSuffix) {
		return nil, nil
	}
	reservation, err := h.db.ClaimDNSDomainReservation(ctx, accountID, baseDomain, clusterNamespace)
	if err == nil {
		return reservation, nil
	}
	if hyperfleetdb.IsNotFound(err) {
		return nil, &ErrClusterCreateDNSDomainNotFound
	}
	if hyperfleetdb.IsConflict(err) {
		return nil, &ErrClusterCreateDNSDomainInUse
	}
	h.logger.Error("failed to claim managed DNS domain", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID), "base_domain", redact(baseDomain))
	return nil, &ErrClusterCreateDNSDomainLookupFailed
}

// releaseDNSDomainClaim rolls back a DNS claim if Cluster creation fails.
func (h *ClusterHandler) releaseDNSDomainClaim(ctx context.Context, accountID, reservationName, clusterNamespace string) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseClaimTimeout)
	defer cancel()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return h.db.ReleaseDNSDomainReservationClaim(releaseCtx, accountID, reservationName, clusterNamespace)
	})
}

// isManagedDNSDomain reports whether domain belongs to the configured regional suffix.
func isManagedDNSDomain(domain, suffix string) bool {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	suffix = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(suffix)), ".")
	return suffix != "" && (domain == suffix || strings.HasSuffix(domain, "."+suffix))
}

// resolveAndClaimOidcConfig validates the OidcConfig referenced by oidcConfigID and, if unclaimed, atomically claims it for clusterID via a resourceVersion-gated Update; callers must call releaseOidcConfigClaim to roll back the claim if a later step fails.
func (h *ClusterHandler) resolveAndClaimOidcConfig(ctx context.Context, accountID, oidcConfigID, clusterID string) (*hyperfleetv1alpha1.OidcConfig, *APIError) {
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

	if oidcConfig.Labels[clusterNamespaceLabel] != "" {
		err := ErrClusterCreateOidcConfigInUse.WithReason(oidcConfigID)
		return nil, &err
	}

	if oidcConfig.Labels == nil {
		oidcConfig.Labels = map[string]string{}
	}
	oidcConfig.Labels[clusterNamespaceLabel] = hyperfleetdb.ClusterNSPrefix + clusterID
	if err := h.db.UpdateOidcConfigObject(ctx, oidcConfig); err != nil {
		if hyperfleetdb.IsConflict(err) {
			// A concurrent request won the claim between our Get and Update.
			err := ErrClusterCreateOidcConfigInUse.WithReason(oidcConfigID)
			return nil, &err
		}
		h.logger.Error("failed to claim oidc config", "error", err, "account_id", accountID, "oidc_config_id", oidcConfigID)
		return nil, &ErrClusterCreateOidcConfigLookupFailed
	}

	return oidcConfig, nil
}

// releaseOidcConfigClaim removes the clusterNamespaceLabel claim from the given OidcConfig, best-effort
// so a Cluster create that failed after winning the claim doesn't leave it permanently claimed
func (h *ClusterHandler) releaseOidcConfigClaim(ctx context.Context, accountID, oidcConfigID string) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseClaimTimeout)
	defer cancel()

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		oc, err := h.db.GetOidcConfig(releaseCtx, accountID, oidcConfigID)
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
