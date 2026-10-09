package handlers

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
)

const claimedByClusterUIDLabel = "hyperfleet.io/claimed-by-cluster-uid"

// ClusterClaimableResource adapts an account-scoped resource to the common
// Cluster-claim workflow. Implementations provide lookup, readiness, persistence,
// and resource-specific API errors.
type ClusterClaimableResource interface {
	LoadByUID(context.Context) error
	RequestedUID() string
	UID() string
	ClaimedByClusterUID() string
	SetClaimedByClusterUID(string)
	ReadyForClusterClaim() bool
	Save(context.Context) error
	NotFoundAPIError() *APIError
	NotReadyAPIError() *APIError
	InUseAPIError() *APIError
}

type dnsReservationClaimableResource struct {
	db        *hyperfleetdb.Client
	accountID string
	uid       string
	resource  *hyperfleetv1alpha1.DNSReservation
}

func (r *dnsReservationClaimableResource) LoadByUID(ctx context.Context) error {
	r.resource = nil
	reservation, err := r.db.GetDNSReservationByUID(ctx, r.accountID, r.uid)
	if err != nil {
		return err
	}
	r.resource = reservation
	return nil
}

func (r *dnsReservationClaimableResource) RequestedUID() string { return r.uid }
func (r *dnsReservationClaimableResource) UID() string          { return string(r.resource.UID) }
func (r *dnsReservationClaimableResource) ClaimedByClusterUID() string {
	return r.resource.Labels[claimedByClusterUIDLabel]
}
func (r *dnsReservationClaimableResource) SetClaimedByClusterUID(uid string) {
	if r.resource.Labels == nil {
		r.resource.Labels = make(map[string]string)
	}
	r.resource.Labels[claimedByClusterUIDLabel] = uid
}
func (r *dnsReservationClaimableResource) ReadyForClusterClaim() bool {
	return r.resource.Status.Phase == hyperfleetv1alpha1.DNSReservationPhaseReady && r.resource.Status.BaseDomain != ""
}
func (r *dnsReservationClaimableResource) Save(ctx context.Context) error {
	return r.db.UpdateDNSReservation(ctx, r.resource)
}
func (r *dnsReservationClaimableResource) NotFoundAPIError() *APIError {
	return &ErrClusterCreateDNSReservationNotFound
}
func (r *dnsReservationClaimableResource) NotReadyAPIError() *APIError {
	return &ErrClusterCreateDNSReservationNotReady
}
func (r *dnsReservationClaimableResource) InUseAPIError() *APIError {
	return &ErrClusterCreateDNSReservationInUse
}

type oidcConfigClaimableResource struct {
	db        *hyperfleetdb.Client
	accountID string
	uid       string
	resource  *hyperfleetv1alpha1.OidcConfig
}

func (r *oidcConfigClaimableResource) LoadByUID(ctx context.Context) error {
	r.resource = nil
	config, err := r.db.GetOidcConfigByUID(ctx, r.accountID, r.uid)
	if err != nil {
		return err
	}
	r.resource = config
	return nil
}

func (r *oidcConfigClaimableResource) RequestedUID() string { return r.uid }
func (r *oidcConfigClaimableResource) UID() string          { return string(r.resource.UID) }
func (r *oidcConfigClaimableResource) ClaimedByClusterUID() string {
	return r.resource.Labels[claimedByClusterUIDLabel]
}
func (r *oidcConfigClaimableResource) SetClaimedByClusterUID(uid string) {
	if r.resource.Labels == nil {
		r.resource.Labels = make(map[string]string)
	}
	r.resource.Labels[claimedByClusterUIDLabel] = uid
}
func (r *oidcConfigClaimableResource) ReadyForClusterClaim() bool {
	return oidcConfigUsable(r.resource)
}
func (r *oidcConfigClaimableResource) Save(ctx context.Context) error {
	return r.db.UpdateOidcConfigObject(ctx, r.resource)
}
func (r *oidcConfigClaimableResource) NotFoundAPIError() *APIError {
	return &ErrClusterCreateOidcConfigNotFound
}
func (r *oidcConfigClaimableResource) NotReadyAPIError() *APIError {
	return &ErrClusterCreateOidcConfigNotReady
}
func (r *oidcConfigClaimableResource) InUseAPIError() *APIError {
	apiErr := ErrClusterCreateOidcConfigInUse.WithReason(r.resource.Name)
	return &apiErr
}

func (h *ClusterHandler) resolveDNSReservation(ctx context.Context, accountID, reservationUID string) (*hyperfleetv1alpha1.DNSReservation, *APIError) {
	if reservationUID == "" {
		return nil, &ErrClusterCreateDNSReservationRequired
	}
	reservation, err := h.db.GetDNSReservationByUID(ctx, accountID, reservationUID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			return nil, &ErrClusterCreateDNSReservationNotFound
		}
		h.logger.Error("failed to look up DNS reservation", "error", err, "account_id", accountID, "reservation_uid", reservationUID)
		return nil, &ErrClusterCreateFailed
	}
	if string(reservation.UID) != reservationUID {
		return nil, &ErrClusterCreateDNSReservationNotFound
	}
	if reservation.Status.Phase != hyperfleetv1alpha1.DNSReservationPhaseReady || reservation.Status.BaseDomain == "" {
		return nil, &ErrClusterCreateDNSReservationNotReady
	}
	if reservation.Labels[claimedByClusterUIDLabel] != "" {
		return nil, &ErrClusterCreateDNSReservationInUse
	}
	return reservation, nil
}

