package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"

	seaplanev1alpha1 "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/api"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/clients/seaplanedb"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/middleware"
)

const (
	dnsDomainArchHCP               = "hcp"
	dnsDomainShard                 = "0"
	dnsDomainShardNamespace        = "dns-shard-0-reservations"
	dnsDomainGeneratedPrefixLength = 8 + 1 + len(dnsDomainShard) + 1
	dnsMaxNameLength               = 253
)

// DNSDomainHandler serves account-scoped HCP DNS-domain reservation operations.
type DNSDomainHandler struct {
	db               *seaplanedb.Client
	baseDomainSuffix string
	logger           *slog.Logger
}

// NewDNSDomainHandler creates the HCP DNS-domain handler for an account API.
func NewDNSDomainHandler(db *seaplanedb.Client, baseDomainSuffix string, logger *slog.Logger) *DNSDomainHandler {
	return &DNSDomainHandler{db: db, baseDomainSuffix: normalizeDNSBaseDomainSuffix(baseDomainSuffix), logger: logger}
}

func normalizeDNSBaseDomainSuffix(baseDomainSuffix string) string {
	return strings.ToLower(strings.Trim(strings.TrimSpace(baseDomainSuffix), "."))
}

// ValidateDNSBaseDomainSuffix checks the regional configuration required to
// generate account-owned HCP DNS-domain reservations.
func ValidateDNSBaseDomainSuffix(baseDomainSuffix string) error {
	normalized := normalizeDNSBaseDomainSuffix(baseDomainSuffix)
	if normalized == "" {
		return fmt.Errorf("DNS base domain suffix is required")
	}
	if problems := utilvalidation.IsDNS1123Subdomain(normalized); len(problems) != 0 {
		return fmt.Errorf("invalid DNS base domain suffix %q: %v", baseDomainSuffix, problems)
	}
	if len(normalized)+dnsDomainGeneratedPrefixLength > dnsMaxNameLength {
		return fmt.Errorf("DNS base domain suffix %q leaves insufficient room for a generated domain", baseDomainSuffix)
	}
	return nil
}

type dnsDomainResponse struct {
	Kind                string    `json:"kind"`
	ID                  string    `json:"id"`
	ClusterArch         string    `json:"cluster_arch"`
	UserDefined         bool      `json:"user_defined"`
	ReservedAtTimestamp time.Time `json:"reserved_at_timestamp"`
}

type dnsDomainListResponse struct {
	Kind  string              `json:"kind"`
	Page  int                 `json:"page"`
	Size  int                 `json:"size"`
	Total int                 `json:"total"`
	Items []dnsDomainResponse `json:"items"`
}

// List handles GET /api/v0/dns_domains and the OCM-compatible DNS domain path.
func (h *DNSDomainHandler) List(w http.ResponseWriter, r *http.Request) {
	accountID := middleware.GetAccountID(r.Context())
	reservations, err := h.db.ListDNSDomainReservations(r.Context(), accountID)
	if err != nil {
		h.logger.Error("failed to list DNS domains", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID))
		writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-LIST-001", "Failed to list DNS domains", h.logger)
		return
	}

	items := make([]dnsDomainResponse, 0, len(reservations.Items))
	for i := range reservations.Items {
		reservation := &reservations.Items[i]
		if !reservation.Spec.UserDefined || reservation.Spec.ClusterArch != dnsDomainArchHCP {
			continue
		}
		items = append(items, dnsDomainResponse{
			Kind:                "DNSDomain",
			ID:                  reservation.Spec.BaseDomain,
			ClusterArch:         reservation.Spec.ClusterArch,
			UserDefined:         true,
			ReservedAtTimestamp: reservation.Spec.ReservedAt.Time,
		})
	}
	response := dnsDomainListResponse{Kind: "DNSDomainList", Page: 1, Size: len(items), Total: len(items), Items: items}
	if err := api.Write(w, http.StatusOK, response); err != nil {
		h.logger.Error("failed to write DNS domain list", "error_type", fmt.Sprintf("%T", err))
	}
}

