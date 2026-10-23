package seaplanedb

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	seaplanedb "github.com/openshift-online/rosa-seaplane-api/seaplane-db"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	seaplanev1alpha1 "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1"
)

const accountIDLabel = "seaplane.io/account-id"
const clusterUIDLabel = "seaplane.io/cluster-uid"
const claimedByClusterUIDLabel = "seaplane.io/claimed-by-cluster-uid"
const ownerUIDLabel = "seaplane.io/owner-uid"

const spotQueueClaimTimeout = 10 * time.Minute
const spotQueueOwnerPrefix = "cluster-"

// Client wraps a pgruntime client.Client for CRUD on seaplane resources.
type Client struct {
	client client.Client
	close  func()
	logger *slog.Logger
}

// NewClient creates a Client backed by seaplanedb.NewClient.
// The direct client is never sharded and sees all data.
func NewClient(ctx context.Context, dsn string, logger *slog.Logger) (*Client, error) {
	scheme := runtime.NewScheme()
	if err := seaplanev1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register seaplane scheme: %w", err)
	}

	c, cleanup, err := seaplanedb.NewClient(seaplanedb.Options{
		Scheme: scheme,
		DSN:    dsn,
	})
	if err != nil {
		return nil, fmt.Errorf("create pgruntime client: %w", err)
	}

	return &Client{client: c, close: cleanup, logger: logger}, nil
}

// NewClientFrom wraps an existing client.Client (useful for testing with fakes).
func NewClientFrom(c client.Client, logger *slog.Logger) *Client {
	return &Client{client: c, close: func() {}, logger: logger}
}

// Close releases the underlying connection pool.
func (c *Client) Close() {
	c.close()
}

// --- Cluster operations ---

// CreateCluster creates a Cluster in the caller's account namespace.
func (c *Client) CreateCluster(ctx context.Context, accountID string, cluster *seaplanev1alpha1.Cluster) error {
	cluster.Namespace = accountNamespace(accountID)
	setAccountLabel(cluster, accountID)
	return c.client.Create(ctx, cluster)
}

// GetCluster retrieves a Cluster by its account-scoped metadata.name.
func (c *Client) GetCluster(ctx context.Context, accountID, name string) (*seaplanev1alpha1.Cluster, error) {
	var cluster seaplanev1alpha1.Cluster
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: accountNamespace(accountID),
		Name:      name,
	}, &cluster)
	if err != nil {
		return nil, err
	}
	return &cluster, nil
}

