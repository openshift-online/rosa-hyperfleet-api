package handlers

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"strings"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
)

func (h *NodePoolHandler) nodePoolResource(ctx context.Context, np *v1.NodePool) (authz.Resource, error) {
	id := hyperfleetdb.ClusterIDFromNamespace(np.Namespace)
	if id == np.Namespace || id == "" {
		return authz.Resource{}, &authz.Failure{Stage: authz.StageEntityValidation, Err: fmt.Errorf("invalid node pool parent namespace")}
	}
	// The optional query only selected the object. Its stored namespace determines the parent.
	parent, err := h.db.GetCluster(ctx, id)
	if err != nil {
		return authz.Resource{}, &authz.Failure{Stage: authz.StageResourceLoading, Err: err}
	}
	return authz.Resource{Kind: authz.NodePool, ID: np.Name, AccountID: np.Labels["hyperfleet.io/account-id"], Labels: maps.Clone(np.Labels),
		ParentCluster: &authz.ParentCluster{ID: id, AccountID: parent.Labels["hyperfleet.io/account-id"], Labels: maps.Clone(parent.Labels)}}, nil
}
func (h *NodePoolHandler) authorizeNodePool(w http.ResponseWriter, r *http.Request, operation authz.Action, np *v1.NodePool, additional ...string) bool {
	resource, err := h.nodePoolResource(r.Context(), np)
	if err != nil {
		attempt, _ := authz.DefaultMetrics.Start(operation)
		writeResourceAuthzError(w, r, attempt, operation, err, h.logger)
		return false
	}
	return authorizeResource(w, r, h.authorizer, h.logger, operation, resource, additional...)
}

func (h *OidcConfigHandler) oidcConfigResource(oc *v1.OidcConfig) authz.Resource {
	account := strings.TrimPrefix(oc.Namespace, "account-")
	if account == oc.Namespace {
		account = ""
	}
	return authz.Resource{Kind: authz.OIDCConfig, ID: oc.Name, AccountID: account, Labels: maps.Clone(oc.Labels)}
}
func (h *ManagementClusterHandler) managementClusterResource(mc *v1.ManagementCluster) authz.Resource {
	id := mc.Name
	if mc.Namespace != "" {
		id = ""
	}
	return authz.Resource{Kind: authz.ManagementCluster, ID: id, AccountID: mc.Spec.AccountID, RegistrationRegion: mc.Spec.Region, Labels: maps.Clone(mc.Labels)}
}
