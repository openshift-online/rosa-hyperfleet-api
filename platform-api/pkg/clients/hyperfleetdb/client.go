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

// CreateCluster creates a Cluster resource. Namespace = clusterID (UUID),
// Name = human-readable name. Labeled with the account ID from ctx.
func (c *Client) CreateCluster(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) error {
	accountID := middleware.GetAccountID(ctx)
	setAccountLabel(cluster, accountID)
	cluster.Spec.AccountID = accountID
	return c.client.Create(ctx, cluster)
}

// GetCluster retrieves a Cluster by clusterID, scoped to the account in ctx.
// Namespace = clusterID, filtered by account-id label.
func (c *Client) GetCluster(ctx context.Context, clusterID string) (*hyperfleetv1alpha1.Cluster, error) {
	accountID := middleware.GetAccountID(ctx)
	var list hyperfleetv1alpha1.ClusterList
	err := c.client.List(
		ctx, &list,
		client.InNamespace(clusterNamespace(clusterID)),
		client.MatchingLabels{accountIDLabel: accountID},
	)
	if err != nil {
		return nil, err
	}
	if len(list.Items) == 0 {
		return nil, apierrors.NewNotFound(clusterGR, clusterID)
	}
	return &list.Items[0], nil
}

// ListClusters lists Clusters for the account in ctx using the account-id label.
func (c *Client) ListClusters(ctx context.Context) (*hyperfleetv1alpha1.ClusterList, error) {
	accountID := middleware.GetAccountID(ctx)
	var list hyperfleetv1alpha1.ClusterList
	err := c.client.List(ctx, &list, client.MatchingLabels{accountIDLabel: accountID})
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// UpdateCluster CAS-updates a cluster and re-pins account ownership from ctx.
func (c *Client) UpdateCluster(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) error {
	accountID := middleware.GetAccountID(ctx)
	setAccountLabel(cluster, accountID)
	cluster.Spec.AccountID = accountID
	return c.client.Update(ctx, cluster)
}

// DeleteCluster deletes a Cluster, scoped to the account in ctx.
func (c *Client) DeleteCluster(ctx context.Context, clusterID string) error {
	cluster, err := c.GetCluster(ctx, clusterID)
	if err != nil {
		return err
	}
	return c.client.Delete(ctx, cluster)
}

// --- NodePool operations ---

// CreateNodePool creates a NodePool labeled with the account in ctx.
func (c *Client) CreateNodePool(ctx context.Context, np *hyperfleetv1alpha1.NodePool) error {
	accountID := middleware.GetAccountID(ctx)
	setAccountLabel(np, accountID)
	np.Spec.AccountID = accountID
	return c.client.Create(ctx, np)
}

// GetNodePool retrieves a NodePool by name, scoped to the account in ctx and optionally cluster.
func (c *Client) GetNodePool(
	ctx context.Context, clusterID, nodepoolName string,
) (*hyperfleetv1alpha1.NodePool, error) {
	accountID := middleware.GetAccountID(ctx)
	if clusterID != "" {
		var np hyperfleetv1alpha1.NodePool
		err := c.client.Get(ctx, k8stypes.NamespacedName{
			Namespace: clusterNamespace(clusterID),
			Name:      nodepoolName,
		}, &np)
		if err != nil {
			return nil, err
		}
		if np.Labels[accountIDLabel] != accountID {
			return nil, apierrors.NewNotFound(nodePoolGR, nodepoolName)
		}
		return &np, nil
	}

	var list hyperfleetv1alpha1.NodePoolList
	err := c.client.List(ctx, &list, client.MatchingLabels{accountIDLabel: accountID})
	if err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].Name != nodepoolName {
			continue
		}
		return &list.Items[i], nil
	}
	return nil, apierrors.NewNotFound(nodePoolGR, nodepoolName)
}

