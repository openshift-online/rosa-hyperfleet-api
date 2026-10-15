package hyperfleetdb

import (
	"context"
	"fmt"
	"log/slog"

	hyperfleetdb "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
)

const accountIDLabel = "hyperfleet.io/account-id"
const clusterUIDLabel = "hyperfleet.io/cluster-uid"

// Client wraps a pgruntime client.Client for CRUD on hyperfleet resources.
type Client struct {
	client client.Client
	close  func()
	logger *slog.Logger
}

// NewClient creates a Client backed by hyperfleetdb.NewClient.
// The direct client is never sharded and sees all data.
func NewClient(ctx context.Context, dsn string, logger *slog.Logger) (*Client, error) {
	scheme := runtime.NewScheme()
	if err := hyperfleetv1alpha1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("register hyperfleet scheme: %w", err)
	}

	c, cleanup, err := hyperfleetdb.NewClient(hyperfleetdb.Options{
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

// CreateCluster creates a Cluster in the account namespace from ctx.
func (c *Client) CreateCluster(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) error {
	accountID := middleware.GetAccountID(ctx)
	cluster.Namespace = accountNamespace(accountID)
	setAccountLabel(cluster, accountID)
	cluster.Spec.AccountID = accountID
	return c.client.Create(ctx, cluster)
}

// GetCluster retrieves a Cluster by name in the account namespace from ctx.
func (c *Client) GetCluster(ctx context.Context, name string) (*hyperfleetv1alpha1.Cluster, error) {
	accountID := middleware.GetAccountID(ctx)
	var cluster hyperfleetv1alpha1.Cluster
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: accountNamespace(accountID),
		Name:      name,
	}, &cluster)
	if err != nil {
		return nil, err
	}
	return &cluster, nil
}

// ListClusters lists Clusters in the account namespace from ctx.
func (c *Client) ListClusters(ctx context.Context) (*hyperfleetv1alpha1.ClusterList, error) {
	accountID := middleware.GetAccountID(ctx)
	var list hyperfleetv1alpha1.ClusterList
	err := c.client.List(ctx, &list, client.InNamespace(accountNamespace(accountID)))
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// UpdateCluster CAS-updates a cluster the caller owns; it never moves ownership.
func (c *Client) UpdateCluster(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) error {
	if err := validateOwnedSnapshot(ctx, cluster); err != nil {
		return err
	}
	return c.client.Update(ctx, cluster)
}

// DeleteCluster deletes a Cluster by name in the account namespace from ctx.
func (c *Client) DeleteCluster(ctx context.Context, name string) error {
	cluster, err := c.GetCluster(ctx, name)
	if err != nil {
		return err
	}
	return c.DeleteClusterObject(ctx, cluster)
}

// DeleteClusterObject deletes the checked snapshot without fetching newer state.
func (c *Client) DeleteClusterObject(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) error {
	if err := validateOwnedSnapshot(ctx, cluster); err != nil {
		return err
	}
	return c.client.Delete(ctx, cluster)
}

// --- NodePool operations ---

// CreateNodePool creates a NodePool under a parent owned by the account in ctx.
func (c *Client) CreateNodePool(ctx context.Context, np *hyperfleetv1alpha1.NodePool) error {
	accountID := middleware.GetAccountID(ctx)
	np.Namespace = accountNamespace(accountID)
	if err := c.requireOwnedCluster(ctx, np); err != nil {
		return err
	}
	setAccountLabel(np, accountID)
	np.Spec.AccountID = accountID
	return c.client.Create(ctx, np)
}

// requireOwnedCluster checks the stored parent name and UID, not child ownership labels.
func (c *Client) requireOwnedCluster(ctx context.Context, np *hyperfleetv1alpha1.NodePool) error {
	parent := nodePoolParent(np)
	if parent == nil {
		return apierrors.NewNotFound(nodePoolGR, np.Name)
	}
	cluster, err := c.GetCluster(ctx, parent.Name)
	if err != nil {
		return err
	}
	if cluster.UID != parent.UID {
		return apierrors.NewNotFound(nodePoolGR, np.Name)
	}
	return nil
}

func nodePoolParent(np *hyperfleetv1alpha1.NodePool) *metav1.OwnerReference {
	parent := metav1.GetControllerOf(np)
	if parent == nil || parent.Kind != "Cluster" || parent.UID == "" || np.Labels[clusterUIDLabel] != string(parent.UID) {
		return nil
	}
	return parent
}

// GetNodePool retrieves a NodePool by name in the account namespace from ctx.
func (c *Client) GetNodePool(ctx context.Context, name string) (*hyperfleetv1alpha1.NodePool, error) {
	accountID := middleware.GetAccountID(ctx)
	var nodePool hyperfleetv1alpha1.NodePool
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: accountNamespace(accountID),
		Name:      name,
	}, &nodePool)
	if err != nil {
		return nil, err
	}
	if err := c.requireOwnedCluster(ctx, &nodePool); err != nil {
		return nil, err
	}
	return &nodePool, nil
}