// Create handles POST /api/v0/dns_domains and the OCM-compatible DNS domain path.
func (h *DNSDomainHandler) Create(w http.ResponseWriter, r *http.Request) {
	accountID := middleware.GetAccountID(r.Context())
	if err := ValidateDNSBaseDomainSuffix(h.baseDomainSuffix); err != nil {
		h.logger.Error("DNS domain suffix configuration is invalid", "error", err)
		writeDNSDomainError(w, http.StatusServiceUnavailable, "DNSDOMAINS-CREATE-001", "DNS domain suffix is not configured", h.logger)
		return
	}
	var request struct{}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeDNSDomainError(w, http.StatusBadRequest, "DNSDOMAINS-CREATE-002", "Invalid request body", h.logger)
		return
	}

	for range 5 {
		prefix, err := newDNSDomainPrefix()
		if err != nil {
			h.logger.Error("failed to generate DNS domain prefix", "error_type", fmt.Sprintf("%T", err))
			writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-CREATE-004", "Failed to create DNS domain", h.logger)
			return
		}
		baseDomain := fmt.Sprintf("%s.%s.%s", prefix, dnsDomainShard, h.baseDomainSuffix)
		if len(baseDomain) > dnsMaxNameLength || len(utilvalidation.IsDNS1123Subdomain(baseDomain)) != 0 {
			h.logger.Error("generated DNS domain is invalid", "account_id", redact(accountID))
			writeDNSDomainError(w, http.StatusServiceUnavailable, "DNSDOMAINS-CREATE-001", "DNS domain suffix is not configured", h.logger)
			return
		}
		index := &seaplanev1alpha1.Index{
			ObjectMeta: metav1.ObjectMeta{
				Name:      prefix,
				Namespace: dnsDomainShardNamespace,
			},
			Spec: seaplanev1alpha1.IndexSpec{},
		}
		if err := h.db.CreateDNSDomainIndex(r.Context(), accountID, index); err != nil {
			if seaplanedb.IsAlreadyExists(err) {
				continue
			}
			h.logger.Error("failed to reserve DNS domain prefix", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID))
			writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-CREATE-004", "Failed to create DNS domain", h.logger)
			return
		}

		reservedAt := metav1.NewTime(time.Now().UTC())
		reservation := &seaplanev1alpha1.DNSReservation{
			ObjectMeta: metav1.ObjectMeta{
				Name: dnsDomainShard + "-" + prefix,
			},
			Spec: seaplanev1alpha1.DNSReservationSpec{
				ClusterDeletionPolicy: seaplanev1alpha1.DNSReservationClusterDeletionPolicyRetain,
				BaseDomain:            baseDomain,
				ClusterArch:           dnsDomainArchHCP,
				UserDefined:           true,
				ReservedAt:            reservedAt,
			},
		}
		if err := h.db.CreateDNSDomainReservation(r.Context(), accountID, reservation); err != nil {
			cleanupErr := h.db.DeleteDNSDomainIndex(r.Context(), dnsDomainShardNamespace, prefix)
			if seaplanedb.IsAlreadyExists(err) && cleanupErr == nil {
				continue
			}
			h.logger.Error("failed to store DNS domain reservation", "error_type", fmt.Sprintf("%T", err), "cleanup_error_type", fmt.Sprintf("%T", cleanupErr), "account_id", redact(accountID))
			writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-CREATE-004", "Failed to create DNS domain", h.logger)
			return
		}

		if reservation.UID == "" {
			cleanupErr := h.db.DeleteDNSDomainIndex(r.Context(), dnsDomainShardNamespace, prefix)
			h.logger.Error("DNS domain reservation has no database UID", "error_type", fmt.Sprintf("%T", err), "cleanup_error_type", fmt.Sprintf("%T", cleanupErr), "account_id", redact(accountID))
			writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-CREATE-004", "Failed to create DNS domain", h.logger)
			return
		}
		if err := h.db.SetDNSDomainIndexOwner(r.Context(), dnsDomainShardNamespace, prefix, string(reservation.UID)); err != nil {
			cleanupErr := h.db.DeleteDNSDomainReservation(r.Context(), accountID, reservation.Name)
			if seaplanedb.IsNotFound(cleanupErr) {
				cleanupErr = nil
			}
			if cleanupErr == nil {
				cleanupErr = h.db.DeleteDNSDomainIndex(r.Context(), dnsDomainShardNamespace, prefix)
			}
			h.logger.Error("failed to associate DNS domain index", "error_type", fmt.Sprintf("%T", err), "cleanup_error_type", fmt.Sprintf("%T", cleanupErr), "account_id", redact(accountID))
			writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-CREATE-004", "Failed to create DNS domain", h.logger)
			return
		}
		reservation.Status.Phase = seaplanev1alpha1.DNSReservationPhaseReady
		reservation.Status.BaseDomain = baseDomain
		reservation.Status.InternalHostedClusterName = seaplanev1alpha1.HostedClusterNameFromDNSReservation(reservation)
		if reservation.Status.InternalHostedClusterName == "" {
			cleanupErr := h.db.DeleteDNSDomainReservation(r.Context(), accountID, reservation.Name)
			if seaplanedb.IsNotFound(cleanupErr) {
				cleanupErr = nil
			}
			if cleanupErr == nil {
				cleanupErr = h.db.DeleteDNSDomainIndex(r.Context(), dnsDomainShardNamespace, prefix)
			}
			h.logger.Error("failed to derive internal HostedCluster name", "error_type", fmt.Sprintf("%T", err), "cleanup_error_type", fmt.Sprintf("%T", cleanupErr), "account_id", redact(accountID))
			writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-CREATE-004", "Failed to create DNS domain", h.logger)
			return
		}
		if err := h.db.UpdateDNSDomainReservationStatus(r.Context(), reservation); err != nil {
			h.logger.Error("failed to mark DNS domain reservation ready", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID))
			writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-CREATE-004", "Failed to create DNS domain", h.logger)
			return
		}

		response := dnsDomainResponse{
			Kind:                "DNSDomain",
			ID:                  baseDomain,
			ClusterArch:         dnsDomainArchHCP,
			UserDefined:         true,
			ReservedAtTimestamp: reservedAt.Time,
		}
		if err := api.Write(w, http.StatusCreated, response); err != nil {
			h.logger.Error("failed to write DNS domain response", "error_type", fmt.Sprintf("%T", err))
		}
		return
	}
	writeDNSDomainError(w, http.StatusConflict, "DNSDOMAINS-CREATE-005", "Unable to reserve a unique DNS domain", h.logger)
}

