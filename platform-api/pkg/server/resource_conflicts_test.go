package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// A conflict never writes unchecked state. Updates retry like upstream, re-reading
// and re-authorizing each attempt; deletes return 409 without retrying.
func TestAuthorizedSnapshotConflict(t *testing.T) {
	const (
		clusterChange = `{"spec":{"properties":{"change":"requested"}}}`
		poolChange    = `{"spec":{"labels":{"change":"requested"}}}`
	)
	for _, tc := range []struct {
		method, path, body string
		index              int
		competitorTeam     string
		losses             int // writes that lose to the competitor
		want, writes       int
	}{
		{"PUT", "clusters/parent", clusterChange, 0, "red", 1, 403, 1},
		{"PATCH", "clusters/parent", clusterChange, 0, "red", 1, 403, 1},
		{"PUT", "nodepools/parent.workers", poolChange, 1, "red", 1, 403, 1},
		{"PUT", "clusters/parent", clusterChange, 0, "blue", 1, 200, 2},
		{"PATCH", "clusters/parent", clusterChange, 0, "blue", 1, 200, 2},
		{"PUT", "nodepools/parent.workers", poolChange, 1, "blue", 1, 200, 2},
		{"PUT", "clusters/parent", clusterChange, 0, "blue", 99, 409, retry.DefaultRetry.Steps},
		{"PUT", "nodepools/parent.workers", poolChange, 1, "blue", 99, 409, retry.DefaultRetry.Steps},
		{"DELETE", "clusters/parent", "", 0, "blue", 1, 409, 1},
		{"DELETE", "nodepools/parent.workers", "", 1, "blue", 1, 409, 1},
		{"DELETE", "oidc_configs/config", "", 2, "blue", 1, 409, 1},
	} {
		t.Run(fmt.Sprintf("%s%s/%s/%d", tc.method, tc.path, tc.competitorTeam, tc.losses), func(t *testing.T) {
			objects := resourceFixtures()
			key := client.ObjectKeyFromObject(objects[tc.index])
			writes := 0
			var checkedRV string
			// The first tc.losses writes lose to a competitor that relabels the object; later writes go through.
			compete := func(ctx context.Context, c client.WithWatch, obj client.Object, write func() error) error {
				writes++
				if rv := obj.GetResourceVersion(); rv == "" || rv == "0" || rv == checkedRV {
					t.Fatalf("write %d did not use a freshly checked version: %q", writes, rv)
				}
				if writes > tc.losses {
					return write()
				}
				checkedRV = obj.GetResourceVersion()
				competitor := objects[tc.index].DeepCopyObject().(client.Object)
				if err := c.Get(ctx, key, competitor); err != nil {
					return err
				}
				labels := competitor.GetLabels()
				labels["team"], labels["competitor"] = tc.competitorTeam, "wrote"
				competitor.SetLabels(labels)
				if err := c.Update(ctx, competitor); err != nil {
					return err
				}
				return apierrors.NewConflict(v1.GroupVersion.WithResource("resources").GroupResource(), obj.GetName(), fmt.Errorf("checked version lost"))
			}
			hooks := interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
				return compete(ctx, c, obj, func() error { return c.Update(ctx, obj, opts...) })
			}, Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				return compete(ctx, c, obj, func() error { return c.Delete(ctx, obj, opts...) })
			}}
			// Only a blue snapshot is allowed, so a red competitor makes the fresh snapshot deny.
			policy := `permit(principal,action in [HyperFleet::Action::"UpdateCluster",HyperFleet::Action::"DeleteCluster",HyperFleet::Action::"UpdateNodePool",HyperFleet::Action::"DeleteNodePool",HyperFleet::Action::"DeleteOIDCConfig"],resource) when { resource.hasTag("team") && resource.getTag("team") == "blue" };`
			srv, store := resourceServer(t, []string{policy}, nil, hooks, objects...)
			w := resourceRequest(srv, tc.method, tc.path, tc.body)
			if w.Code != tc.want || writes != tc.writes {
				t.Fatalf("want %d after %d writes, got %d after %d: %s", tc.want, tc.writes, w.Code, writes, w.Body.String())
			}
			if tc.want == 409 && tc.method != "DELETE" && !strings.Contains(w.Body.String(), "-MGMT-UPDATE-006") {
				t.Fatalf("exhausted update retries must return the upstream conflict code: %s", w.Body.String())
			}
			stored := objects[tc.index].DeepCopyObject().(client.Object)
			if err := store.Get(context.Background(), key, stored); err != nil || stored.GetLabels()["competitor"] != "wrote" || stored.GetLabels()["team"] != tc.competitorTeam {
				t.Fatalf("newer state lost: labels=%v %v", stored.GetLabels(), err)
			}
			changed := false
			switch obj := stored.(type) {
			case *v1.Cluster:
				changed = obj.Spec.Properties["change"] == "requested"
			case *v1.NodePool:
				changed = obj.Spec.Labels["change"] == "requested"
			}
			if changed != (tc.want == 200) {
				t.Fatalf("requested change persisted = %v, want %v", changed, tc.want == 200)
			}
		})
	}
}
