package authz

// actionResourceKind returns the resource kind for a supported action without inferring it from HTTP paths.
func actionResourceKind(action Action) ResourceKind {
	switch action {
	case ListClusters, DescribeCluster, CreateCluster, UpdateCluster, UpdateClusterConfig, UpdateClusterVersion, DeleteCluster:
		return Cluster
	case ListNodePools, DescribeNodePool, CreateNodePool, UpdateNodePool, ScaleNodePool, UpdateNodePoolVersion, DeleteNodePool:
		return NodePool
	case ListOIDCConfigs, DescribeOIDCConfig, CreateOIDCConfig, DeleteOIDCConfig:
		return OIDCConfig
	case ListManagementClusters, DescribeManagementCluster, CreateManagementCluster:
		return ManagementCluster
	default:
		return ""
	}
}

// listAction reports whether the action authorizes a collection listing.
func listAction(action Action) bool {
	switch action {
	case ListClusters, ListNodePools, ListOIDCConfigs, ListManagementClusters:
		return true
	default:
		return false
	}
}

// collectionName returns the collection segment used in Cedar UIDs for the resource kind.
func collectionName(kind ResourceKind) string {
	switch kind {
	case Cluster:
		return "clusters"
	case NodePool:
		return "nodepools"
	case OIDCConfig:
		return "oidc_configs"
	case ManagementCluster:
		return "management_clusters"
	default:
		return ""
	}
}
