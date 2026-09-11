package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

// maxIssuerURLLength bounds customer-supplied unmanaged issuer URLs.
const maxIssuerURLLength = 2048

// normalizeIssuerURL validates raw as an absolute https URL and returns its canonical form, so two
// spellings of the same issuer never reserve two Index entries.
func normalizeIssuerURL(raw string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("must not be empty")
	}
	if len(raw) > maxIssuerURLLength {
		return "", fmt.Errorf("must be at most %d characters", maxIssuerURLLength)
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f {
			return "", fmt.Errorf("must not contain control characters or whitespace")
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("not a valid URL")
	}
	if u.Scheme != "https" || u.Host == "" || u.Opaque != "" {
		return "", fmt.Errorf("must be an absolute https URL")
	}
	if u.User != nil {
		return "", fmt.Errorf("must not contain userinfo")
	}
	host := strings.ToLower(u.Hostname())
	if port := u.Port(); port != "" && port != "443" {
		host = host + ":" + port
	}

	path := strings.TrimRight(u.EscapedPath(), "/")
	return "https://" + host + path, nil
}

// OidcConfigHandler handles OIDC config HTTP requests.
type OidcConfigHandler struct {
	db                *hyperfleetdb.Client
	oidcIssuerBaseURL string
	// region is embedded in auto-generated managed issuerUrls so two regions never mint the same one.
	region     string
	logger     *slog.Logger
	generateID func() string
}

// NewOidcConfigHandler creates a new OIDC config handler.
func NewOidcConfigHandler(db *hyperfleetdb.Client, oidcIssuerBaseURL string, region string, logger *slog.Logger) *OidcConfigHandler {
	return &OidcConfigHandler{
		db:                db,
		oidcIssuerBaseURL: oidcIssuerBaseURL,
		region:            region,
		logger:            logger,
		generateID:        func() string { return uuid.New().String() },
	}
}

