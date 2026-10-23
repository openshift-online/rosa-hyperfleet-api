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

	seaplanev1alpha1 "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/internal/codegen/featuregate"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/api"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/clients/seaplanedb"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/middleware"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/validation"
)

const (
	clusterUIDLabel         = "seaplane.io/cluster-uid"
	clusterCleanupFinalizer = "seaplane.io/cluster" // keep in sync with the operator's Cluster finalizer
)

// clusterCreateCleanupTimeout bounds a detached best-effort delete after a create failure.
const clusterCreateCleanupTimeout = 5 * time.Second

// ClusterHandler handles cluster-related HTTP requests
type ClusterHandler struct {
	db                       *seaplanedb.Client
	oidcIssuerBaseURL        string
	defaultClusterExpiration time.Duration
	dnsBaseDomainSuffix      string
	validator                *validation.FieldValidator
	logger                   *slog.Logger
}

// NewClusterHandler creates a new cluster handler
func NewClusterHandler(db *seaplanedb.Client, oidcIssuerBaseURL string, defaultClusterExpiration time.Duration, logger *slog.Logger, dnsBaseDomainSuffix ...string) *ClusterHandler {
	suffix := ""
	if len(dnsBaseDomainSuffix) > 0 {
		suffix = dnsBaseDomainSuffix[0]
	}
	return &ClusterHandler{
		db:                       db,
		oidcIssuerBaseURL:        oidcIssuerBaseURL,
		defaultClusterExpiration: defaultClusterExpiration,
		dnsBaseDomainSuffix:      strings.ToLower(strings.Trim(strings.TrimSpace(suffix), ".")),
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
		clusters = append(clusters, seaplanedb.InternalToPublicCluster(&list.Items[i]))
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

	dnsReservation, apiErr := h.resolveClusterCreateDNSReservation(ctx, accountID, req)
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
	}
	oidcConfig, apiErr := h.resolveOidcConfig(ctx, accountID, req.Spec.OidcConfigID)
	if apiErr != nil {
		writeAPIError(w, *apiErr, h.logger)
		return
	}

	var queueURL, region string
	if aws := req.Spec.HostedCluster.Platform.AWS; aws != nil {
		queueURL, region = aws.TerminationHandlerQueueURL, aws.Region
	}
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
			if seaplanedb.IsAlreadyExists(err) {
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
	cr := seaplanedb.PublicToInternalCluster(req, accountID)
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
		if seaplanedb.IsAlreadyExists(err) {
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
	if err := api.Write(w, http.StatusCreated, seaplanedb.InternalToPublicCluster(cr)); err != nil {
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
	if err := validation.ValidateAccountNamespace(req.Namespace, accountID); err != nil {
		return nil, &ErrClusterCreateNamespaceMismatch
	}
	errs := h.validator.ValidateCreate(&req.Spec, featuregate.Default)
	errs = append(errs, validation.ValidateCustomerAWSTags(req.Spec.Tags, "spec.tags")...)
	errs = append(errs, validation.ValidateClusterAWSTagBudget(req.Spec.Tags)...)
	if len(errs) > 0 {
		apiErr := ErrClusterValidation.WithErrors(errs)
		return nil, &apiErr
	}

	cidrValidator := validation.NewNetworkCIDRValidator()
	if errs := cidrValidator.ValidateNetworkCIDRs(&req.Spec.HostedCluster.Networking); errs != nil {
		apiErr := ErrClusterValidation.WithErrors(errs)
		return nil, &apiErr
	}

	return &req, nil
}

func (h *ClusterHandler) completeClusterCreate(ctx context.Context, accountID, clusterName string, cluster *seaplanev1alpha1.Cluster, reservation *seaplanev1alpha1.DNSReservation, oidcConfig *seaplanev1alpha1.OidcConfig) (*seaplanev1alpha1.Cluster, *APIError) {
	clusterUID := string(cluster.UID)
	deleteClusterForCleanup := func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), clusterCreateCleanupTimeout)
		defer cancel()
		if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			current, err := h.db.GetCluster(cleanupCtx, accountID, clusterName)
			if err != nil {
				if seaplanedb.IsNotFound(err) {
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

// resolveManagedDNSDomain finds the HCP DNS-domain reservation backing a
// customer-selected base domain. Every non-generated base domain must belong to
// the regional managed suffix and be reserved by the requesting account.
func (h *ClusterHandler) resolveManagedDNSDomain(ctx context.Context, accountID, baseDomain string) (string, *APIError) {
	if baseDomain == "" {
		return "", nil
	}
	if h.dnsBaseDomainSuffix == "" {
		return "", &ErrClusterCreateDNSDomainLookupFailed
	}
	if !isManagedDNSDomain(baseDomain, h.dnsBaseDomainSuffix) {
		return "", &ErrClusterCreateDNSDomainNotFound
	}

	reservations, err := h.db.ListDNSDomainReservations(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to look up managed DNS domain", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID), "base_domain", redact(baseDomain))
		return "", &ErrClusterCreateDNSDomainLookupFailed
	}
	for i := range reservations.Items {
		reservation := &reservations.Items[i]
		if !reservation.Spec.UserDefined || reservation.Spec.ClusterArch != dnsDomainArchHCP ||
			!strings.EqualFold(strings.TrimSuffix(reservation.Spec.BaseDomain, "."), strings.TrimSuffix(baseDomain, ".")) {
			continue
		}
		if reservation.Labels[claimedByClusterUIDLabel] != "" {
			return "", &ErrClusterCreateDNSDomainInUse
		}
		if reservation.Status.Phase != seaplanev1alpha1.DNSReservationPhaseReady ||
			reservation.Status.BaseDomain == "" || reservation.Status.InternalHostedClusterName == "" {
			return "", &ErrClusterCreateDNSReservationNotReady
		}
		return string(reservation.UID), nil
	}
	return "", &ErrClusterCreateDNSDomainNotFound
}

func (h *ClusterHandler) resolveCreateDNSReservationID(ctx context.Context, accountID string, req *public.Cluster) *APIError {
	reservationUID, apiErr := h.resolveManagedDNSDomain(ctx, accountID, req.Spec.HostedCluster.DNS.BaseDomain)
	if apiErr != nil {
		return apiErr
	}
	if req.Spec.DNSReservationID != "" && reservationUID != "" && req.Spec.DNSReservationID != reservationUID {
		return &ErrClusterCreateDNSDomainMismatch
	}
	if reservationUID != "" {
		req.Spec.DNSReservationID = reservationUID
	}
	return nil
}

func (h *ClusterHandler) resolveClusterCreateDNSReservation(ctx context.Context, accountID string, req *public.Cluster) (*seaplanev1alpha1.DNSReservation, *APIError) {
	if apiErr := h.resolveCreateDNSReservationID(ctx, accountID, req); apiErr != nil {
		return nil, apiErr
	}
	if err := validation.ValidateClusterNameForDNSReservation(req.Name, req.Spec.DNSReservationID != ""); err != nil {
		return nil, &ErrClusterCreateInvalidName
	}
	if req.Spec.DNSReservationID == "" {
		return nil, nil
	}
	return h.resolveDNSReservation(ctx, accountID, req.Spec.DNSReservationID)
}

// isManagedDNSDomain reports whether domain belongs to the configured regional suffix.
func isManagedDNSDomain(domain, suffix string) bool {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	suffix = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(suffix)), ".")
	return suffix != "" && (domain == suffix || strings.HasSuffix(domain, "."+suffix))
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
		if seaplanedb.IsNotFound(err) {
			writeAPIError(w, ErrClusterGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get cluster", "error", err, "account_id", accountID, "cluster_name", clusterName)
		writeAPIError(w, ErrClusterGetFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, seaplanedb.InternalToPublicCluster(cr)); err != nil {
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

	request, ok := h.decodeClusterUpdateRequest(w, body, accountID)
	if !ok {
		return
	}

	h.logger.Info("updating cluster", "account_id", accountID, "cluster_name", clusterName)
	outcome, err := h.updateClusterWithRetry(ctx, accountID, clusterName, request)
	if len(outcome.validationErr) != 0 {
		writeAPIError(w, ErrClusterValidation.WithErrors(outcome.validationErr), h.logger)
		return
	}
	if outcome.mergeErr != nil {
		h.logger.Error("failed to merge cluster spec", "error", outcome.mergeErr)
		writeAPIError(w, ErrClusterUpdateInvalidSpec, h.logger)
		return
	}
	if err != nil {
		if seaplanedb.IsNotFound(err) {
			writeAPIError(w, ErrClusterUpdateNotFound, h.logger)
			return
		}
		if seaplanedb.IsConflict(err) {
			writeAPIError(w, ErrClusterUpdateConflict, h.logger)
			return
		}
		h.logger.Error("failed to update cluster", "error", err, "account_id", accountID, "cluster_name", clusterName)
		writeAPIError(w, ErrClusterUpdateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, seaplanedb.InternalToPublicCluster(outcome.updated)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

type clusterUpdateRequest struct {
	spec    json.RawMessage
	rawSpec map[string]any
}

type clusterUpdateOutcome struct {
	updated       *seaplanev1alpha1.Cluster
	validationErr validation.ValidationErrors
	mergeErr      error
}

func (h *ClusterHandler) decodeClusterUpdateRequest(
	w http.ResponseWriter,
	body []byte,
	accountID string,
) (clusterUpdateRequest, bool) {
	// Keep the raw spec so validation only considers fields the client sent.
	var envelope struct {
		Metadata struct {
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		writeAPIError(w, ErrClusterUpdateInvalidBody, h.logger)
		return clusterUpdateRequest{}, false
	}
	if err := validation.ValidateAccountNamespace(envelope.Metadata.Namespace, accountID); err != nil {
		writeAPIError(w, ErrClusterUpdateNamespaceMismatch, h.logger)
		return clusterUpdateRequest{}, false
	}
	if len(envelope.Spec) == 0 || string(envelope.Spec) == "{}" {
		writeAPIError(w, ErrClusterUpdateMissingFields, h.logger)
		return clusterUpdateRequest{}, false
	}

	var rawSpec map[string]any
	if err := json.Unmarshal(envelope.Spec, &rawSpec); err != nil {
		writeAPIError(w, ErrClusterUpdateInvalidBody, h.logger)
		return clusterUpdateRequest{}, false
	}
	if len(rawSpec) == 0 {
		writeAPIError(w, ErrClusterUpdateMissingFields, h.logger)
		return clusterUpdateRequest{}, false
	}
	return clusterUpdateRequest{spec: envelope.Spec, rawSpec: rawSpec}, true
}

func (h *ClusterHandler) updateClusterWithRetry(
	ctx context.Context,
	accountID, clusterName string,
	request clusterUpdateRequest,
) (clusterUpdateOutcome, error) {
	var outcome clusterUpdateOutcome
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		return h.updateClusterOnce(ctx, accountID, clusterName, request, &outcome)
	})
	return outcome, err
}

func (h *ClusterHandler) updateClusterOnce(
	ctx context.Context,
	accountID, clusterName string,
	request clusterUpdateRequest,
	outcome *clusterUpdateOutcome,
) error {
	current, err := h.db.GetCluster(ctx, accountID, clusterName)
	if err != nil {
		return err
	}
	if errs := h.validator.ValidateUpdate(request.rawSpec, &current.Spec, featuregate.Default); errs != nil {
		outcome.validationErr = errs
		return fmt.Errorf("invalid cluster update")
	}

	oldQueueURL, oldRegion := clusterTerminationHandlerQueue(current)
	if err := seaplanedb.MergeSpecJSON(&current.Spec, request.spec); err != nil {
		outcome.mergeErr = err
		return err
	}
	if clusterTagsSupplied(request.rawSpec) {
		tagErrs, err := h.validateUpdatedClusterTags(ctx, accountID, current)
		if err != nil {
			outcome.validationErr = tagErrs
			return err
		}
	}

	if errs, err := h.validateTerminationHandlerQueueUpdate(ctx, accountID, clusterName, current, oldQueueURL, oldRegion); err != nil {
		outcome.validationErr = errs
		return err
	}
	if err := h.db.UpdateCluster(ctx, current); err != nil {
		return err
	}
	outcome.updated = current
	return nil
}

func (h *ClusterHandler) validateUpdatedClusterTags(
	ctx context.Context,
	accountID string,
	cluster *seaplanev1alpha1.Cluster,
) (validation.ValidationErrors, error) {
	errs := validation.ValidateCustomerAWSTags(cluster.Spec.Tags, "spec.tags")
	errs = append(errs, validation.ValidateClusterAWSTagBudget(cluster.Spec.Tags)...)
	if len(errs) > 0 {
		return errs, fmt.Errorf("invalid cluster AWS tags")
	}

	nodePools, err := h.db.ListNodePools(ctx, accountID, string(cluster.UID))
	if err != nil {
		return nil, fmt.Errorf("list NodePools for AWS tag validation: %w", err)
	}
	for i := range nodePools.Items {
		nodePool := &nodePools.Items[i]
		if nodePool.Spec.NodePool.ClusterName != cluster.Name && !strings.HasPrefix(nodePool.Name, cluster.Name+".") {
			continue
		}
		poolErrs := validation.ValidateNodePoolAWSTagBudget(cluster.Spec.Tags, nodePool.Spec.Tags, "spec.tags")
		for _, tagErr := range poolErrs {
			tagErr.Reason = fmt.Sprintf("NodePool %q: %s", nodePool.Name, tagErr.Reason)
		}
		errs = append(errs, poolErrs...)
	}
	if len(errs) > 0 {
		return errs, fmt.Errorf("invalid cluster AWS tags for existing NodePools")
	}
	return errs, nil
}

func (h *ClusterHandler) validateTerminationHandlerQueueUpdate(
	ctx context.Context,
	accountID, clusterName string,
	cluster *seaplanev1alpha1.Cluster,
	oldQueueURL, oldRegion string,
) (validation.ValidationErrors, error) {
	queueURL, region := clusterTerminationHandlerQueue(cluster)
	if (queueURL == oldQueueURL && region == oldRegion) || queueURL == "" {
		return nil, nil
	}

	existing, err := h.db.ListClusters(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if errs := validation.ValidateSpotTerminationHandlerQueueURL(queueURL, region, terminationHandlerQueueURLs(existing.Items, cluster.Name)); errs != nil {
		return errs, fmt.Errorf("invalid cluster update")
	}
	if _, err := h.db.ClaimSpotTerminationQueue(ctx, accountID, queueURL, clusterName); err != nil {
		if seaplanedb.IsAlreadyExists(err) {
			return validation.SpotTerminationHandlerQueueAlreadyInUse(), fmt.Errorf("invalid cluster update")
		}
		return nil, err
	}
	return nil, nil
}

func clusterTerminationHandlerQueue(cluster *seaplanev1alpha1.Cluster) (queueURL, region string) {
	if cluster == nil {
		return "", ""
	}
	aws := cluster.Spec.HostedCluster.Platform.AWS
	if aws == nil {
		return "", ""
	}
	return aws.TerminationHandlerQueueURL, aws.Region
}

func terminationHandlerQueueURLs(clusters []seaplanev1alpha1.Cluster, excludeName string) []string {
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

func clusterTagsSupplied(rawSpec map[string]any) bool {
	_, exists := rawSpec["tags"]
	return exists
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
		if seaplanedb.IsNotFound(err) {
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
