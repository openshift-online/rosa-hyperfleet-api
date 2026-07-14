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

// ManagementClusterHandler handles management cluster endpoints
type ManagementClusterHandler struct {
	maestroClient *maestro.Client
	logger        *slog.Logger
}

// NewManagementClusterHandler creates a new ManagementClusterHandler
func NewManagementClusterHandler(maestroClient *maestro.Client, logger *slog.Logger) *ManagementClusterHandler {
	return &ManagementClusterHandler{
		maestroClient: maestroClient,
		logger:        logger,
	}
}

// Create handles POST /api/v0/management_clusters
func (h *ManagementClusterHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	h.logger.Info("creating management cluster", "account_id", accountID)

	var req maestro.ConsumerCreateRequest
	if r.Body != nil && r.ContentLength > 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
			return
		}
	}

	consumer, err := h.maestroClient.CreateConsumer(ctx, &req)
	if err != nil {
		h.logger.Error("failed to create consumer in Maestro", "error", err, "account_id", accountID)
		if maestroErr, ok := err.(*maestro.Error); ok {
			h.writeError(w, http.StatusBadGateway, maestroErr.Code, maestroErr.Reason)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "maestro-error", "Failed to create management cluster")
		return
	}

	h.logger.Info("management cluster created", "id", consumer.ID, "name", consumer.Name, "account_id", accountID)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(consumer)
}

// List handles GET /api/v0/management_clusters
func (h *ManagementClusterHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	h.logger.Debug("listing management clusters", "account_id", accountID)

	page := 1
	size := 100

	if p := r.URL.Query().Get("page"); p != "" {
		if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
			page = parsed
		}
	}

	if s := r.URL.Query().Get("size"); s != "" {
		if parsed, err := strconv.Atoi(s); err == nil && parsed > 0 && parsed <= 100 {
			size = parsed
		}
	}

	list, err := h.maestroClient.ListConsumers(ctx, page, size)
	if err != nil {
		h.logger.Error("failed to list consumers from Maestro", "error", err, "account_id", accountID)
		if maestroErr, ok := err.(*maestro.Error); ok {
			h.writeError(w, http.StatusBadGateway, maestroErr.Code, maestroErr.Reason)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "maestro-error", "Failed to list management clusters")
		return
	}

	h.logger.Debug("management clusters listed", "total", list.Total, "account_id", accountID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(list)
}

// Get handles GET /api/v0/management_clusters/{id}
func (h *ManagementClusterHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	vars := mux.Vars(r)
	id := vars["id"]

	h.logger.Debug("getting management cluster", "id", id, "account_id", accountID)

	consumer, err := h.maestroClient.GetConsumer(ctx, id)
	if err != nil {
		h.logger.Error("failed to get consumer from Maestro", "error", err, "id", id, "account_id", accountID)
		if maestroErr, ok := err.(*maestro.Error); ok {
			h.writeError(w, http.StatusBadGateway, maestroErr.Code, maestroErr.Reason)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "maestro-error", "Failed to get management cluster")
		return
	}

	if consumer == nil {
		h.writeError(w, http.StatusNotFound, "not-found", "Management cluster not found")
		return
	}

	h.logger.Debug("management cluster retrieved", "id", consumer.ID, "name", consumer.Name, "account_id", accountID)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(consumer)
}

func (h *ManagementClusterHandler) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}

	_ = json.NewEncoder(w).Encode(resp)
}
