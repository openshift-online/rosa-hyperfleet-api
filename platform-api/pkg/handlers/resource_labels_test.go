//go:build integration

package handlers

import (
	"io"
	"log/slog"
	"testing"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestCapturedResourceLabelsAreSnapshots(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := clusterAuthorizer(t, lifecyclePermit)
	for _, kind := range []string{"Cluster", "NodePool", "OIDCConfig", "ManagementCluster"} {
		t.Run(kind, func(t *testing.T) {
			labels := map[string]string{"hyperfleet.io/account-id": testAccountID, "team": "blue"}
			var resource authz.Resource
			switch kind {
			case "Cluster":
				cr := testClusterCR("parent", "parent", testAccountID)
				cr.Labels = labels
				resource = NewClusterHandler(nil, "", 0, a, logger).clusterResource(cr)
			case "NodePool":
				parent := testClusterCR("parent", "parent", testAccountID)
				np := testNodePoolCR("workers", "cluster-parent", testAccountID)
				np.Labels = labels
				fc := fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(parent).Build()
				var err error
				resource, err = NewNodePoolHandler(hyperfleetdb.NewClientFrom(fc, logger), a, logger).nodePoolResource(testContext(testAccountID), np)
				if err != nil {
					t.Fatal(err)
				}
			case "OIDCConfig":
				oc := testOidcConfigCR("config", testAccountID, testManagedOidcConfigSpec(testAccountID))
				oc.Labels = labels
				resource = NewOidcConfigHandler(nil, "", testRegion, a, logger).oidcConfigResource(oc)
			case "ManagementCluster":
				mc := &v1.ManagementCluster{ObjectMeta: metav1.ObjectMeta{Name: "mc", Labels: labels}, Spec: v1.ManagementClusterSpec{AccountID: testAccountID, Region: testRegion}}
				resource = NewManagementClusterHandler(nil, a, logger).managementClusterResource(mc)
			}
			labels["team"] = "red"
			if resource.Labels["team"] != "blue" {
				t.Fatal("captured authorization labels changed with the input object")
			}
		})
	}
}
