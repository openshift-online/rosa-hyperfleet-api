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
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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

// clusterCreateCleanupTimeout bounds a detached best-effort delete after a create failure.
const clusterCreateCleanupTimeout = 5 * time.Second

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

	req, requestErr := h.decodeClusterCreateRequest(accountID, body)
	if requestErr != nil {
		writeAPIError(w, *requestErr, h.logger)
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

	queueURL, region := requestedTerminationHandlerQueue(req.Spec.HostedCluster.Platform.AWS)
	if queueURL != "" {
		existing, err := h.db.ListClusters(ctx, accountID)
		if err != nil {
			h.logger.Error("failed to check Spot termination handler queue URL uniqueness", "error", err, "account_id", accountID)
			writeAPIError(w, ErrClusterCreateNameCheck, h.logger)
			return
		}
		if errs := validation.ValidateSpotTerminationHandlerQueueURL(queueURL, region, terminationHandlerQueueURLs(existing.Items, "")); errs != nil {
			writeAPIError(w, ErrClusterValidation.WithErrors(errs), h.logger)
			return
		}
	}

	queueClaimCreated := false
	if queueURL != "" {
		queueClaimCreated, err = h.db.ClaimSpotTerminationQueue(ctx, accountID, queueURL, req.Name)
		if err != nil {
			if hyperfleetdb.IsAlreadyExists(err) {
				writeAPIError(w, ErrClusterValidation.WithErrors(validation.SpotTerminationHandlerQueueAlreadyInUse()), h.logger)
				return
			}
			h.logger.Error("failed to claim Spot termination handler queue URL", "error", err, "account_id", accountID)
			writeAPIError(w, ErrClusterCreateFailed, h.logger)
			return
		}
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
	cr := hyperfleetdb.PublicToInternalCluster(req, accountID)
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

	if err := h.db.CreateCluster(ctx, accountID, cr); err != nil {
		if queueClaimCreated {
			if releaseErr := h.db.ReleaseSpotTerminationQueue(context.WithoutCancel(ctx), accountID, queueURL, req.Name); releaseErr != nil {
				h.logger.Warn("failed to release Spot termination handler queue claim after cluster create failure", "error", releaseErr, "account_id", accountID)
			}
		}
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
		if queueClaimCreated {
			if releaseErr := h.db.ReleaseSpotTerminationQueue(context.WithoutCancel(ctx), accountID, queueURL, req.Name); releaseErr != nil {
				h.logger.Warn("failed to release Spot termination handler queue claim after cluster create completion failure", "error", releaseErr, "account_id", accountID)
			}
		}
		writeAPIError(w, *apiErr, h.logger)
		return
	}
	if err := api.Write(w, http.StatusCreated, hyperfleetdb.InternalToPublicCluster(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

func (h *ClusterHandler) decodeClusterCreateRequest(accountID string, body []byte) (*public.Cluster, *APIError) {
	var req public.Cluster
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, &ErrClusterCreateInvalidBody
	}

	var envelope struct {
		Spec json.RawMessage `json:"spec"`
	}
	_ = json.Unmarshal(body, &envelope)
	specStr := string(envelope.Spec)
	if req.Name == "" || len(envelope.Spec) == 0 || specStr == "{}" || specStr == "null" {
		return nil, &ErrClusterCreateMissingFields
	}

	var rawSpec map[string]any
	if err := json.Unmarshal(envelope.Spec, &rawSpec); err != nil {
		return nil, &ErrClusterCreateInvalidBody
	}
	if len(rawSpec) == 0 {
		return nil, &ErrClusterCreateMissingFields
	}
	if err := validation.ValidateClusterNameForDNSReservation(req.Name, req.Spec.DNSReservationID != ""); err != nil {
		return nil, &ErrClusterCreateInvalidName
	}
	if err := validation.ValidateAccountNamespace(req.Namespace, accountID); err != nil {
		return nil, &ErrClusterCreateNamespaceMismatch
	}
	if errs := h.validator.ValidateCreate(&req.Spec, featuregate.Default); errs != nil {
		apiErr := ErrClusterValidation.WithErrors(errs)
		return nil, &apiErr
	}

	cidrValidator := validation.NewNetworkCIDRValidator()
	if errs := cidrValidator.ValidateNetworkCIDRs(&req.Spec.HostedCluster.Networking); errs != nil {
		apiErr := ErrClusterValidation.WithErrors(errs)
		return nil, &apiErr
	}

	// Validate image configuration if provided
	if req.Spec.HostedCluster.Configuration != nil && req.Spec.HostedCluster.Configuration.Image != nil {
		if err := validation.ValidateImageConfiguration(req.Spec.HostedCluster.Configuration.Image); err != nil {
			h.logger.Error("invalid image configuration", "error", err, "account_id", accountID, "cluster_name", req.Name)
			validationErr := ErrClusterValidation.WithErrors(validation.ValidationErrors{
				&validation.ValidationError{
					Field:  "spec.hostedCluster.configuration.image",
					Reason: err.Error(),
				},
			})
			return nil, &validationErr
		}
	}

	// Validate registry additional trusted CA if provided
	if len(req.Spec.RegistryAdditionalTrustedCA) > 0 {
		if err := validation.ValidateRegistryAdditionalTrustedCA(req.Spec.RegistryAdditionalTrustedCA); err != nil {
			h.logger.Error("invalid registry additional trusted CA", "error", err, "account_id", accountID, "cluster_name", req.Name)
			validationErr := ErrClusterValidation.WithErrors(validation.ValidationErrors{
				&validation.ValidationError{
					Field:  "spec.registryAdditionalTrustedCA",
					Reason: err.Error(),
				},
			})
			return nil, &validationErr
		}
	}

	return &req, nil
}

func (h *ClusterHandler) completeClusterCreate(ctx context.Context, accountID, clusterName string, cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation, oidcConfig *hyperfleetv1alpha1.OidcConfig) (*hyperfleetv1alpha1.Cluster, *APIError) {
	clusterUID := string(cluster.UID)
	deleteClusterForCleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clusterCreateCleanupTimeout)
		defer cancel()
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := h.db.GetCluster(cleanupCtx, accountID, clusterName)
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
		if err := h.updateManagedIssuerURL(ctx, accountID, clusterName, clusterUID, issuerURL); err != nil {
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
		if err := h.db.UpdateOidcConfigLastUsedTimestamp(ctx, accountID, string(oidcConfig.UID), metav1.Now()); err != nil {
			h.logger.Warn("failed to update oidc config lastUsedTimestamp", "error", err, "account_id", accountID, "oidc_config_uid", oidcConfig.UID)
		}
	}

	if latest, err := h.db.GetCluster(ctx, accountID, clusterName); err == nil {
		cluster = latest
	}
	return cluster, nil
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

// parseUpdateRequest parses and validates the cluster update request body.
func (h *ClusterHandler) parseUpdateRequest(body []byte, accountID string) (envelope struct {
	Metadata struct {
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec json.RawMessage `json:"spec"`
}, rawSpec map[string]any, apiErr *APIError) {
	if err := json.Unmarshal(body, &envelope); err != nil {
		return envelope, nil, &ErrClusterUpdateInvalidBody
	}
	if err := validation.ValidateAccountNamespace(envelope.Metadata.Namespace, accountID); err != nil {
		return envelope, nil, &ErrClusterUpdateNamespaceMismatch
	}
	if len(envelope.Spec) == 0 || string(envelope.Spec) == "{}" {
		return envelope, nil, &ErrClusterUpdateMissingFields
	}
	if err := json.Unmarshal(envelope.Spec, &rawSpec); err != nil {
		return envelope, nil, &ErrClusterUpdateInvalidBody
	}
	if len(rawSpec) == 0 {
		return envelope, nil, &ErrClusterUpdateMissingFields
	}
	return envelope, rawSpec, nil
}

// validateAndClaimSpotQueue validates and claims the Spot termination handler queue if it changed.
func (h *ClusterHandler) validateAndClaimSpotQueue(ctx context.Context, accountID, clusterName string, current *hyperfleetv1alpha1.Cluster, oldQueueURL, oldRegion string, validationErr *validation.ValidationErrors) error {
	queueURL, region := clusterTerminationHandlerQueue(current)
	queueChanged := queueURL != oldQueueURL || region != oldRegion
	if queueChanged && queueURL != "" {
		existing, err := h.db.ListClusters(ctx, accountID)
		if err != nil {
			return err
		}
		if errs := validation.ValidateSpotTerminationHandlerQueueURL(queueURL, region, terminationHandlerQueueURLs(existing.Items, current.Name)); errs != nil {
			*validationErr = errs
			return fmt.Errorf("invalid cluster update")
		}
	}
	if queueChanged && queueURL != "" {
		if _, err := h.db.ClaimSpotTerminationQueue(ctx, accountID, queueURL, clusterName); err != nil {
			if hyperfleetdb.IsAlreadyExists(err) {
				*validationErr = validation.SpotTerminationHandlerQueueAlreadyInUse()
				return fmt.Errorf("invalid cluster update")
			}
			return err
		}
	}
	return nil
}

// validateRegistryConfigAfterMerge validates image configuration and registry CA after spec merge.
func (h *ClusterHandler) validateRegistryConfigAfterMerge(current *hyperfleetv1alpha1.Cluster, validationErr *validation.ValidationErrors) error {
	publicCluster := hyperfleetdb.InternalToPublicCluster(current)
	if publicCluster.Spec.HostedCluster.Configuration != nil && publicCluster.Spec.HostedCluster.Configuration.Image != nil {
		if err := validation.ValidateImageConfiguration(publicCluster.Spec.HostedCluster.Configuration.Image); err != nil {
			*validationErr = validation.ValidationErrors{
				&validation.ValidationError{
					Field:  "spec.hostedCluster.configuration.image",
					Reason: err.Error(),
				},
			}
			return fmt.Errorf("invalid image configuration")
		}
	}
	if len(publicCluster.Spec.RegistryAdditionalTrustedCA) > 0 {
		if err := validation.ValidateRegistryAdditionalTrustedCA(publicCluster.Spec.RegistryAdditionalTrustedCA); err != nil {
			*validationErr = validation.ValidationErrors{
				&validation.ValidationError{
					Field:  "spec.registryAdditionalTrustedCA",
					Reason: err.Error(),
				},
			}
			return fmt.Errorf("invalid registry additional trusted CA")
		}
	}
	return nil
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

	envelope, rawSpec, apiErr := h.parseUpdateRequest(body, accountID)
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
	}

	h.logger.Info("updating cluster", "account_id", accountID, "cluster_name", clusterName)

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
		oldQueueURL, oldRegion := clusterTerminationHandlerQueue(current)
		if err := hyperfleetdb.MergeSpecJSON(&current.Spec, envelope.Spec); err != nil {
			mergeErr = err
			return err
		}

		if err := h.validateAndClaimSpotQueue(ctx, accountID, clusterName, current, oldQueueURL, oldRegion, &validationErr); err != nil {
			return err
		}
		if err := h.validateRegistryConfigAfterMerge(current, &validationErr); err != nil {
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

func requestedTerminationHandlerQueue(aws *hypershiftv1beta1.AWSPlatformSpec) (queueURL, region string) {
	if aws == nil {
		return "", ""
	}
	return aws.TerminationHandlerQueueURL, aws.Region
}

func clusterTerminationHandlerQueue(cluster *hyperfleetv1alpha1.Cluster) (queueURL, region string) {
	if cluster == nil {
		return "", ""
	}
	return requestedTerminationHandlerQueue(cluster.Spec.HostedCluster.Platform.AWS)
}

func terminationHandlerQueueURLs(clusters []hyperfleetv1alpha1.Cluster, excludeName string) []string {
	var queueURLs []string
	for i := range clusters {
		cluster := &clusters[i]
		if excludeName != "" && cluster.Name == excludeName {
			continue
		}
		if queueURL, _ := clusterTerminationHandlerQueue(cluster); queueURL != "" {
			queueURLs = append(queueURLs, queueURL)
		}
	}
	return queueURLs
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
