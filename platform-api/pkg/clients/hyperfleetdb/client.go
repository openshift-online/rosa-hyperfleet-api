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
// Name = human-readable name. Labeled with the account ID.
func (c *Client) CreateCluster(ctx context.Context, accountID string, cluster *hyperfleetv1alpha1.Cluster) error {
	setAccountLabel(cluster, accountID)
	return c.client.Create(ctx, cluster)
}

// GetCluster retrieves a Cluster by clusterID, scoped to the given account.
// Namespace = clusterID, filtered by account-id label.
func (c *Client) GetCluster(ctx context.Context, accountID, clusterID string) (*hyperfleetv1alpha1.Cluster, error) {
	var list hyperfleetv1alpha1.ClusterList
	err := c.client.List(ctx, &list,
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

// ListClusters lists Clusters for the given account using the account-id label.
func (c *Client) ListClusters(ctx context.Context, accountID string) (*hyperfleetv1alpha1.ClusterList, error) {
	var list hyperfleetv1alpha1.ClusterList
	err := c.client.List(ctx, &list, client.MatchingLabels{accountIDLabel: accountID})
	if err != nil {
		return nil, err
	}
	return &list, nil
}

// UpdateCluster updates the spec of an existing Cluster via CAS.
func (c *Client) UpdateCluster(ctx context.Context, cluster *hyperfleetv1alpha1.Cluster) error {
	return c.client.Update(ctx, cluster)
}

// DeleteCluster deletes a Cluster, scoped to the given account.
func (c *Client) DeleteCluster(ctx context.Context, accountID, clusterID string) error {
	cluster, err := c.GetCluster(ctx, accountID, clusterID)
	if err != nil {
		return err
	}
	return c.client.Delete(ctx, cluster)
}

// --- NodePool operations ---

// CreateNodePool creates a NodePool resource. Namespace = clusterID,
// Name = human-readable name. Labeled with the account ID.
func (c *Client) CreateNodePool(ctx context.Context, accountID string, np *hyperfleetv1alpha1.NodePool) error {
	setAccountLabel(np, accountID)
	return c.client.Create(ctx, np)
}

// GetNodePool retrieves a NodePool by name, scoped to the given account.
func (c *Client) GetNodePool(ctx context.Context, accountID, nodepoolName string) (*hyperfleetv1alpha1.NodePool, error) {
	var list hyperfleetv1alpha1.NodePoolList
	err := c.client.List(ctx, &list, client.MatchingLabels{accountIDLabel: accountID})
	if err != nil {
		return nil, err
	}
	for i := range list.Items {
		if list.Items[i].Name == nodepoolName {
			return &list.Items[i], nil
		}
	}
	return nil, apierrors.NewNotFound(nodePoolGR, nodepoolName)
}

// ListNodePools lists NodePools. If clusterID is set, lists by namespace
// scoped to the account. Otherwise lists all nodepools for the account.
func (c *Client) ListNodePools(ctx context.Context, accountID, clusterID string) (*hyperfleetv1alpha1.NodePoolList, error) {
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

// UpdateNodePool updates the spec of an existing NodePool via CAS.
func (c *Client) UpdateNodePool(ctx context.Context, np *hyperfleetv1alpha1.NodePool) error {
	return c.client.Update(ctx, np)
}

// DeleteNodePool deletes a NodePool by name, scoped to the given account.
func (c *Client) DeleteNodePool(ctx context.Context, accountID, nodepoolName string) error {
	np, err := c.GetNodePool(ctx, accountID, nodepoolName)
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

// GetOidcConfig retrieves an OidcConfig by accountID and configID.
func (c *Client) GetOidcConfig(ctx context.Context, accountID, configID string) (*hyperfleetv1alpha1.OidcConfig, error) {
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

// ListOidcConfigs lists OidcConfigs for the given account by namespace.
func (c *Client) ListOidcConfigs(ctx context.Context, accountID string) (*hyperfleetv1alpha1.OidcConfigList, error) {
	var list hyperfleetv1alpha1.OidcConfigList
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
func (c *Client) UpdateOidcConfigLastUsedTimestamp(ctx context.Context, accountID, configID string, ts metav1.Time) error {
	oc, err := c.GetOidcConfig(ctx, accountID, configID)
	if err != nil {
		return err
	}
	oc.Status.LastUsedTimestamp = &ts
	return c.client.Status().Update(ctx, oc)
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