// ListNodePools lists NodePools in the account namespace from ctx, optionally
// filtered by their parent Cluster UID label.
func (c *Client) ListNodePools(ctx context.Context, clusterUID string) (*hyperfleetv1alpha1.NodePoolList, error) {
	accountID := middleware.GetAccountID(ctx)
	var list hyperfleetv1alpha1.NodePoolList
	opts := []client.ListOption{client.InNamespace(accountNamespace(accountID))}
	if clusterUID != "" {
		opts = append(opts, client.MatchingLabels{clusterUIDLabel: clusterUID})
	}

	if err := c.client.List(ctx, &list, opts...); err != nil {
		return nil, err
	}

	// Resolve parent ownership in one batch, including the current object incarnation.
	parents, err := c.ListClusters(ctx)
	if err != nil {
		return nil, err
	}
	owned := make(map[string]k8stypes.UID, len(parents.Items))
	for i := range parents.Items {
		owned[parents.Items[i].Name] = parents.Items[i].UID
	}
	items := list.Items[:0]
	for i := range list.Items {
		parent := nodePoolParent(&list.Items[i])
		if parent == nil || owned[parent.Name] != parent.UID {
			continue
		}
		items = append(items, list.Items[i])
	}
	list.Items = items
	return &list, nil
}

// UpdateNodePool CAS-updates a node pool the caller owns; it never moves ownership.
func (c *Client) UpdateNodePool(ctx context.Context, np *hyperfleetv1alpha1.NodePool) error {
	if err := validateOwnedSnapshot(ctx, np); err != nil {
		return err
	}
	return c.client.Update(ctx, np)
}

// DeleteNodePool deletes a NodePool by name in the account namespace from ctx.
func (c *Client) DeleteNodePool(ctx context.Context, name string) error {
	np, err := c.GetNodePool(ctx, name)
	if err != nil {
		return err
	}
	return c.DeleteNodePoolObject(ctx, np)
}

// DeleteNodePoolObject deletes the checked child snapshot, never an unchecked reload.
func (c *Client) DeleteNodePoolObject(ctx context.Context, np *hyperfleetv1alpha1.NodePool) error {
	if err := validateOwnedSnapshot(ctx, np); err != nil {
		return err
	}
	return c.client.Delete(ctx, np)
}

// --- DNSReservation operations ---

func (c *Client) CreateDNSReservation(ctx context.Context, accountID string, reservation *hyperfleetv1alpha1.DNSReservation) error {
	reservation.Namespace = accountNamespace(accountID)
	setAccountLabel(reservation, accountID)
	return c.client.Create(ctx, reservation)
}