// ListClusters lists Clusters in the caller's account namespace.
func (c *Client) ListClusters(ctx context.Context, accountID string) (*seaplanev1alpha1.ClusterList, error) {
	var list seaplanev1alpha1.ClusterList
	err := c.client.List(ctx, &list, client.InNamespace(accountNamespace(accountID)))
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// UpdateCluster updates the spec of an existing Cluster via CAS.
func (c *Client) UpdateCluster(ctx context.Context, cluster *seaplanev1alpha1.Cluster) error {
	return c.client.Update(ctx, cluster)
}

// DeleteCluster deletes a Cluster by account-scoped metadata.name.
func (c *Client) DeleteCluster(ctx context.Context, accountID, name string) error {
	cluster, err := c.GetCluster(ctx, accountID, name)
	if err != nil {
		return err
	}
	return c.client.Delete(ctx, cluster)
}

// DeleteClusterObject deletes the supplied Cluster with its UID and
// resourceVersion preconditions. It is used to compensate failed creates.
func (c *Client) DeleteClusterObject(ctx context.Context, cluster *seaplanev1alpha1.Cluster) error {
	return c.client.Delete(ctx, cluster)
}

// ClaimSpotTerminationQueue atomically reserves a customer SQS queue URL for a
// Cluster. The bool reports whether this call created the reservation. An
// existing reservation owned by the same Cluster is treated as success.
func (c *Client) ClaimSpotTerminationQueue(ctx context.Context, accountID, queueURL, clusterName string) (bool, error) {
	indexName := seaplanev1alpha1.SpotTerminationQueueIndexName(accountID, queueURL)
	ownerKey := spotQueueOwnerKey(clusterName)
	var lastConflict error

	for range 5 {
		reservation := &seaplanev1alpha1.Index{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: seaplanev1alpha1.SpotTerminationQueueReservationsNamespace,
				Name:      indexName,
				Labels: map[string]string{
					seaplanev1alpha1.SpotTerminationQueueOwnerLabel: ownerKey,
				},
				Annotations: map[string]string{
					seaplanev1alpha1.SpotTerminationQueueClaimedAtAnnotation: time.Now().UTC().Format(time.RFC3339Nano),
				},
			},
		}
		if err := c.client.Create(ctx, reservation); err == nil {
			return true, nil
		} else if !IsAlreadyExists(err) {
			return false, err
		} else {
			createErr := err
			lastConflict = createErr
			var existing seaplanev1alpha1.Index
			getErr := c.client.Get(ctx, k8stypes.NamespacedName{
				Namespace: seaplanev1alpha1.SpotTerminationQueueReservationsNamespace,
				Name:      indexName,
			}, &existing)
			if IsNotFound(getErr) {
				continue
			}
			if getErr != nil {
				return false, getErr
			}
			if existing.Labels[seaplanev1alpha1.SpotTerminationQueueOwnerLabel] == ownerKey {
				if existing.Annotations == nil {
					existing.Annotations = make(map[string]string)
				}
				existing.Annotations[seaplanev1alpha1.SpotTerminationQueueClaimedAtAnnotation] = time.Now().UTC().Format(time.RFC3339Nano)
				if err := c.client.Update(ctx, &existing); err != nil {
					if IsConflict(err) {
						continue
					}
					return false, err
				}
				return false, nil
			}

			ownerName := strings.TrimPrefix(existing.Labels[seaplanev1alpha1.SpotTerminationQueueOwnerLabel], spotQueueOwnerPrefix)
			if ownerName == existing.Labels[seaplanev1alpha1.SpotTerminationQueueOwnerLabel] || ownerName == "" {
				return false, createErr
			}
			ownerCluster, err := c.GetCluster(ctx, accountID, ownerName)
			if err == nil && clusterUsesSpotTerminationQueue(ownerCluster, queueURL) {
				return false, createErr
			}
			if err != nil && !IsNotFound(err) {
				return false, err
			}
			if !spotQueueClaimExpired(&existing) {
				return false, createErr
			}
			if err := c.client.Delete(ctx, &existing); err != nil && !IsNotFound(err) {
				if IsConflict(err) {
					continue
				}
				return false, err
			}
		}
	}

	if lastConflict != nil {
		return false, lastConflict
	}
	return false, fmt.Errorf("could not claim Spot termination queue after reclaiming stale reservations")
}

func spotQueueOwnerKey(clusterName string) string {
	return spotQueueOwnerPrefix + clusterName
}

func spotQueueClaimExpired(index *seaplanev1alpha1.Index) bool {
	claimedAt, err := time.Parse(time.RFC3339Nano, index.Annotations[seaplanev1alpha1.SpotTerminationQueueClaimedAtAnnotation])
	return err == nil && time.Since(claimedAt) > spotQueueClaimTimeout
}

func clusterUsesSpotTerminationQueue(cluster *seaplanev1alpha1.Cluster, queueURL string) bool {
	if cluster == nil || cluster.Spec.HostedCluster.Platform.AWS == nil {
		return false
	}
	return cluster.Spec.HostedCluster.Platform.AWS.TerminationHandlerQueueURL == queueURL
}

// ReleaseSpotTerminationQueue releases a reservation only when it is still
// owned by the given Cluster. A missing reservation is already released.
func (c *Client) ReleaseSpotTerminationQueue(ctx context.Context, accountID, queueURL, clusterName string) error {
	var reservation seaplanev1alpha1.Index
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: seaplanev1alpha1.SpotTerminationQueueReservationsNamespace,
		Name:      seaplanev1alpha1.SpotTerminationQueueIndexName(accountID, queueURL),
	}, &reservation)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if reservation.Labels[seaplanev1alpha1.SpotTerminationQueueOwnerLabel] != spotQueueOwnerKey(clusterName) {
		return fmt.Errorf("spot termination queue reservation is not owned by cluster %q", clusterName)
	}
	if ownerCluster, err := c.GetCluster(ctx, accountID, clusterName); err == nil && clusterUsesSpotTerminationQueue(ownerCluster, queueURL) {
		return nil
	} else if err != nil && !IsNotFound(err) {
		return err
	}
	if err := c.client.Delete(ctx, &reservation); IsNotFound(err) {
		return nil
	} else {
		return err
	}
}

// --- NodePool operations ---

// CreateNodePool creates a NodePool in the caller's account namespace.
func (c *Client) CreateNodePool(ctx context.Context, accountID string, np *seaplanev1alpha1.NodePool) error {
	np.Namespace = accountNamespace(accountID)
	setAccountLabel(np, accountID)
	return c.client.Create(ctx, np)
}

