package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/openshift/rosa-hyperfleet-api/pkg/clients/maestro"
	"github.com/openshift/rosa-hyperfleet-api/pkg/middleware"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	workv1 "open-cluster-management.io/api/work/v1"
)

// WorkHandler handles work/manifestwork endpoints
type WorkHandler struct {
	maestroClient maestro.ClientInterface
	logger        *slog.Logger
}

// NewWorkHandler creates a new WorkHandler
func NewWorkHandler(maestroClient maestro.ClientInterface, logger *slog.Logger) *WorkHandler {
	return &WorkHandler{
		maestroClient: maestroClient,
		logger:        logger,
	}
}

// WorkRequest represents the request payload for creating manifestwork
type WorkRequest struct {
	ClusterID string                 `json:"cluster_id"`
	Data      map[string]interface{} `json:"data"`
}

// Create handles POST /api/v0/work
func (h *WorkHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)

	h.logger.Info("received work creation request", "account_id", accountID)

	// Parse request body
	var req WorkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		h.logger.Error("failed to decode request body", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusBadRequest, "invalid-request", "Invalid request body")
		return
	}

	// Validate cluster_id
	if req.ClusterID == "" {
		h.logger.Error("missing cluster_id in request", "account_id", accountID)
		h.writeError(w, http.StatusBadRequest, "missing-cluster-id", "cluster_id is required")
		return
	}

	// Validate data payload
	if req.Data == nil {
		h.logger.Error("missing data in request", "account_id", accountID)
		h.writeError(w, http.StatusBadRequest, "missing-data", "data payload is required")
		return
	}

	// Log the received data
	h.logger.Info("processing manifestwork creation",
		"cluster_id", req.ClusterID,
		"account_id", accountID,
	)

	// Convert the data map to JSON and then unmarshal into ManifestWork
	dataBytes, err := json.Marshal(req.Data)
	if err != nil {
		h.logger.Error("failed to marshal data payload", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusBadRequest, "invalid-data", "Failed to process data payload")
		return
	}

	// Create a scheme and decoder for ManifestWork
	scheme := runtime.NewScheme()
	_ = workv1.Install(scheme)
	decoder := serializer.NewCodecFactory(scheme).UniversalDeserializer()

	// Decode the ManifestWork from the data payload
	obj, _, err := decoder.Decode(dataBytes, nil, nil)
	if err != nil {
		h.logger.Error("failed to decode manifestwork from data", "error", err, "account_id", accountID)
		h.writeError(w, http.StatusBadRequest, "invalid-manifestwork", "Failed to decode ManifestWork from data payload")
		return
	}

	manifestWork, ok := obj.(*workv1.ManifestWork)
	if !ok {
		h.logger.Error("data payload is not a valid ManifestWork", "account_id", accountID)
		h.writeError(w, http.StatusBadRequest, "invalid-manifestwork-type", "Data payload must be a ManifestWork object")
		return
	}

	// Ensure the namespace matches the cluster_id
	manifestWork.Namespace = req.ClusterID

	// Create the ManifestWork via gRPC
	result, err := h.maestroClient.CreateManifestWork(ctx, req.ClusterID, manifestWork)
	if err != nil {
		h.logger.Error("failed to create manifestwork", "error", err, "cluster_id", req.ClusterID, "account_id", accountID)
		if maestroErr, ok := err.(*maestro.Error); ok {
			h.writeError(w, http.StatusBadGateway, maestroErr.Code, maestroErr.Reason)
			return
		}
		h.writeError(w, http.StatusInternalServerError, "manifestwork-creation-failed", "Failed to create manifestwork")
		return
	}

	// Build response
	response := map[string]interface{}{
		"id":         string(result.UID),
		"kind":       "ManifestWork",
		"href":       "/api/v0/work/" + result.Name,
		"cluster_id": req.ClusterID,
		"name":       result.Name,
		"status":     result.Status,
	}

	h.logger.Info("manifestwork created successfully",
		"cluster_id", req.ClusterID,
		"work_name", result.Name,
		"account_id", accountID,
	)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(response)
}

func (h *WorkHandler) writeError(w http.ResponseWriter, status int, code, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	resp := map[string]interface{}{
		"kind":   "Error",
		"code":   code,
		"reason": reason,
	}

	_ = json.NewEncoder(w).Encode(resp)
}