func (c *Client) GetDNSReservation(ctx context.Context, accountID, name string) (*hyperfleetv1alpha1.DNSReservation, error) {
	var reservation hyperfleetv1alpha1.DNSReservation
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
func (c *Client) GetDNSReservationByUID(ctx context.Context, accountID, uid string) (*hyperfleetv1alpha1.DNSReservation, error) {
	var list hyperfleetv1alpha1.DNSReservationList
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

func (c *Client) ListDNSReservations(ctx context.Context, accountID string) (*hyperfleetv1alpha1.DNSReservationList, error) {
	var list hyperfleetv1alpha1.DNSReservationList
	if err := c.client.List(ctx, &list, client.InNamespace(accountNamespace(accountID))); err != nil {
		return nil, err
	}
	return &list, nil
}

func (c *Client) UpdateDNSReservation(ctx context.Context, reservation *hyperfleetv1alpha1.DNSReservation) error {
	return c.client.Update(ctx, reservation)
}

func (c *Client) UpdateDNSReservationStatus(ctx context.Context, reservation *hyperfleetv1alpha1.DNSReservation) error {
	return c.client.Status().Update(ctx, reservation)
}

func (c *Client) DeleteDNSReservation(ctx context.Context, accountID, name string) error {
	reservation, err := c.GetDNSReservation(ctx, accountID, name)
	if err != nil {
		return err
	}
	return c.client.Delete(ctx, reservation)
}

func (c *Client) DeleteDNSReservationObject(ctx context.Context, reservation *hyperfleetv1alpha1.DNSReservation) error {
	return c.client.Delete(ctx, reservation)
}

// --- Manifest operations ---

// CreateManifest creates a Manifest resource in the given namespace.
func (c *Client) CreateManifest(ctx context.Context, namespace string, hfm *hyperfleetv1alpha1.Manifest) error {
	hfm.Namespace = namespace
	return c.client.Create(ctx, hfm)
}

// GetManifest retrieves a Manifest by namespace and name.
func (c *Client) GetManifest(ctx context.Context, namespace, name string) (*hyperfleetv1alpha1.Manifest, error) {
	var m hyperfleetv1alpha1.Manifest
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
	m := &hyperfleetv1alpha1.Manifest{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
	return c.client.Delete(ctx, m)
}

// --- ManagementCluster operations ---

// CreateManagementCluster creates a ManagementCluster resource with global scope.
func (c *Client) CreateManagementCluster(ctx context.Context, mc *hyperfleetv1alpha1.ManagementCluster) error {
	mc.Namespace = ""
	return c.client.Create(ctx, mc)
}

// GetManagementCluster retrieves a ManagementCluster by ID with global scope.
func (c *Client) GetManagementCluster(ctx context.Context, id string) (*hyperfleetv1alpha1.ManagementCluster, error) {
	var mc hyperfleetv1alpha1.ManagementCluster
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
func (c *Client) ListManagementClusters(ctx context.Context) (*hyperfleetv1alpha1.ManagementClusterList, error) {
	var list hyperfleetv1alpha1.ManagementClusterList
	err := c.client.List(ctx, &list)
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// --- OidcConfig operations ---

// CreateOidcConfig creates an OidcConfig in the caller's account namespace.
func (c *Client) CreateOidcConfig(ctx context.Context, accountID string, oc *hyperfleetv1alpha1.OidcConfig) error {
	oc.Namespace = accountNamespace(accountID)
	setAccountLabel(oc, accountID)
	return c.client.Create(ctx, oc)
}

// GetOidcConfig retrieves an OidcConfig by name for the account in ctx.
func (c *Client) GetOidcConfig(ctx context.Context, name string) (*hyperfleetv1alpha1.OidcConfig, error) {
	accountID := middleware.GetAccountID(ctx)
	var oc hyperfleetv1alpha1.OidcConfig
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: accountNamespace(accountID),
		Name:      name,
	}, &oc)
	if err != nil {
		return nil, err
	}
	return &oc, nil
}

// GetOidcConfigByUID resolves an OidcConfig UID within the account namespace from ctx.
func (c *Client) GetOidcConfigByUID(ctx context.Context, uid string) (*hyperfleetv1alpha1.OidcConfig, error) {
	accountID := middleware.GetAccountID(ctx)
	var list hyperfleetv1alpha1.OidcConfigList
	if err := c.client.List(ctx, &list,
		client.InNamespace(accountNamespace(accountID)),
		client.MatchingFields{"metadata.uid": uid},
	); err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, apierrors.NewNotFound(hyperfleetv1alpha1.GroupVersion.WithResource("oidcconfigs").GroupResource(), uid)
	}
	return &list.Items[0], nil
}

// ListOidcConfigs lists OidcConfigs for the account in ctx by namespace.
func (c *Client) ListOidcConfigs(ctx context.Context) (*hyperfleetv1alpha1.OidcConfigList, error) {
	accountID := middleware.GetAccountID(ctx)
	var list hyperfleetv1alpha1.OidcConfigList
	err := c.client.List(ctx, &list, client.InNamespace(accountNamespace(accountID)))
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// DeleteOidcConfig deletes an OidcConfig by name for the account in ctx.
func (c *Client) DeleteOidcConfig(ctx context.Context, name string) error {
	oc, err := c.GetOidcConfig(ctx, name)
	if err != nil {
		return err
	}
	return c.DeleteOidcConfigObject(ctx, oc)
}

// UpdateOidcConfigLastUsedTimestamp sets status.lastUsedTimestamp on an
// OidcConfig, resolved by UID within the account in ctx.
func (c *Client) UpdateOidcConfigLastUsedTimestamp(ctx context.Context, oidcConfigUID string, ts metav1.Time) error {
	oc, err := c.GetOidcConfigByUID(ctx, oidcConfigUID)
	if err != nil {
		return err
	}
	oc.Status.LastUsedTimestamp = &ts
	return c.client.Status().Update(ctx, oc)
}

// UpdateOidcConfigObject updates oc (e.g. its labels) via CAS on its current ResourceVersion; oc must be freshly fetched (e.g. via GetOidcConfig) so a concurrent change surfaces as a conflict (IsConflict) instead of overwriting it.
func (c *Client) UpdateOidcConfigObject(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig) error {
	if err := hyperfleetdb.ValidateObjectResourceVersion(oc); err != nil {
		return err
	}
	return c.client.Update(ctx, oc)
}

// DeleteOidcConfigObject deletes oc via CAS on its current ResourceVersion; oc must be freshly fetched (e.g. via GetOidcConfig) so a concurrent change surfaces as a conflict (IsConflict) instead of deleting stale state.
func (c *Client) DeleteOidcConfigObject(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig) error {
	if err := validateOwnedSnapshot(ctx, oc); err != nil {
		return err
	}
	return c.client.Delete(ctx, oc)
}

func validateOwnedSnapshot(ctx context.Context, obj client.Object) error {
	if err := hyperfleetdb.ValidateObjectResourceVersion(obj); err != nil {
		return err
	}
	account := middleware.GetAccountID(ctx)
	if account == "" || obj.GetNamespace() != accountNamespace(account) || obj.GetName() == "" {
		return fmt.Errorf("invalid snapshot identity")
	}
	if owner, exists := obj.GetLabels()[accountIDLabel]; exists && owner != account {
		return fmt.Errorf("snapshot owner does not match caller")
	}
	return nil
}

// GetOidcIssuerIndex is a best-effort, non-atomic fast-path check for whether indexName is reserved.
func (c *Client) GetOidcIssuerIndex(ctx context.Context, indexName string) (*hyperfleetv1alpha1.Index, error) {
	var idx hyperfleetv1alpha1.Index
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: hyperfleetv1alpha1.OidcIssuerReservationsNamespace,
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
	nodePoolGR       = hyperfleetv1alpha1.GroupVersion.WithResource("nodepools").GroupResource()
	dnsReservationGR = hyperfleetv1alpha1.GroupVersion.WithResource("dnsreservations").GroupResource()
)

const accountNSPrefix = "account-"

func accountNamespace(accountID string) string {
	return accountNSPrefix + accountID
}
