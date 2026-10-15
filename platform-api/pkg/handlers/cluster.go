package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

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

const (
	clusterUIDLabel         = "hyperfleet.io/cluster-uid"
	clusterCleanupFinalizer = "hyperfleet.io/cluster" // keep in sync with the operator's Cluster finalizer
)

// clusterCreateCleanupTimeout bounds a detached best-effort delete after a create failure.
const clusterCreateCleanupTimeout = 5 * time.Second

// ClusterHandler handles cluster-related HTTP requests
type ClusterHandler struct {
	db                       *hyperfleetdb.Client
	oidcIssuerBaseURL        string
	defaultClusterExpiration time.Duration
	validator                *validation.FieldValidator
	logger                   *slog.Logger
	authorizer               *authz.Authorizer
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
		authorizer:               authorizer,
	}
}

// List handles GET /api/v0/clusters
func (h *ClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	prepared, attempt := authorizeCollection(w, r, h.authorizer, h.logger, authz.ListClusters, authz.Resource{
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
		attempt.Finish(authz.OutcomeError, authz.StageResourceLoading)
		writeAPIError(w, ErrClusterList, h.logger)
		return
	}

	// Every item is listed (EKS style); without Describe it shows only ID, name, and state.
	// Check all items before paging so a late failure cannot leak partial success.
	clusters := make([]*public.Cluster, 0, len(list.Items))
	for i := range list.Items {
		decision, err := prepared.Check(ctx, authz.DescribeCluster, h.clusterResource(&list.Items[i]))
		if err != nil {
			writeResourceAuthzError(w, r, attempt, authz.ListClusters, err, h.logger)
			return
		}
		c := hyperfleetdb.InternalToPublicCluster(&list.Items[i])
		if !decision.Allowed {
			c = &public.Cluster{ObjectMeta: listMeta(c.ObjectMeta), Status: public.ClusterStatus{Phase: c.Status.Phase}}
		}
		clusters = append(clusters, c)
	}
	attempt.Finish(authz.OutcomeAllow, authz.StageNone)

	total := len(clusters)
	if offset >= total {
		clusters = []*public.Cluster{}
	} else {
		end := offset + min(limit, total-offset)
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

	if err := validation.ValidateClusterNameForDNSReservation(req.Name, req.Spec.DNSReservationID != ""); err != nil {
		writeAPIError(w, ErrClusterCreateInvalidName, h.logger)
		return
	}
	if err := validation.ValidateAccountNamespace(req.Namespace, accountID); err != nil {
		writeAPIError(w, ErrClusterCreateNamespaceMismatch, h.logger)
		return
	}

	if errs := h.validator.ValidateCreate(&req.Spec, featuregate.Default); errs != nil {
		writeAPIError(w, ErrClusterValidation.WithErrors(errs), h.logger)
		return
	}
	var dnsReservation *hyperfleetv1alpha1.DNSReservation
	var apiErr *APIError
	if req.Spec.DNSReservationID != "" {
		dnsReservation, apiErr = h.resolveDNSReservation(ctx, accountID, req.Spec.DNSReservationID)
		if apiErr != nil {
			writeAPIError(w, *apiErr, h.logger)
			return
		}
	}
	oidcConfig, apiErr := h.resolveOidcConfig(ctx, accountID, req.Spec.OidcConfigID)
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
	}

	h.logger.Info("creating cluster", "account_id", accountID, "cluster_name", req.Name)

	if h.defaultClusterExpiration > 0 && req.Spec.ExpirationTimestamp == nil {
		expiry := metav1.NewTime(time.Now().Add(h.defaultClusterExpiration))
		req.Spec.ExpirationTimestamp = &expiry
	}

	req.Namespace = "account-" + accountID
	if dnsReservation != nil {
		req.Spec.DNSReservationID = string(dnsReservation.UID)
	}
	if oidcConfig != nil {
		req.Spec.OidcConfigID = string(oidcConfig.UID)
	}
	cr := hyperfleetdb.PublicToInternalCluster(&req, accountID)
	cr.Spec.HostedClusterName = req.Name
	if dnsReservation != nil {
		cr.Spec.HostedClusterName = dnsReservation.Status.InternalHostedClusterName
		if cr.Spec.HostedClusterName == "" {
			writeAPIError(w, ErrClusterCreateDNSReservationNotReady, h.logger)
			return
		}
	}
	// Persist the operator cleanup finalizer with the initial Cluster so a failed
	// post-create claim cannot delete the object before the operator can release claims.
	cr.Finalizers = []string{clusterCleanupFinalizer}

	// Set service-set fields on the internal CRD (not visible in public request/response)
	if callerARN := middleware.GetCallerARN(ctx); callerARN != "" {
		cr.Spec.CreatorARN = callerARN
	}
	// issuerURL is service-set; derive it here rather than accept it from the caller.
	if oidcConfig != nil {
		cr.Spec.HostedCluster.IssuerURL = oidcConfig.Spec.IssuerUrl
	}

	if !authorizeResource(w, r, h.authorizer, h.logger, authz.CreateCluster, candidateResource(h.clusterResource(cr), cr.Name)) {
		return
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
	cr, apiErr = h.completeClusterCreate(ctx, accountID, req.Name, cr, dnsReservation, oidcConfig)
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
	}
	if err := api.Write(w, http.StatusCreated, hyperfleetdb.InternalToPublicCluster(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

func (h *ClusterHandler) completeClusterCreate(ctx context.Context, accountID, clusterName string, cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation, oidcConfig *hyperfleetv1alpha1.OidcConfig) (*hyperfleetv1alpha1.Cluster, *APIError) {
	clusterUID := string(cluster.UID)
	deleteClusterForCleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clusterCreateCleanupTimeout)
		defer cancel()
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := h.db.GetCluster(cleanupCtx, clusterName)
			if err != nil {
				if hyperfleetdb.IsNotFound(err) {
					return nil
				}
				return err
			}
			if string(current.UID) != clusterUID {
				return nil
			}
			return h.db.DeleteClusterObject(cleanupCtx, current)
		}); err != nil {
			h.logger.Error("failed to delete Cluster after create failure; operator cleanup will retry", "error", err, "cluster_uid", clusterUID)
		}
	}

	if oidcConfig == nil && h.oidcIssuerBaseURL != "" {
		issuerURL := strings.TrimRight(h.oidcIssuerBaseURL, "/") + "/" + clusterUID
		if err := h.updateManagedIssuerURL(ctx, clusterName, clusterUID, issuerURL); err != nil {
			deleteClusterForCleanup()
			h.logger.Error("failed to set managed issuer URL", "error", err, "cluster_uid", clusterUID)
			return nil, &ErrClusterCreateFailed
		}
		cluster.Spec.HostedCluster.IssuerURL = issuerURL
	}

	if reservation != nil {
		if apiErr := h.claimDNSReservation(ctx, accountID, string(reservation.UID), clusterUID); apiErr != nil {
			deleteClusterForCleanup()
			return nil, apiErr
		}
	}
	if oidcConfig != nil {
		if apiErr := h.claimOidcConfig(ctx, accountID, string(oidcConfig.UID), clusterUID); apiErr != nil {
			deleteClusterForCleanup()
			return nil, apiErr
		}
		if err := h.db.UpdateOidcConfigLastUsedTimestamp(ctx, string(oidcConfig.UID), metav1.Now()); err != nil {
			h.logger.Warn("failed to update oidc config lastUsedTimestamp", "error", err, "account_id", accountID, "oidc_config_uid", oidcConfig.UID)
		}
	}

	if latest, err := h.db.GetCluster(ctx, clusterName); err == nil {
		cluster = latest
	}
	return cluster, nil
}
func (h *ClusterHandler) updateManagedIssuerURL(ctx context.Context, clusterName, clusterUID, issuerURL string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster, err := h.db.GetCluster(ctx, clusterName)
		if err != nil {
			return err
		}
		if string(cluster.UID) != clusterUID {
			return fmt.Errorf("cluster UID changed from %s to %s during create", clusterUID, cluster.UID)
		}
		cluster.Spec.HostedCluster.IssuerURL = issuerURL
		return h.db.UpdateCluster(ctx, cluster)
	})
}

