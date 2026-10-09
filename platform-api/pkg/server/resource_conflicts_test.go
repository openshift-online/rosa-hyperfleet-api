package server

import (
	"context"
	"fmt"
	"testing"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestAuthorizedSnapshotConflictDoesNotRetryUncheckedState(t *testing.T) {
	for _, tc := range []struct {
		method, path, body string
		index              int
	}{
		{"PUT", "clusters/" + resourceTestClusterID, `{"spec":{"displayName":"requested"}}`, 0},
		{"PATCH", "clusters/" + resourceTestClusterID, `{"spec":{"displayName":"requested"}}`, 0},
		{"PUT", "nodepools/workers", `{"spec":{"displayName":"requested"}}`, 1},
		{"DELETE", "clusters/" + resourceTestClusterID, "", 0},
		{"DELETE", "nodepools/workers", "", 1},
		{"DELETE", "oidc_configs/config", "", 2},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			objects := resourceFixtures()
			key := client.ObjectKeyFromObject(objects[tc.index])
			writes := 0
			var authorizedRV string
			conflict := func(ctx context.Context, c client.WithWatch, obj client.Object) error {
				writes++
				authorizedRV = obj.GetResourceVersion()
				if authorizedRV == "" || authorizedRV == "0" {
					t.Fatal("write did not use checked version")
				}
				competitor := objects[tc.index].DeepCopyObject().(client.Object)
				if err := c.Get(ctx, key, competitor); err != nil {
					return err
				}
				labels := competitor.GetLabels()
				labels["team"] = "red"
				competitor.SetLabels(labels)
				if err := c.Update(ctx, competitor); err != nil {
					return err
				}
				if competitor.GetResourceVersion() == authorizedRV {
					t.Fatal("fixture failed to change version")
				}
				return apierrors.NewConflict(v1.GroupVersion.WithResource("resources").GroupResource(), obj.GetName(), fmt.Errorf("checked version lost"))
			}
			hooks := interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, _ ...client.UpdateOption) error {
				return conflict(ctx, c, obj)
			}, Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
				return conflict(ctx, c, obj)
			}}
			// Label-specific allow would deny a fresh snapshot. No automatic unchecked retry is permitted.
			policy := `permit(principal,action in [HyperFleet::Action::"UpdateCluster",HyperFleet::Action::"DeleteCluster",HyperFleet::Action::"UpdateNodePool",HyperFleet::Action::"DeleteNodePool",HyperFleet::Action::"DeleteOIDCConfig"],resource) when { resource.hasTag("team") && resource.getTag("team") == "blue" };`
			srv, store := resourceServer(t, []string{policy}, nil, hooks, objects...)
			w := resourceRequest(srv, tc.method, tc.path, tc.body)
			if w.Code != 409 || writes != 1 {
				t.Fatalf("conflict retried/wrote unchecked state: %d writes=%d %s", w.Code, writes, w.Body.String())
			}
			stored := objects[tc.index].DeepCopyObject().(client.Object)
			if err := store.Get(context.Background(), key, stored); err != nil || stored.GetLabels()["team"] != "red" || stored.GetResourceVersion() == authorizedRV {
				t.Fatalf("newer state lost: labels=%v rv=%s %v", stored.GetLabels(), stored.GetResourceVersion(), err)
			}
			switch obj := stored.(type) {
			case *v1.Cluster:
				if obj.Spec.DisplayName == "requested" {
					t.Fatal("unchecked cluster update persisted")
				}
			case *v1.NodePool:
				if obj.Spec.DisplayName == "requested" {
					t.Fatal("unchecked pool update persisted")
				}
			}
		})
	}
}