// GetNodePool retrieves a NodePool by its account-scoped metadata.name.
func (c *Client) GetNodePool(ctx context.Context, accountID, name string) (*seaplanev1alpha1.NodePool, error) {
	var nodePool seaplanev1alpha1.NodePool
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: accountNamespace(accountID),
		Name:      name,
	}, &nodePool)
	if err != nil {
		return nil, err
	}
	return &nodePool, nil
}

// ListNodePools lists NodePools in the account namespace, optionally filtered
// by their parent Cluster UID label.
func (c *Client) ListNodePools(ctx context.Context, accountID, clusterUID string) (*seaplanev1alpha1.NodePoolList, error) {
	var list seaplanev1alpha1.NodePoolList
	opts := []client.ListOption{client.InNamespace(accountNamespace(accountID))}
	if clusterUID != "" {
		opts = append(opts, client.MatchingLabels{clusterUIDLabel: clusterUID})
	}

	if err := c.client.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	return &list, nil
}

// UpdateNodePool updates the spec of an existing NodePool via CAS.
func (c *Client) UpdateNodePool(ctx context.Context, np *seaplanev1alpha1.NodePool) error {
	return c.client.Update(ctx, np)
}

// DeleteNodePool deletes a NodePool by its account-scoped metadata.name.
func (c *Client) DeleteNodePool(ctx context.Context, accountID, name string) error {
	np, err := c.GetNodePool(ctx, accountID, name)
	if err != nil {
		return err
	}
	return c.client.Delete(ctx, np)
}

// --- DNSReservation operations ---

func (c *Client) CreateDNSReservation(ctx context.Context, accountID string, reservation *seaplanev1alpha1.DNSReservation) error {
	reservation.Namespace = accountNamespace(accountID)
	setAccountLabel(reservation, accountID)
	return c.client.Create(ctx, reservation)
}

func (c *Client) GetDNSReservation(ctx context.Context, accountID, name string) (*seaplanev1alpha1.DNSReservation, error) {
	var reservation seaplanev1alpha1.DNSReservation
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: accountNamespace(accountID),
		Name:      name,
	}, &reservation)
	if err != nil {
		return nil, err
	}
	return &reservation, nil
}

// GetDNSReservationByUID resolves a reservation UID within an account namespace.
func (c *Client) GetDNSReservationByUID(ctx context.Context, accountID, uid string) (*seaplanev1alpha1.DNSReservation, error) {
	var list seaplanev1alpha1.DNSReservationList
	if err := c.client.List(ctx, &list,
		client.InNamespace(accountNamespace(accountID)),
		client.MatchingFields{"metadata.uid": uid},
	); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, apierrors.NewNotFound(dnsReservationGR, uid)
	}
	return &list.Items[0], nil
}

func (c *Client) ListDNSReservations(ctx context.Context, accountID string) (*seaplanev1alpha1.DNSReservationList, error) {
	var list seaplanev1alpha1.DNSReservationList
	if err := c.client.List(ctx, &list, client.InNamespace(accountNamespace(accountID))); err != nil {
		return nil, err
	}
	return &list, nil
}

func (c *Client) UpdateDNSReservation(ctx context.Context, reservation *seaplanev1alpha1.DNSReservation) error {
	return c.client.Update(ctx, reservation)
}

func (c *Client) UpdateDNSReservationStatus(ctx context.Context, reservation *seaplanev1alpha1.DNSReservation) error {
	return c.client.Status().Update(ctx, reservation)
}

func (c *Client) DeleteDNSReservation(ctx context.Context, accountID, name string) error {
	reservation, err := c.GetDNSReservation(ctx, accountID, name)
	if err != nil {
		return err
	}
	return c.client.Delete(ctx, reservation)
}

func (c *Client) DeleteDNSReservationObject(ctx context.Context, reservation *seaplanev1alpha1.DNSReservation) error {
	return c.client.Delete(ctx, reservation)
}

// CreateDNSDomainIndex reserves a generated HCP DNS prefix globally.
func (c *Client) CreateDNSDomainIndex(ctx context.Context, accountID string, idx *seaplanev1alpha1.Index) error {
	setAccountLabel(idx, accountID)
	return c.client.Create(ctx, idx)
}

// SetDNSDomainIndexOwner associates a DNS-domain Index with its reservation UID.
func (c *Client) SetDNSDomainIndexOwner(ctx context.Context, namespace, name, uid string) error {
	var idx seaplanev1alpha1.Index
	if err := c.client.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: name}, &idx); err != nil {
		return err
	}
	labels := idx.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[ownerUIDLabel] = uid
	idx.SetLabels(labels)
	return c.client.Update(ctx, &idx)
}