// Delete handles DELETE /api/v0/dns_domains/{id} and the OCM-compatible path.
func (h *DNSDomainHandler) Delete(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accountID := middleware.GetAccountID(ctx)
	domainID := mux.Vars(r)["id"]
	reservations, err := h.db.ListDNSDomainReservations(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to list DNS domains for delete", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID))
		writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-DELETE-001", "Failed to delete DNS domain", h.logger)
		return
	}
	var reservation *seaplanev1alpha1.DNSReservation
	for i := range reservations.Items {
		candidate := &reservations.Items[i]
		if candidate.Spec.BaseDomain == domainID && candidate.Spec.UserDefined && candidate.Spec.ClusterArch == dnsDomainArchHCP {
			reservation = candidate
			break
		}
	}
	if reservation == nil {
		writeDNSDomainError(w, http.StatusNotFound, "DNSDOMAINS-DELETE-002", "DNS domain not found", h.logger)
		return
	}

	err = h.db.DeleteDNSDomainReservationIfUnclaimed(ctx, accountID, reservation.Name)
	if err != nil {
		if seaplanedb.IsNotFound(err) {
			writeDNSDomainError(w, http.StatusNotFound, "DNSDOMAINS-DELETE-002", "DNS domain not found", h.logger)
			return
		}
		if seaplanedb.IsConflict(err) {
			writeDNSDomainError(w, http.StatusConflict, "DNSDOMAINS-DELETE-003", "DNS domain is in use by a cluster", h.logger)
			return
		}
		h.logger.Error("failed to delete DNS domain reservation", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID), "domain_id", redact(domainID))
		writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-DELETE-001", "Failed to delete DNS domain reservation", h.logger)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// newDNSDomainPrefix generates the random shard prefix used in a DNS domain ID.
func newDNSDomainPrefix() (string, error) {
	bytes := make([]byte, 4)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("crypto/rand: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

// writeDNSDomainError writes a Kubernetes-style status response for DNS errors.
func writeDNSDomainError(w http.ResponseWriter, status int, code, message string, logger *slog.Logger) {
	if err := api.WriteError(w, APIError{Code: code, HTTPStatus: status, Message: message}); err != nil {
		logger.Error("failed to write DNS domain error", "error_type", fmt.Sprintf("%T", err), "code", code)
	}
}
