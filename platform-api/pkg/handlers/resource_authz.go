package handlers

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"strings"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Creation is authorized before FleetDB assigns a UID.
func candidateResource(resource authz.Resource, name string) authz.Resource {
	resource.ID = name
	return resource
}

func (h *NodePoolHandler) nodePoolResource(ctx context.Context, np *v1.NodePool) (authz.Resource, error) {
	owner := metav1.GetControllerOf(np)
	if owner == nil || owner.Kind != "Cluster" || owner.UID == "" || np.Labels[clusterUIDLabel] != string(owner.UID) {
		return authz.Resource{}, &authz.Failure{Stage: authz.StageEntityValidation, Err: fmt.Errorf("invalid node pool parent binding")}
	}
	// Resolve the stored controller reference within the caller's account and pin its incarnation.
	parent, err := h.db.GetCluster(ctx, owner.Name)
	if err != nil {
		return authz.Resource{}, &authz.Failure{Stage: authz.StageResourceLoading, Err: err}
	}
	if parent.UID != owner.UID {
		return authz.Resource{}, &authz.Failure{Stage: authz.StageEntityValidation, Err: fmt.Errorf("node pool parent UID does not match")}
	}
	return authz.Resource{Kind: authz.NodePool, ID: string(np.UID), AccountID: np.Labels["hyperfleet.io/account-id"], Labels: maps.Clone(np.Labels),
		ParentCluster: &authz.ParentCluster{ID: string(parent.UID), AccountID: parent.Labels["hyperfleet.io/account-id"], Labels: maps.Clone(parent.Labels)}}, nil
}
func (h *NodePoolHandler) authorizeNodePool(w http.ResponseWriter, r *http.Request, operation authz.Action, np *v1.NodePool, additional ...authz.Action) bool {
	resource, err := h.nodePoolResource(r.Context(), np)
	if err != nil {
		attempt := authz.DefaultMetrics.Start(operation)
		writeResourceAuthzError(w, r, attempt, operation, err, h.logger)
		return false
	}
	if operation == authz.CreateNodePool {
		resource = candidateResource(resource, np.Name)
	}
	return authorizeResource(w, r, h.authorizer, h.logger, operation, resource, additional...)
}

// namespaceAccount returns the account that owns an account-<id> namespace, or "".
func namespaceAccount(namespace string) string {
	account, isAccountNamespace := strings.CutPrefix(namespace, "account-")
	if !isAccountNamespace {
		return ""
	}
	return account
}

func (h *OidcConfigHandler) oidcConfigResource(oc *v1.OidcConfig) authz.Resource {
	return authz.Resource{Kind: authz.OIDCConfig, ID: string(oc.UID), AccountID: namespaceAccount(oc.Namespace), Labels: maps.Clone(oc.Labels)}
}

func (h *DNSReservationHandler) dnsReservationResource(res *v1.DNSReservation) authz.Resource {
	return authz.Resource{Kind: authz.DNSReservation, ID: string(res.UID), AccountID: namespaceAccount(res.Namespace), Labels: maps.Clone(res.Labels)}
}
func (h *ManagementClusterHandler) managementClusterResource(mc *v1.ManagementCluster) authz.Resource {
	id := mc.Name
	if mc.Namespace != "" {
		id = ""
	}
	return authz.Resource{Kind: authz.ManagementCluster, ID: id, AccountID: mc.Spec.AccountID, RegistrationRegion: mc.Spec.Region, Labels: maps.Clone(mc.Labels)}
}

// clusterUpdateActions returns the specialized actions an update needs beyond
// UpdateCluster. It compares the stored spec with the exact candidate to be written.
func clusterUpdateActions(old, candidate *v1.ClusterSpec) []authz.Action {
	var actions []authz.Action
	if !reflect.DeepEqual(old.HostedCluster.Configuration, candidate.HostedCluster.Configuration) {
		actions = append(actions, authz.UpdateClusterConfig)
	}
	if !reflect.DeepEqual(old.HostedCluster.Release, candidate.HostedCluster.Release) ||
		!reflect.DeepEqual(old.ControlPlaneUpgradePolicy, candidate.ControlPlaneUpgradePolicy) {
		actions = append(actions, authz.UpdateClusterVersion)
	}
	return actions
}

// nodePoolUpdateActions returns the specialized actions an update needs beyond
// UpdateNodePool. It compares the stored spec with the exact candidate to be written.
func nodePoolUpdateActions(old, candidate *v1.NodePoolSpec) []authz.Action {
	var actions []authz.Action
	if !reflect.DeepEqual(old.NodePool.Replicas, candidate.NodePool.Replicas) ||
		!reflect.DeepEqual(old.NodePool.AutoScaling, candidate.NodePool.AutoScaling) {
		actions = append(actions, authz.ScaleNodePool)
	}
	if !reflect.DeepEqual(old.NodePool.Release, candidate.NodePool.Release) {
		actions = append(actions, authz.UpdateNodePoolVersion)
	}
	return actions
}