// Get handles GET /api/v0/clusters/{id}
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterName := vars["id"]

	h.logger.Info("getting cluster", "account_id", accountID, "cluster_name", clusterName)

	attempt := authz.DefaultMetrics.Start(authz.DescribeCluster)
	prepared, err := prepareResourceAuthz(r, h.authorizer)
	if err != nil {
		writeResourceAuthzError(w, r, attempt, authz.DescribeCluster, err, h.logger)
		return
	}
	cr, err := h.db.GetCluster(ctx, clusterName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			attempt.Finish(authz.OutcomeDeny, authz.StageNone)
			writeAPIError(w, ErrClusterGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster", "error", err, "account_id", accountID, "cluster_name", clusterName)
		attempt.Finish(authz.OutcomeError, authz.StageResourceLoading)
		writeAPIError(w, ErrClusterGetFailed, h.logger)
		return
	}

	decision, err := prepared.Check(ctx, authz.DescribeCluster, h.clusterResource(cr))
	if err != nil {
		writeResourceAuthzError(w, r, attempt, authz.DescribeCluster, err, h.logger)
		return
	}
	if !decision.Allowed {
		writeAuthzDenied(w, r, attempt, authz.DescribeCluster, authz.DescribeCluster, decision, h.logger)
		return
	}
	attempt.Finish(authz.OutcomeAllow, authz.StageNone)

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
	clusterName := vars["id"]

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
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeAPIError(w, ErrClusterUpdateInvalidBody, h.logger)
		return
	}
	if err := validation.ValidateAccountNamespace(envelope.Metadata.Namespace, accountID); err != nil {
		writeAPIError(w, ErrClusterUpdateNamespaceMismatch, h.logger)
		return
	}

	// Reject absent spec (nil) and empty spec object ({}) — both are no-ops
	// that indicate a malformed request rather than a deliberate partial update.
	if len(envelope.Spec) == 0 || string(envelope.Spec) == "{}" {
		writeAPIError(w, ErrClusterUpdateMissingFields, h.logger)
		return
	}

	h.logger.Info("updating cluster", "account_id", accountID, "cluster_name", clusterName)

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
	// Retry conflicts like upstream. Each attempt re-reads, re-validates, and
	// re-authorizes, so no write uses a snapshot that was not checked.
	var updated *hyperfleetv1alpha1.Cluster
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var attemptErr error
		updated, attemptErr = h.updateOnce(w, r, clusterName, rawSpec, envelope.Spec)
		return attemptErr
	})
	if errors.Is(err, errResponded) {
		return
	}
	if err != nil {
		writeAPIError(w, ErrClusterUpdateConflict, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicCluster(updated)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// errResponded ends a retry after an attempt has already written its response.
var errResponded = errors.New("response written")

// updateOnce makes one checked update attempt. It writes its own error
// response and returns errResponded, except for a write conflict, which it
// returns unwritten so the caller can retry.
func (h *ClusterHandler) updateOnce(w http.ResponseWriter, r *http.Request, clusterName string, rawSpec map[string]any, spec json.RawMessage) (*hyperfleetv1alpha1.Cluster, error) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	cr, err := h.db.GetCluster(ctx, clusterName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterUpdateNotFound, h.logger)
			return nil, errResponded
		}
		h.logger.Error("failed to get cluster for update", "error", err, "account_id", accountID, "cluster_name", clusterName)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return nil, errResponded
	}
	if errs := h.validator.ValidateUpdate(rawSpec, &cr.Spec, featuregate.Default); errs != nil {
		writeAPIError(w, ErrClusterValidation.WithErrors(errs), h.logger)
		return nil, errResponded
	}
	candidate := cr.DeepCopy()
	if err := hyperfleetdb.MergeSpecJSON(&candidate.Spec, spec); err != nil {
		h.logger.Error("failed to merge cluster spec", "error", err)
		writeAPIError(w, ErrClusterUpdateInvalidSpec, h.logger)
		return nil, errResponded
	}
	if err := h.validator.PreserveServiceSet(&candidate.Spec, &cr.Spec, featuregate.Default); err != nil {
		h.logger.Error("failed to preserve service-set fields", "error", err, "account_id", accountID, "cluster_name", clusterName)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return nil, errResponded
	}
	// Authorize the stored snapshot and every changed-field action before its CAS write.
	if !authorizeResource(w, r, h.authorizer, h.logger, authz.UpdateCluster, h.clusterResource(cr), clusterUpdateActions(&cr.Spec, &candidate.Spec)...) {
		return nil, errResponded
	}

	if err := h.db.UpdateCluster(ctx, candidate); err != nil {
		if hyperfleetdb.IsConflict(err) {
			return nil, err
		}
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterUpdateNotFound, h.logger)
			return nil, errResponded
		}
		h.logger.Error("failed to update cluster", "error", err, "account_id", accountID, "cluster_name", clusterName)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return nil, errResponded
	}
	return candidate, nil
}

// Delete handles DELETE /api/v0/clusters/{id}
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterName := vars["id"]

	h.logger.Info("deleting cluster", "account_id", accountID, "cluster_name", clusterName)

	cr, err := h.db.GetCluster(ctx, clusterName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterDeleteNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster for delete", "error", err, "account_id", accountID, "cluster_name", clusterName)
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
		h.logger.Error("failed to delete cluster", "error", err, "account_id", accountID, "cluster_name", clusterName)
		writeAPIError(w, ErrClusterDeleteFailed, h.logger)
		return
	}

	response := map[string]any{
		"message":      "Cluster deletion initiated",
		"cluster_name": clusterName,
	}

	if err := api.Write(w, http.StatusAccepted, response); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}