// DeleteDNSDomainIndex removes a DNS-domain prefix reservation.
func (c *Client) DeleteDNSDomainIndex(ctx context.Context, namespace, name string) error {
	var idx seaplanev1alpha1.Index
	if err := c.client.Get(ctx, k8stypes.NamespacedName{Namespace: namespace, Name: name}, &idx); err != nil {
		return err
	}
	return c.client.Delete(ctx, &idx)
}

// ListDNSDomainReservations lists DNS reservations that may contain HCP DNS domains.
func (c *Client) ListDNSDomainReservations(ctx context.Context, accountID string) (*seaplanev1alpha1.DNSReservationList, error) {
	return c.ListDNSReservations(ctx, accountID)
}

// CreateDNSDomainReservation stores a customer-created DNS domain for an account.
func (c *Client) CreateDNSDomainReservation(ctx context.Context, accountID string, reservation *seaplanev1alpha1.DNSReservation) error {
	return c.CreateDNSReservation(ctx, accountID, reservation)
}

// UpdateDNSDomainReservationStatus updates a DNS-domain reservation's status.
func (c *Client) UpdateDNSDomainReservationStatus(ctx context.Context, reservation *seaplanev1alpha1.DNSReservation) error {
	return c.UpdateDNSReservationStatus(ctx, reservation)
}

// DeleteDNSDomainReservation deletes a DNS-domain reservation object.
func (c *Client) DeleteDNSDomainReservation(ctx context.Context, accountID, name string) error {
	return c.DeleteDNSReservation(ctx, accountID, name)
}

// DeleteDNSDomainReservationIfUnclaimed deletes an unclaimed DNS-domain
// reservation. Resource-version preconditions serialize the delete with claims.
func (c *Client) DeleteDNSDomainReservationIfUnclaimed(ctx context.Context, accountID, name string) error {
	reservation, err := c.GetDNSReservation(ctx, accountID, name)
	if err != nil {
		return err
	}
	if reservation.Labels[claimedByClusterUIDLabel] != "" {
		return apierrors.NewConflict(dnsReservationGR, name, fmt.Errorf("DNS domain is in use by a cluster"))
	}
	uid := reservation.UID
	resourceVersion := reservation.ResourceVersion
	return c.client.Delete(ctx, reservation, &client.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid, ResourceVersion: &resourceVersion},
	})
}

// --- Manifest operations ---

// CreateManifest creates a Manifest resource in the given namespace.
func (c *Client) CreateManifest(ctx context.Context, namespace string, hfm *seaplanev1alpha1.Manifest) error {
	hfm.Namespace = namespace
	return c.client.Create(ctx, hfm)
}

// GetManifest retrieves a Manifest by namespace and name.
func (c *Client) GetManifest(ctx context.Context, namespace, name string) (*seaplanev1alpha1.Manifest, error) {
	var m seaplanev1alpha1.Manifest
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}, &m)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// DeleteManifest deletes a Manifest by namespace and name.
func (c *Client) DeleteManifest(ctx context.Context, namespace, name string) error {
	m := &seaplanev1alpha1.Manifest{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	return c.client.Delete(ctx, m)
}

// --- ManagementCluster operations ---

// CreateManagementCluster creates a ManagementCluster resource with global scope.
func (c *Client) CreateManagementCluster(ctx context.Context, mc *seaplanev1alpha1.ManagementCluster) error {
	mc.Namespace = ""
	return c.client.Create(ctx, mc)
}

// GetManagementCluster retrieves a ManagementCluster by ID with global scope.
func (c *Client) GetManagementCluster(ctx context.Context, id string) (*seaplanev1alpha1.ManagementCluster, error) {
	var mc seaplanev1alpha1.ManagementCluster
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: "",
		Name:      id,
	}, &mc)
	if err != nil {
		return nil, err
	}
	return &mc, nil
}

// ListManagementClusters lists all ManagementCluster resources (global scope).
func (c *Client) ListManagementClusters(ctx context.Context) (*seaplanev1alpha1.ManagementClusterList, error) {
	var list seaplanev1alpha1.ManagementClusterList
	err := c.client.List(ctx, &list)
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// --- OidcConfig operations ---

// CreateOidcConfig creates an OidcConfig in the caller's account namespace.
func (c *Client) CreateOidcConfig(ctx context.Context, accountID string, oc *seaplanev1alpha1.OidcConfig) error {
	oc.Namespace = accountNamespace(accountID)
	setAccountLabel(oc, accountID)
	return c.client.Create(ctx, oc)
}

// GetOidcConfig retrieves an OidcConfig by account-scoped metadata.name.
func (c *Client) GetOidcConfig(ctx context.Context, accountID, name string) (*seaplanev1alpha1.OidcConfig, error) {
	var oc seaplanev1alpha1.OidcConfig
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: accountNamespace(accountID),
		Name:      name,
	}, &oc)
	if err != nil {
		return nil, err
	}
	return &oc, nil
}

