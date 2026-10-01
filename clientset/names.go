/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package hyperfleet

import (
	"k8s.io/apimachinery/pkg/types"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

// ClusterUIDLabel is set on every object that belongs to a cluster, to the
// cluster's uid. Select a cluster's node pools with it:
//
//	platform.ListOptions{LabelSelector: hyperfleet.ClusterUIDSelector(cluster.UID)}
const ClusterUIDLabel = hyperfleetv1alpha1.ClusterUIDLabel

// AccountNamespace returns the namespace holding accountID's clusters and node
// pools, e.g. for cs.HyperfleetV1alpha1().NodePools(AccountNamespace(id)).
func AccountNamespace(accountID string) string {
	return hyperfleetv1alpha1.AccountNamespace(accountID)
}

// NodePoolName returns the name of a cluster's node pool: "<cluster>.<pool>".
// Neither part may contain a dot.
func NodePoolName(clusterName, pool string) string {
	return hyperfleetv1alpha1.ChildName(clusterName, pool)
}

// ClusterUIDSelector returns a label selector matching the objects that
// belong to the cluster with uid.
func ClusterUIDSelector(uid types.UID) string {
	return ClusterUIDLabel + "=" + string(uid)
}