// ListNodePools lists NodePools. If clusterID is set, lists by namespace
// scoped to the account in ctx. Otherwise lists all nodepools for that account.
func (c *Client) ListNodePools(ctx context.Context, clusterID string) (*hyperfleetv1alpha1.NodePoolList, error) {
	accountID := middleware.GetAccountID(ctx)
	var list hyperfleetv1alpha1.NodePoolList
	var opts []client.ListOption

	opts = append(opts, client.MatchingLabels{accountIDLabel: accountID})
	if clusterID != "" {
		opts = append(opts, client.InNamespace(clusterNamespace(clusterID)))
	}

	if err := c.client.List(ctx, &list, opts...); err != nil {
		return nil, err
	}
	return &list, nil
}

// UpdateNodePool CAS-updates a node pool and re-pins account ownership from ctx.
func (c *Client) UpdateNodePool(ctx context.Context, np *hyperfleetv1alpha1.NodePool) error {
	accountID := middleware.GetAccountID(ctx)
	setAccountLabel(np, accountID)
	np.Spec.AccountID = accountID
	return c.client.Update(ctx, np)
}

// DeleteNodePool deletes a NodePool by name, scoped to the account in ctx and cluster.
func (c *Client) DeleteNodePool(ctx context.Context, clusterID, nodepoolName string) error {
	np, err := c.GetNodePool(ctx, clusterID, nodepoolName)
	if err != nil {
		return err
	}
	return c.client.Delete(ctx, np)
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

// CreateOidcConfig creates an OidcConfig resource.
// Namespace = account-<accountID>, Name = configID.
func (c *Client) CreateOidcConfig(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig) error {
	return c.client.Create(ctx, oc)
}

// GetOidcConfig retrieves an OidcConfig by configID for the account in ctx.
func (c *Client) GetOidcConfig(ctx context.Context, configID string) (*hyperfleetv1alpha1.OidcConfig, error) {
	accountID := middleware.GetAccountID(ctx)
	var oc hyperfleetv1alpha1.OidcConfig
	err := c.client.Get(ctx, k8stypes.NamespacedName{
		Namespace: accountNamespace(accountID),
		Name:      configID,
	}, &oc)
	if err != nil {
		return nil, err
	}
	return &oc, nil
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

// DeleteOidcConfig deletes an OidcConfig by configID for the account in ctx.
func (c *Client) DeleteOidcConfig(ctx context.Context, configID string) error {
	oc, err := c.GetOidcConfig(ctx, configID)
	if err != nil {
		return err
	}
	return c.client.Delete(ctx, oc)
}

// UpdateOidcConfigLastUsedTimestamp sets status.lastUsedTimestamp on an
// OidcConfig, scoped to the account in ctx.
func (c *Client) UpdateOidcConfigLastUsedTimestamp(ctx context.Context, configID string, ts metav1.Time) error {
	oc, err := c.GetOidcConfig(ctx, configID)
	if err != nil {
		return err
	}
	oc.Status.LastUsedTimestamp = &ts
	return c.client.Status().Update(ctx, oc)
}

// UpdateOidcConfigObject updates oc (e.g. its labels) via CAS on its current ResourceVersion; oc must be freshly fetched (e.g. via GetOidcConfig) so a concurrent change surfaces as a conflict (IsConflict) instead of overwriting it.
func (c *Client) UpdateOidcConfigObject(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig) error {
	return c.client.Update(ctx, oc)
}

// DeleteOidcConfigObject deletes oc via CAS on its current ResourceVersion; oc must be freshly fetched (e.g. via GetOidcConfig) so a concurrent change surfaces as a conflict (IsConflict) instead of deleting stale state.
func (c *Client) DeleteOidcConfigObject(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig) error {
	return c.client.Delete(ctx, oc)
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
	clusterGR  = hyperfleetv1alpha1.GroupVersion.WithResource("clusters").GroupResource()
	nodePoolGR = hyperfleetv1alpha1.GroupVersion.WithResource("nodepools").GroupResource()
)

const accountNSPrefix = "account-"

func accountNamespace(accountID string) string {
	return accountNSPrefix + accountID
}
