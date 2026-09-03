package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
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

// OidcConfigHandler handles OIDC config HTTP requests.
type OidcConfigHandler struct {
	db                *hyperfleetdb.Client
	oidcIssuerBaseURL string
	logger            *slog.Logger
	generateID        func() string
}

// NewOidcConfigHandler creates a new OIDC config handler.
func NewOidcConfigHandler(db *hyperfleetdb.Client, oidcIssuerBaseURL string, logger *slog.Logger) *OidcConfigHandler {
	return &OidcConfigHandler{
		db:                db,
		oidcIssuerBaseURL: oidcIssuerBaseURL,
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
		req.Spec.IssuerUrl = strings.TrimRight(h.oidcIssuerBaseURL, "/") + "/" + configID
	}

	cr := hyperfleetdb.PublicToInternalOidcConfig(&req, accountID, configID)

	if err := h.db.CreateOidcConfig(ctx, cr); err != nil {
		if hyperfleetdb.IsAlreadyExists(err) {
			writeAPIError(w, ErrOidcConfigCreateDuplicateIssuerUrl, h.logger)
			return
		}
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

	clusters, err := h.db.ListClusters(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to check oidc config usage", "error", err, "account_id", accountID, "config_id", configID)
		writeAPIError(w, ErrOidcConfigDeleteFailed, h.logger)
		return
	}
	for i := range clusters.Items {
		if clusters.Items[i].Spec.OidcConfigID == configID {
			writeAPIError(w, ErrOidcConfigDeleteInUse, h.logger)
			return
		}
	}

	if err := h.db.DeleteOidcConfig(ctx, accountID, configID); err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrOidcConfigDeleteNotFound, h.logger)
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