// GetOidcConfigByUID resolves an OidcConfig UID within an account namespace.
func (c *Client) GetOidcConfigByUID(ctx context.Context, accountID, uid string) (*seaplanev1alpha1.OidcConfig, error) {
	var list seaplanev1alpha1.OidcConfigList
	if err := c.client.List(ctx, &list,
		client.InNamespace(accountNamespace(accountID)),
		client.MatchingFields{"metadata.uid": uid},
	); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, apierrors.NewNotFound(seaplanev1alpha1.GroupVersion.WithResource("oidcconfigs").GroupResource(), uid)
	}
	return &list.Items[0], nil
}

// ListOidcConfigs lists OidcConfigs for the given account by namespace.
func (c *Client) ListOidcConfigs(ctx context.Context, accountID string) (*seaplanev1alpha1.OidcConfigList, error) {
	var list seaplanev1alpha1.OidcConfigList
	err := c.client.List(ctx, &list, client.InNamespace(accountNamespace(accountID)))
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// DeleteOidcConfig deletes an OidcConfig by accountID and configID.
func (c *Client) DeleteOidcConfig(ctx context.Context, accountID, configID string) error {
	oc, err := c.GetOidcConfig(ctx, accountID, configID)
	if err != nil {
		return err
	}
	return c.client.Delete(ctx, oc)
}

// UpdateOidcConfigLastUsedTimestamp sets status.lastUsedTimestamp on an
// OidcConfig, scoped to the given account.
func (c *Client) UpdateOidcConfigLastUsedTimestamp(ctx context.Context, accountID, oidcConfigUID string, ts metav1.Time) error {
	oc, err := c.GetOidcConfigByUID(ctx, accountID, oidcConfigUID)
	if err != nil {
		return err
	}
	oc.Status.LastUsedTimestamp = &ts
	return c.client.Status().Update(ctx, oc)
}

// UpdateOidcConfigObject updates oc (e.g. its labels) via CAS on its current ResourceVersion; oc must be freshly fetched (e.g. via GetOidcConfig) so a concurrent change surfaces as a conflict (IsConflict) instead of overwriting it.
func (c *Client) UpdateOidcConfigObject(ctx context.Context, oc *seaplanev1alpha1.OidcConfig) error {
	return c.client.Update(ctx, oc)
}

// DeleteOidcConfigObject deletes oc via CAS on its current ResourceVersion; oc must be freshly fetched (e.g. via GetOidcConfig) so a concurrent change surfaces as a conflict (IsConflict) instead of deleting stale state.
func (c *Client) DeleteOidcConfigObject(ctx context.Context, oc *seaplanev1alpha1.OidcConfig) error {
	return c.client.Delete(ctx, oc)
}

// GetOidcIssuerIndex is a best-effort, non-atomic fast-path check for whether indexName is reserved.
func (c *Client) GetOidcIssuerIndex(ctx context.Context, indexName string) (*seaplanev1alpha1.Index, error) {
	var idx seaplanev1alpha1.Index
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: seaplanev1alpha1.OidcIssuerReservationsNamespace,
		Name:      indexName,
	}, &idx)
	if err != nil {
		return nil, err
	}
	return &idx, nil
}

// --- Error helpers ---

// IsNotFound returns true if the error is a Kubernetes 404.
func IsNotFound(err error) bool {
	return apierrors.IsNotFound(err)
}

// IsAlreadyExists returns true if the error is a Kubernetes 409 (already exists).
func IsAlreadyExists(err error) bool {
	return apierrors.IsAlreadyExists(err)
}

// IsConflict returns true if the error is a Kubernetes 409 from a CAS Update/Delete, distinct from IsAlreadyExists which is a 409 from a colliding Create.
func IsConflict(err error) bool {
	return apierrors.IsConflict(err)
}

// --- internal helpers ---

func setAccountLabel(obj client.Object, accountID string) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[accountIDLabel] = accountID
	obj.SetLabels(labels)
}

var (
	dnsReservationGR = seaplanev1alpha1.GroupVersion.WithResource("dnsreservations").GroupResource()
)

const accountNSPrefix = "account-"

func accountNamespace(accountID string) string {
	return accountNSPrefix + accountID
}
