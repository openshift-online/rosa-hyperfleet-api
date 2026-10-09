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

const (
	clusterUIDLabel         = "hyperfleet.io/cluster-uid"
	clusterCleanupFinalizer = "hyperfleet.io/cluster" // keep in sync with the operator's Cluster finalizer
)

// releaseClaimTimeout bounds detached claim cleanup and rollback work so a canceled request can't skip it.
const releaseClaimTimeout = 5 * time.Second

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

	if err := validation.ValidateClusterName(req.Name); err != nil {
		writeAPIError(w, ErrClusterCreateNameTooLong, h.logger)
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

	// Set service-set fields on the internal CRD (not visible in public request/response)
	if callerARN := middleware.GetCallerARN(ctx); callerARN != "" {
		cr.Spec.CreatorARN = callerARN
	}
	// issuerURL is service-set; derive it here rather than accept it from the caller.
	if oidcConfig != nil {
		cr.Spec.HostedCluster.IssuerURL = oidcConfig.Spec.IssuerUrl
	}

	if err := h.db.CreateCluster(ctx, accountID, cr); err != nil {
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
	if oidcConfig == nil && h.oidcIssuerBaseURL != "" {
		issuerURL := strings.TrimRight(h.oidcIssuerBaseURL, "/") + "/" + clusterUID
		if err := h.updateManagedIssuerURL(ctx, accountID, clusterName, clusterUID, issuerURL); err != nil {
			h.rollbackCreatedCluster(ctx, accountID, clusterName, clusterUID)
			h.logger.Error("failed to set managed issuer URL", "error", err, "cluster_uid", clusterUID)
			return nil, &ErrClusterCreateFailed
		}
		cluster.Spec.HostedCluster.IssuerURL = issuerURL
	}

	if reservation != nil {
		if apiErr := h.claimDNSReservation(ctx, accountID, string(reservation.UID), clusterUID); apiErr != nil {
			h.rollbackCreatedCluster(ctx, accountID, clusterName, clusterUID)
			return nil, apiErr
		}
	}
	if oidcConfig != nil {
		if apiErr := h.claimOidcConfig(ctx, accountID, string(oidcConfig.UID), clusterUID); apiErr != nil {
			if reservation != nil {
				if cleanupErr := h.releaseDNSReservationClaim(ctx, accountID, string(reservation.UID), clusterUID); cleanupErr != nil {
					h.logger.Error("failed to release DNS reservation after OIDC claim conflict; registering operator cleanup before rollback", "error", cleanupErr, "reservation_uid", reservation.UID, "cluster_uid", clusterUID)
					if finalizerErr := h.ensureClusterCleanupFinalizer(ctx, accountID, clusterName, clusterUID); finalizerErr != nil {
						h.logger.Error("failed to register Cluster cleanup finalizer; leaving the Cluster in place to preserve DNS claim ownership", "error", finalizerErr, "reservation_uid", reservation.UID, "cluster_uid", clusterUID)
						return nil, &ErrClusterCreateFailed
					}
				}
			}
			h.rollbackCreatedCluster(ctx, accountID, clusterName, clusterUID)
			return nil, apiErr
		}
		if err := h.db.UpdateOidcConfigLastUsedTimestamp(ctx, accountID, string(oidcConfig.UID), metav1.Now()); err != nil {
			h.logger.Warn("failed to update oidc config lastUsedTimestamp", "error", err, "account_id", accountID, "oidc_config_uid", oidcConfig.UID)
		}
	}

	if latest, err := h.db.GetCluster(ctx, accountID, clusterName); err == nil {
		cluster = latest
	}
	return cluster, nil
}

// ensureClusterCleanupFinalizer durably registers the operator's delete cleanup
// before a failed OIDC claim can roll back a Cluster that still owns a DNS claim.
func (h *ClusterHandler) ensureClusterCleanupFinalizer(ctx context.Context, accountID, clusterName, clusterUID string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseClaimTimeout)
	defer cancel()

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster, err := h.db.GetCluster(cleanupCtx, accountID, clusterName)
		if err != nil {
			return err
		}
		if string(cluster.UID) != clusterUID {
			return fmt.Errorf("cluster UID changed from %s to %s before registering cleanup", clusterUID, cluster.UID)
		}
		for _, finalizer := range cluster.Finalizers {
			if finalizer == clusterCleanupFinalizer {
				return nil
			}
		}
		cluster.Finalizers = append(cluster.Finalizers, clusterCleanupFinalizer)
		return h.db.UpdateCluster(cleanupCtx, cluster)
	})
}

func (h *ClusterHandler) updateManagedIssuerURL(ctx context.Context, accountID, clusterName, clusterUID, issuerURL string) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster, err := h.db.GetCluster(ctx, accountID, clusterName)
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

func (h *ClusterHandler) rollbackCreatedCluster(ctx context.Context, accountID, clusterName, clusterUID string) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseClaimTimeout)
	defer cancel()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cluster, err := h.db.GetCluster(cleanupCtx, accountID, clusterName)
		if err != nil {
			if hyperfleetdb.IsNotFound(err) {
				return nil
			}
			return err
		}
		if string(cluster.UID) != clusterUID {
			return nil
		}
		return h.db.DeleteClusterObject(cleanupCtx, cluster)
	}); err != nil {
		h.logger.Error("failed to roll back Cluster create", "error", err, "cluster_uid", clusterUID)
	}
}

// Get handles GET /api/v0/clusters/{id}
func (h *ClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterName := vars["id"]

	h.logger.Info("getting cluster", "account_id", accountID, "cluster_name", clusterName)

	cr, err := h.db.GetCluster(ctx, accountID, clusterName)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster", "error", err, "account_id", accountID, "cluster_name", clusterName)
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
		writeAPIError(w, ErrClusterCreateNamespaceMismatch, h.logger)
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
	var updated *hyperfleetv1alpha1.Cluster
	var validationErr validation.ValidationErrors
	var mergeErr error
	err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
		current, err := h.db.GetCluster(ctx, accountID, clusterName)
		if err != nil {
			return err
		}
		if errs := h.validator.ValidateUpdate(rawSpec, &current.Spec, featuregate.Default); errs != nil {
			validationErr = errs
			return fmt.Errorf("invalid cluster update")
		}
		if err := hyperfleetdb.MergeSpecJSON(&current.Spec, envelope.Spec); err != nil {
			mergeErr = err
			return err
		}
		if err := h.db.UpdateCluster(ctx, current); err != nil {
			return err
		}
		updated = current
		return nil
	})
	if len(validationErr) != 0 {
		writeAPIError(w, ErrClusterValidation.WithErrors(validationErr), h.logger)
		return
	}
	if mergeErr != nil {
		h.logger.Error("failed to merge cluster spec", "error", mergeErr)
		writeAPIError(w, ErrClusterUpdateInvalidSpec, h.logger)
		return
	}
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrClusterUpdateNotFound, h.logger)
			return
		}
		if hyperfleetdb.IsConflict(err) {
			writeAPIError(w, ErrClusterUpdateConflict, h.logger)
			return
		}
		h.logger.Error("failed to update cluster", "error", err, "account_id", accountID, "cluster_name", clusterName)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicCluster(updated)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Delete handles DELETE /api/v0/clusters/{id}
func (h *ClusterHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	clusterName := vars["id"]

	h.logger.Info("deleting cluster", "account_id", accountID, "cluster_name", clusterName)

	err := h.db.DeleteCluster(ctx, accountID, clusterName)
	if err != nil {
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
