package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
)

// ResourceBundleHandler handles resource bundle endpoints
type ResourceBundleHandler struct {
	maestroClient maestro.ClientInterface
	logger        *slog.Logger
}

// NewResourceBundleHandler creates a new ResourceBundleHandler
func NewResourceBundleHandler(maestroClient maestro.ClientInterface, logger *slog.Logger) *ResourceBundleHandler {
	return &ResourceBundleHandler{
		maestroClient: maestroClient,
		logger:        logger,
	}
}

// List handles GET /api/v0/resource_bundles
func (h *ResourceBundleHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	h.logger.Debug("listing resource bundles", "account_id", accountID)

	page := 1
	size := 100

	if p := r.URL.Query().Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}

	if s := r.URL.Query().Get("size"); s != "" {
		if parsed, err := strconv.Atoi(s); err == nil && parsed > 0 {
			size = parsed
		}
	}

	search := r.URL.Query().Get("search")
	orderBy := r.URL.Query().Get("orderBy")
	fields := r.URL.Query().Get("fields")

	list, err := h.maestroClient.ListResourceBundles(ctx, page, size, search, orderBy, fields)
	if err != nil {
		h.logger.Error("failed to list resource bundles from Maestro", "error", err, "account_id", accountID)
		if maestroErr, ok := err.(*maestro.Error); ok {
			h.writeError(w, http.StatusBadGateway, maestroErr.Code, maestroErr.Reason)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "maestro-error", "Failed to list resource bundles")
		return
	}

	h.logger.Debug("resource bundles listed", "total", list.Total, "account_id", accountID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// Delete handles DELETE /api/v0/resource_bundles/{id}
func (h *ResourceBundleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	vars := mux.Vars(r)
	id := vars["id"]

	if id == "" {
		h.logger.Error("resource bundle ID is required", "account_id", accountID)
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Resource bundle ID is required")
		return
	}

	h.logger.Debug("deleting resource bundle", "id", id, "account_id", accountID)

	err := h.maestroClient.DeleteResourceBundle(ctx, id)
	if err != nil {
		h.logger.Error("failed to delete resource bundle from Maestro", "error", err, "id", id, "account_id", accountID)
		if maestroErr, ok := err.(*maestro.Error); ok {
			if maestroErr.Code == "404" {
				h.writeError(w, http.StatusNotFound, maestroErr.Code, maestroErr.Reason)
				return
			}
			h.writeError(w, http.StatusBadGateway, maestroErr.Code, maestroErr.Reason)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "maestro-error", "Failed to delete resource bundle")
		return
	}

	h.logger.Debug("resource bundle deleted", "id", id, "account_id", accountID)

	w.WriteHeader(http.StatusNoContent)
}

func (h *ResourceBundleHandler) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}

	_ = json.NewEncoder(w).Encode(resp)
}
