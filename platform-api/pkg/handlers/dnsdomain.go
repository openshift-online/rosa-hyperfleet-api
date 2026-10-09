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

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

const (
	dnsDomainArchHCP               = "hcp"
	dnsDomainShard                 = "0"
	dnsDomainShardNamespace        = "dns-shard-0-reservations"
	dnsDomainUserLabel             = "hyperfleet.io/user-defined-dns-domain"
	dnsDomainGeneratedPrefixLength = 8 + 1 + len(dnsDomainShard) + 1
	dnsMaxNameLength               = 253
)

// DNSDomainHandler serves account-scoped HCP DNS-domain reservation operations.
type DNSDomainHandler struct {
	db               *hyperfleetdb.Client
	baseDomainSuffix string
	logger           *slog.Logger
}

// NewDNSDomainHandler creates the HCP DNS-domain handler for an account API.
func NewDNSDomainHandler(db *hyperfleetdb.Client, baseDomainSuffix string, logger *slog.Logger) *DNSDomainHandler {
	return &DNSDomainHandler{db: db, baseDomainSuffix: strings.ToLower(strings.Trim(strings.TrimSpace(baseDomainSuffix), ".")), logger: logger}
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
	if problems := utilvalidation.IsDNS1123Subdomain(h.baseDomainSuffix); len(problems) != 0 || len(h.baseDomainSuffix)+dnsDomainGeneratedPrefixLength > dnsMaxNameLength {
		writeDNSDomainError(w, http.StatusServiceUnavailable, "DNSDOMAINS-CREATE-001", "DNS domain suffix is not configured", h.logger)
		return
	}
	var request struct {
		ClusterArch       string `json:"cluster_arch"`
		LegacyClusterArch string `json:"clusterArch"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeDNSDomainError(w, http.StatusBadRequest, "DNSDOMAINS-CREATE-002", "Invalid request body", h.logger)
		return
	}
	architecture := request.ClusterArch
	if architecture == "" {
		architecture = request.LegacyClusterArch
	}
	if architecture != "" && architecture != dnsDomainArchHCP {
		writeDNSDomainError(w, http.StatusBadRequest, "DNSDOMAINS-CREATE-003", "Only HCP DNS domains are supported", h.logger)
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
		index := &hyperfleetv1alpha1.Index{
			ObjectMeta: metav1.ObjectMeta{
				Name:      prefix,
				Namespace: dnsDomainShardNamespace,
			},
			Spec: hyperfleetv1alpha1.IndexSpec{},
		}
		if err := h.db.CreateDNSDomainIndex(r.Context(), accountID, index); err != nil {
			if hyperfleetdb.IsAlreadyExists(err) {
				continue
			}
			h.logger.Error("failed to reserve DNS domain prefix", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID))
			writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-CREATE-004", "Failed to create DNS domain", h.logger)
			return
		}

		reservedAt := metav1.NewTime(time.Now().UTC())
		reservation := &hyperfleetv1alpha1.DNSReservation{
			ObjectMeta: metav1.ObjectMeta{
				Name:   dnsDomainShard + "-" + prefix,
				Labels: map[string]string{dnsDomainUserLabel: "true"},
			},
			Spec: hyperfleetv1alpha1.DNSReservationSpec{
				IndexRef:    hyperfleetv1alpha1.IndexRef{Namespace: dnsDomainShardNamespace, Name: prefix},
				BaseDomain:  baseDomain,
				ClusterArch: dnsDomainArchHCP,
				UserDefined: true,
				ReservedAt:  reservedAt,
			},
		}
		if err := h.db.CreateDNSDomainReservation(r.Context(), accountID, reservation); err != nil {
			cleanupErr := h.db.DeleteDNSDomainIndex(r.Context(), dnsDomainShardNamespace, prefix)
			if hyperfleetdb.IsAlreadyExists(err) && cleanupErr == nil {
				continue
			}
			h.logger.Error("failed to store DNS domain reservation", "error_type", fmt.Sprintf("%T", err), "cleanup_error_type", fmt.Sprintf("%T", cleanupErr), "account_id", redact(accountID))
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
	var reservation *hyperfleetv1alpha1.DNSReservation
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

	clusters, err := h.db.ListClusters(ctx, accountID)
	if err != nil {
		h.logger.Error("failed to check DNS domain usage", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID), "domain_id", redact(domainID))
		writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-DELETE-001", "Failed to delete DNS domain", h.logger)
		return
	}
	for i := range clusters.Items {
		if strings.EqualFold(strings.TrimSuffix(clusters.Items[i].Spec.HostedCluster.DNS.BaseDomain, "."), strings.TrimSuffix(domainID, ".")) {
			writeDNSDomainError(w, http.StatusConflict, "DNSDOMAINS-DELETE-003", "DNS domain is in use by a cluster", h.logger)
			return
		}
	}

	reservation, err = h.db.BeginDeleteDNSDomainReservation(ctx, accountID, reservation.Name)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			writeDNSDomainError(w, http.StatusNotFound, "DNSDOMAINS-DELETE-002", "DNS domain not found", h.logger)
			return
		}
		if hyperfleetdb.IsConflict(err) {
			writeDNSDomainError(w, http.StatusConflict, "DNSDOMAINS-DELETE-003", "DNS domain is in use by a cluster", h.logger)
			return
		}
		h.logger.Error("failed to fence DNS domain from new claims", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID), "domain_id", redact(domainID))
		writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-DELETE-001", "Failed to delete DNS domain", h.logger)
		return
	}

	// Release the unique index first. The reservation remains as a deletion
	// record if this fails, so retries can find it and finish cleanup.
	if err := h.db.DeleteDNSDomainIndex(ctx, reservation.Spec.IndexRef.Namespace, reservation.Spec.IndexRef.Name); err != nil && !hyperfleetdb.IsNotFound(err) {
		h.logger.Error("failed to release DNS domain index", "error_type", fmt.Sprintf("%T", err), "account_id", redact(accountID), "domain_id", redact(domainID))
		writeDNSDomainError(w, http.StatusInternalServerError, "DNSDOMAINS-DELETE-001", "Failed to release DNS domain reservation", h.logger)
		return
	}
	if err := h.db.DeleteDNSDomainReservation(ctx, accountID, reservation.Name); err != nil && !hyperfleetdb.IsNotFound(err) {
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