// List handles GET /api/v0/oidc_configs
func (h *OidcConfigHandler) List(w http.ResponseWriter, r *http.Request) {
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

	h.logger.Info("listing oidc configs", "account_id", accountID, "limit", limit, "offset", offset)

	list, err := h.db.ListOidcConfigs(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to list oidc configs", "error", err, "account_id", accountID)
		writeAPIError(w, ErrOidcConfigList, h.logger)
		return
	}

	configs := make([]*public.OidcConfig, 0, len(list.Items))
	for i := range list.Items {
		configs = append(configs, hyperfleetdb.InternalToPublicOidcConfig(&list.Items[i]))
	}

	total := len(configs)

	if offset >= len(configs) {
		configs = []*public.OidcConfig{}
	} else {
		end := min(offset+limit, len(configs))
		configs = configs[offset:end]
	}

	response := map[string]any{
		"items":  configs,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}

	if err := api.Write(w, http.StatusOK, response); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Create handles POST /api/v0/oidc_configs
// Request body: public.OidcConfig (K8s-native). Spec.Type is required.
func (h *OidcConfigHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, ErrOidcConfigCreateInvalidBody, h.logger)
		return
	}

	var req public.OidcConfig
	if err := json.Unmarshal(body, &req); err != nil {
		writeAPIError(w, ErrOidcConfigCreateInvalidBody, h.logger)
		return
	}

	// Reject absent spec (nil) and empty spec object ({}) — spec.type is required.
	var envelope struct {
		Spec json.RawMessage `json:"spec"`
	}
	_ = json.Unmarshal(body, &envelope)
	specStr := string(envelope.Spec)
	if len(envelope.Spec) == 0 || specStr == "{}" || specStr == "null" {
		writeAPIError(w, ErrOidcConfigCreateMissingFields, h.logger)
		return
	}
	// Reject semantically empty specs (nil, empty decoded maps, whitespace variants).
	var rawSpec map[string]any
	if err := json.Unmarshal(envelope.Spec, &rawSpec); err != nil {
		writeAPIError(w, ErrOidcConfigCreateInvalidBody, h.logger)
		return
	}
	if len(rawSpec) == 0 {
		writeAPIError(w, ErrOidcConfigCreateMissingFields, h.logger)
		return
	}

	if req.Spec.Type == "" {
		writeAPIError(w, ErrOidcConfigCreateMissingFields, h.logger)
		return
	}

	if req.Spec.Type != hyperfleetv1alpha1.OidcConfigTypeManaged && req.Spec.Type != hyperfleetv1alpha1.OidcConfigTypeUnmanaged {
		writeAPIError(w, ErrOidcConfigCreateInvalidType, h.logger)
		return
	}

	if req.Spec.Type == hyperfleetv1alpha1.OidcConfigTypeManaged {
		if req.Spec.SecretArn != "" || req.Spec.InstallerRoleArn != "" {
			writeAPIError(w, ErrOidcConfigCreateInvalidFields, h.logger)
			return
		}
	} else if req.Spec.SecretArn == "" || req.Spec.InstallerRoleArn == "" || req.Spec.IssuerUrl == "" {
		writeAPIError(w, ErrOidcConfigCreateInvalidFields, h.logger)
		return
	}

	configID := h.generateID()
	h.logger.Info("creating oidc config", "account_id", accountID, "config_id", configID, "type", req.Spec.Type)

	if req.Spec.Type == hyperfleetv1alpha1.OidcConfigTypeManaged {
		if h.oidcIssuerBaseURL == "" {
			h.logger.Error("oidc issuer base URL is not configured; refusing to create managed config with a path-only issuerUrl", "account_id", accountID, "config_id", configID)
			writeAPIError(w, ErrOidcConfigCreateIssuerNotConfigured, h.logger)
			return
		}
		base := strings.TrimRight(h.oidcIssuerBaseURL, "/")
		// Region is fused into configID's own path segment, not a separate one: HyperShift's InfraID
		// (and thus its S3 upload key) is derived from this URL's trailing segment alone.
		segment := configID
		if h.region != "" {
			segment = h.region + "-" + configID
		}
		// Server-generated from a UUID; normalization below is a defensive no-op, not a real gate.
		req.Spec.IssuerUrl = base + "/" + segment
	}

	normalizedIssuerURL, err := normalizeIssuerURL(req.Spec.IssuerUrl)
	if err != nil {
		h.logger.Error("invalid issuer URL", "error", err, "account_id", accountID, "config_id", configID)
		writeAPIError(w, ErrOidcConfigCreateInvalidIssuerUrl.WithReason(err.Error()), h.logger)
		return
	}
	req.Spec.IssuerUrl = normalizedIssuerURL

	indexName := hyperfleetv1alpha1.IssuerURLIndexName(normalizedIssuerURL)

	// Fast-path check (best-effort, NOT atomic): reserveIssuerURLIndex is the real enforcement point.
	if _, err := h.db.GetOidcIssuerIndex(ctx, indexName); err == nil {
		writeAPIError(w, ErrOidcConfigCreateDuplicateIssuerUrl, h.logger)
		return
	} else if !hyperfleetdb.IsNotFound(err) {
		h.logger.Warn("fast-path issuer URL uniqueness check failed; falling through to async reservation", "error", err, "account_id", accountID, "config_id", configID)
	}

	cr := hyperfleetdb.PublicToInternalOidcConfig(&req, accountID, configID)

	// Anti-spoofing: strip any client-supplied clusterNamespaceLabel before persisting.
	delete(cr.Labels, clusterNamespaceLabel)

	// indexRef is service-set: platform-api computes it, but OidcConfigReconciler creates the Index.
	cr.Spec.IndexRef = hyperfleetv1alpha1.IndexRef{
		Namespace: hyperfleetv1alpha1.OidcIssuerReservationsNamespace,
		Name:      indexName,
	}

	if err := h.db.CreateOidcConfig(ctx, cr); err != nil {
		h.logger.Error("failed to create oidc config", "error", err, "account_id", accountID)
		writeAPIError(w, ErrOidcConfigCreateFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusCreated, hyperfleetdb.InternalToPublicOidcConfig(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Get handles GET /api/v0/oidc_configs/{id}
func (h *OidcConfigHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	configID := mux.Vars(r)["id"]

	h.logger.Info("getting oidc config", "account_id", accountID, "config_id", configID)

	cr, err := h.db.GetOidcConfig(ctx, accountID, configID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrOidcConfigGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get oidc config", "error", err, "account_id", accountID, "config_id", configID)
		writeAPIError(w, ErrOidcConfigGetFailed, h.logger)
		return
	}

	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicOidcConfig(cr)); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}

// Delete handles DELETE /api/v0/oidc_configs/{id}
func (h *OidcConfigHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	configID := mux.Vars(r)["id"]

	h.logger.Info("deleting oidc config", "account_id", accountID, "config_id", configID)

	// Fetch first (capturing ResourceVersion) so the in-use check and delete are CAS'd against the same object.
	oc, err := h.db.GetOidcConfig(ctx, accountID, configID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrOidcConfigDeleteNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get oidc config for delete", "error", err, "account_id", accountID, "config_id", configID)
		writeAPIError(w, ErrOidcConfigDeleteFailed, h.logger)
		return
	}

	if oc.Labels[clusterNamespaceLabel] != "" {
		writeAPIError(w, ErrOidcConfigDeleteInUse, h.logger)
		return
	}

	if err := h.db.DeleteOidcConfigObject(ctx, oc); err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrOidcConfigDeleteNotFound, h.logger)
			return
		}
		if hyperfleetdb.IsConflict(err) {
			// A concurrent cluster create claimed this config between our Get and Delete above; return 409 instead of 500.
			writeAPIError(w, ErrOidcConfigDeleteInUse, h.logger)
			return
		}
		h.logger.Error("failed to delete oidc config", "error", err, "account_id", accountID, "config_id", configID)
		writeAPIError(w, ErrOidcConfigDeleteFailed, h.logger)
		return
	}

	response := map[string]any{
		"message":   "OIDC config deletion initiated",
		"config_id": configID,
	}

	if err := api.Write(w, http.StatusAccepted, response); err != nil {
		h.logger.Error("failed to write response", "error", err)
	}
}
