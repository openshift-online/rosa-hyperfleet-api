package handlers

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/pagination"
)

// OidcConfigHandler handles OIDC config HTTP requests.
type OidcConfigHandler struct {
	db         *hyperfleetdb.Client
	logger     *slog.Logger
	generateID func() string
}

// NewOidcConfigHandler creates a new OIDC config handler.
func NewOidcConfigHandler(db *hyperfleetdb.Client, logger *slog.Logger) *OidcConfigHandler {
	return &OidcConfigHandler{
		db:         db,
		logger:     logger,
		generateID: func() string { return uuid.New().String() },
	}
}

// List handles GET /api/v0/oidc_configs
func (h *OidcConfigHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	pageOpts := pagination.ParseOptions(r)

	h.logger.Info("listing oidc configs", "account_id", redact(accountID), "limit", pageOpts.Limit)

	list, err := h.db.ListOidcConfigs(ctx, hyperfleetdb.ListOptions{
		AccountID: accountID,
		Options:   pageOpts,
	})
	if err != nil {
		if pagination.IsInvalidCursor(err) {
			writeAPIError(w, ErrOidcConfigListInvalidCursor, h.logger)
			return
		}
		h.logger.Error("failed to list oidc configs", "error", err, "account_id", redact(accountID))
		writeAPIError(w, ErrOidcConfigList, h.logger)
		return
	}

	configs := make([]*public.OidcConfig, 0, len(list.Items))
	for i := range list.Items {
		configs = append(configs, hyperfleetdb.InternalToPublicOidcConfig(&list.Items[i]))
	}

	if err := api.Write(w, http.StatusOK, pagination.Response[*public.OidcConfig]{
		ListMeta: metav1.ListMeta{Continue: list.Continue},
		Items:    configs,
		Limit:    pageOpts.Limit,
	}); err != nil {
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
		req.Spec.IssuerUrl = ""
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

	cr := hyperfleetdb.PublicToInternalOidcConfig(&req, accountID, configID)

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