func (h *ClusterHandler) resolveOidcConfig(ctx context.Context, accountID, oidcConfigUID string) (*hyperfleetv1alpha1.OidcConfig, *APIError) {
	if oidcConfigUID == "" {
		return nil, nil
	}
	oidcConfig, err := h.db.GetOidcConfigByUID(ctx, accountID, oidcConfigUID)
	if err != nil {
		if hyperfleetdb.IsNotFound(err) {
			return nil, &ErrClusterCreateOidcConfigNotFound
		}
		h.logger.Error("failed to look up OIDC config", "error", err, "account_id", accountID, "oidc_config_uid", oidcConfigUID)
		return nil, &ErrClusterCreateOidcConfigLookupFailed
	}
	if string(oidcConfig.UID) != oidcConfigUID {
		return nil, &ErrClusterCreateOidcConfigNotFound
	}
	if !oidcConfigUsable(oidcConfig) {
		return nil, &ErrClusterCreateOidcConfigNotReady
	}
	if oidcConfig.Labels[claimedByClusterUIDLabel] != "" {
		apiErr := ErrClusterCreateOidcConfigInUse.WithReason(oidcConfig.Name)
		return nil, &apiErr
	}
	return oidcConfig, nil
}

func (h *ClusterHandler) claimDNSReservation(ctx context.Context, accountID, reservationUID, clusterUID string) *APIError {
	claimAPIError, err := claimResource(ctx, clusterUID, &dnsReservationClaimableResource{
		db:        h.db,
		accountID: accountID,
		uid:       reservationUID,
	})
	if claimAPIError != nil {
		return claimAPIError
	}
	if err != nil {
		h.logger.Error("failed to claim DNS reservation", "error", err, "account_id", accountID, "reservation_uid", reservationUID, "cluster_uid", clusterUID)
		return &ErrClusterCreateFailed
	}
	return nil
}

func (h *ClusterHandler) claimOidcConfig(ctx context.Context, accountID, oidcConfigUID, clusterUID string) *APIError {
	claimAPIError, err := claimResource(ctx, clusterUID, &oidcConfigClaimableResource{
		db:        h.db,
		accountID: accountID,
		uid:       oidcConfigUID,
	})
	if claimAPIError != nil {
		return claimAPIError
	}
	if err != nil {
		h.logger.Error("failed to claim OIDC config", "error", err, "account_id", accountID, "oidc_config_uid", oidcConfigUID, "cluster_uid", clusterUID)
		return &ErrClusterCreateOidcConfigLookupFailed
	}
	return nil
}

func claimResource(ctx context.Context, clusterUID string, resource ClusterClaimableResource) (*APIError, error) {
	var claimAPIError *APIError
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		claimAPIError = nil
		if err := resource.LoadByUID(ctx); err != nil {
			if hyperfleetdb.IsNotFound(err) {
				claimAPIError = resource.NotFoundAPIError()
				return nil
			}
			return err
		}
		if resource.UID() != resource.RequestedUID() {
			claimAPIError = resource.NotFoundAPIError()
			return nil
		}

		claimedBy := resource.ClaimedByClusterUID()
		if claimedBy == clusterUID {
			return nil
		}
		if claimedBy != "" {
			claimAPIError = resource.InUseAPIError()
			return nil
		}
		if !resource.ReadyForClusterClaim() {
			claimAPIError = resource.NotReadyAPIError()
			return nil
		}
		resource.SetClaimedByClusterUID(clusterUID)
		return resource.Save(ctx)
	})
	if claimAPIError != nil {
		return claimAPIError, nil
	}
	return nil, err
}

func (h *ClusterHandler) releaseDNSReservationClaim(ctx context.Context, accountID, reservationUID, clusterUID string) error {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), releaseClaimTimeout)
	defer cancel()
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		reservation, err := h.db.GetDNSReservationByUID(releaseCtx, accountID, reservationUID)
		if err != nil {
			return clientIgnoreNotFound(err)
		}
		if reservation.Labels[claimedByClusterUIDLabel] != clusterUID {
			return nil
		}
		delete(reservation.Labels, claimedByClusterUIDLabel)
		return h.db.UpdateDNSReservation(releaseCtx, reservation)
	})
}

func oidcConfigUsable(oidcConfig *hyperfleetv1alpha1.OidcConfig) bool {
	if oidcConfig.Status.Phase == hyperfleetv1alpha1.OidcConfigPhaseError {
		return false
	}
	return oidcConfig.Spec.Type != hyperfleetv1alpha1.OidcConfigTypeUnmanaged ||
		oidcConfig.Status.Phase == hyperfleetv1alpha1.OidcConfigPhaseReady
}

func clientIgnoreNotFound(err error) error {
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
