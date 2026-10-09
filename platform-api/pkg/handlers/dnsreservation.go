package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"k8s.io/client-go/util/retry"

	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/validation"
)

var errDNSReservationClaimed = errors.New("DNS reservation is claimed")

// DNSReservationHandler serves the customer-facing DNS reservation API.
type DNSReservationHandler struct {
	db     *hyperfleetdb.Client
	logger *slog.Logger
}

func NewDNSReservationHandler(db *hyperfleetdb.Client, logger *slog.Logger) *DNSReservationHandler {
	return &DNSReservationHandler{db: db, logger: logger}
}

func (h *DNSReservationHandler) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	limit := parsePositiveInt(r.URL.Query().Get("limit"), 50, 100)
	offset := parseNonNegativeInt(r.URL.Query().Get("offset"))

	list, err := h.db.ListDNSReservations(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to list DNS reservations", "error", err, "account_id", accountID)
		writeAPIError(w, ErrDNSReservationListFailed, h.logger)
		return
	}

	items := make([]*public.DNSReservation, 0, len(list.Items))
	for i := range list.Items {
		items = append(items, hyperfleetdb.InternalToPublicDNSReservation(&list.Items[i]))
	}
	total := len(items)
	if offset >= len(items) {
		items = []*public.DNSReservation{}
	} else {
		end := min(offset+limit, len(items))
		items = items[offset:end]
	}

	if err := api.Write(w, http.StatusOK, map[string]any{
		"items":  items,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}); err != nil {
		h.logger.Error("failed to write DNS reservation list", "error", err)
	}
}

func (h *DNSReservationHandler) Create(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	var req public.DNSReservation
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAPIError(w, ErrDNSReservationCreateInvalidBody, h.logger)
		return
	}
	if req.Name == "" {
		writeAPIError(w, ErrDNSReservationCreateMissingName, h.logger)
		return
	}
	if err := validation.ValidateResourceName(req.Name); err != nil {
		writeAPIError(w, ErrDNSReservationCreateInvalidName, h.logger)
		return
	}
	if err := validation.ValidateAccountNamespace(req.Namespace, accountID); err != nil {
		writeAPIError(w, ErrDNSReservationCreateNamespaceMismatch, h.logger)
		return
	}

	reservation := hyperfleetdb.PublicToInternalDNSReservation(&req, accountID)
	reservation.Status.Phase = "Pending"
	if err := h.db.CreateDNSReservation(ctx, accountID, reservation); err != nil {
		if hyperfleetdb.IsAlreadyExists(err) {
			writeAPIError(w, ErrDNSReservationCreateNameConflict, h.logger)
			return
		}
		h.logger.Error("failed to create DNS reservation", "error", err, "account_id", accountID)
		writeAPIError(w, ErrDNSReservationCreateFailed, h.logger)
		return
	}
	if err := api.Write(w, http.StatusCreated, hyperfleetdb.InternalToPublicDNSReservation(reservation)); err != nil {
		h.logger.Error("failed to write DNS reservation response", "error", err)
	}
}

func (h *DNSReservationHandler) Get(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	name := mux.Vars(r)["id"]
	reservation, err := h.db.GetDNSReservation(ctx, accountID, name)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeAPIError(w, ErrDNSReservationGetNotFound, h.logger)
			return
		}
		h.logger.Error("failed to get DNS reservation", "error", err, "account_id", accountID, "name", name)
		writeAPIError(w, ErrDNSReservationGetFailed, h.logger)
		return
	}
	if err := api.Write(w, http.StatusOK, hyperfleetdb.InternalToPublicDNSReservation(reservation)); err != nil {
		h.logger.Error("failed to write DNS reservation response", "error", err)
	}
}

func (h *DNSReservationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	name := mux.Vars(r)["id"]
	var notFound bool
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		reservation, err := h.db.GetDNSReservation(ctx, accountID, name)
		if err != nil {
			if hyperfleetdb.IsNotFound(err) {
				notFound = true
			}
			return err
		}
		if reservation.Labels["hyperfleet.io/claimed-by-cluster-uid"] != "" {
			return errDNSReservationClaimed
		}
		return h.db.DeleteDNSReservationObject(ctx, reservation)
	})
	if notFound || hyperfleetdb.IsNotFound(err) {
		writeAPIError(w, ErrDNSReservationDeleteNotFound, h.logger)
		return
	}
	if errors.Is(err, errDNSReservationClaimed) {
		writeAPIError(w, ErrDNSReservationDeleteClaimed, h.logger)
		return
	}
	if err != nil {
		h.logger.Error("failed to delete DNS reservation", "error", err, "account_id", accountID, "name", name)
		writeAPIError(w, ErrDNSReservationDeleteFailed, h.logger)
		return
	}
	if err := api.Write(w, http.StatusAccepted, map[string]any{
		"message": "DNS reservation deletion initiated",
		"name":    name,
	}); err != nil {
		h.logger.Error("failed to write DNS reservation deletion response", "error", err)
	}
}

func parsePositiveInt(value string, fallback, maxValue int) int {
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || parsed > maxValue {
		return fallback
	}
	return parsed
}

func parseNonNegativeInt(value string) int {
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}
