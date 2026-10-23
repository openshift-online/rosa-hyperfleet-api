package server

import (
	"context"
	"fmt"
	"strings"
	"testing"

	v1 "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// Parent authorization loading failures on retry must replace, not accompany, earlier allows.
func TestRetryAuthzFailure(t *testing.T) {
	// failingRead is the retry's authorization parent load; updates also check before validation.
	for _, tc := range []struct {
		path, body, operation string
		failingRead           int
	}{
		{"nodepools/parent.workers", `{"spec":{"labels":{"change":"requested"}}}`, "UpdateNodePool", 3},
		{"nodepools/parent.workers/labels", `{"change":"requested"}`, "LabelResource", 2},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			objects := resourceFixtures()
			reads, writes := 0, 0
			hooks := interceptor.Funcs{
				Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if _, ok := obj.(*v1.Cluster); ok {
						reads++
						if reads == tc.failingRead {
							return fmt.Errorf("private parent load failure")
						}
					}
					return c.Get(ctx, key, obj, opts...)
				},
				Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
					writes++
					return apierrors.NewConflict(v1.GroupVersion.WithResource("nodepools").GroupResource(), obj.GetName(), nil)
				},
			}
			srv, _ := resourceServer(t, []string{`permit(principal,action,resource);`}, hooks, objects...)
			before := authzMetricSamples(t)
			method := "PUT"
			if strings.HasSuffix(tc.path, "/labels") {
				method = "PATCH"
			}
			w := resourceRequest(srv, method, tc.path, tc.body)
			after := authzMetricSamples(t)
			if w.Code != 500 || writes != 1 || !strings.Contains(w.Body.String(), "AUTHZ-FAILED-001") || strings.Contains(w.Body.String(), "private") {
				t.Fatalf("status=%d writes=%d body=%s", w.Code, writes, w.Body.String())
			}
			assertAuthzOutcome(t, before, after, tc.operation, "error")
			key := "authz_failures_total/operation=" + tc.operation + "/stage=resource_loading"
			if after[key]-before[key] != 1 {
				t.Fatalf("failure stage delta=%v", after[key]-before[key])
			}
		})
	}
}

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
		{"PATCH", "nodepools/parent.workers", poolChange, 1, "red", 1, 403, 1},
		{"PUT", "clusters/parent", clusterChange, 0, "blue", 1, 200, 2},
		{"PATCH", "clusters/parent", clusterChange, 0, "blue", 1, 200, 2},
		{"PUT", "nodepools/parent.workers", poolChange, 1, "blue", 1, 200, 2},
		{"PATCH", "nodepools/parent.workers", poolChange, 1, "blue", 1, 200, 2},
		{"PUT", "clusters/parent", clusterChange, 0, "blue", 99, 409, retry.DefaultRetry.Steps},
		{"PUT", "nodepools/parent.workers", poolChange, 1, "blue", 99, 409, retry.DefaultRetry.Steps},
		{"PATCH", "nodepools/parent.workers", poolChange, 1, "blue", 99, 409, retry.DefaultRetry.Steps},
		{"PATCH", "clusters/parent/labels", `{"change":"requested","absent":null}`, 0, "blue", 1, 204, 2},
		{"PATCH", "nodepools/parent.workers/labels", `{"change":"requested"}`, 1, "blue", 1, 204, 2},
		{"PATCH", "oidc_configs/config/labels", `{"change":"requested"}`, 2, "blue", 1, 204, 2},
		{"PATCH", "dns_reservations/zone/labels", `{"change":"requested"}`, 3, "blue", 1, 204, 2},
		{"PATCH", "clusters/parent/labels", `{"change":"requested"}`, 0, "red", 1, 403, 1},
		{"PATCH", "clusters/parent/labels", `{"change":"requested"}`, 0, "blue", 99, 409, retry.DefaultRetry.Steps},
		{"DELETE", "dns_reservations/zone", "", 3, "red", 1, 403, 1},
		{"DELETE", "clusters/parent", "", 0, "blue", 1, 409, 1},
		{"DELETE", "clusters/parent", "", 0, "red", 1, 409, 1},
		{"DELETE", "nodepools/parent.workers", "", 1, "red", 1, 409, 1},
		{"DELETE", "oidc_configs/config", "", 2, "red", 1, 409, 1},
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
				tags := competitor.GetAnnotations()
				tags["team"], tags["competitor"], tags["change"] = tc.competitorTeam, "wrote", "competitor"
				competitor.SetAnnotations(tags)
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
			policy := `permit(principal,action in [Seaplane::Action::"UpdateCluster",Seaplane::Action::"DeleteCluster",Seaplane::Action::"UpdateNodePool",Seaplane::Action::"DeleteNodePool",Seaplane::Action::"DeleteOIDCConfig",Seaplane::Action::"LabelResource",Seaplane::Action::"UnlabelResource",Seaplane::Action::"DeleteDNSReservation"],resource) when { resource.hasTag("team") && resource.getTag("team") == "blue" };`
			srv, store := resourceServer(t, []string{policy}, hooks, objects...)
			before := authzMetricSamples(t)
			w := resourceRequest(srv, tc.method, tc.path, tc.body)
			after := authzMetricSamples(t)
			kind := map[string]string{"clusters": "Cluster", "nodepools": "NodePool", "oidc_configs": "OIDCConfig", "dns_reservations": "DNSReservation"}[strings.Split(tc.path, "/")[0]]
			operation := "Update" + kind
			if tc.method == "DELETE" {
				operation = "Delete" + kind
			}
			if strings.HasSuffix(tc.path, "/labels") {
				operation = "LabelResource"
			}
			outcome := "allow"
			if tc.want == 403 {
				outcome = "deny"
			}
			assertAuthzOutcome(t, before, after, operation, outcome)
			if w.Code != tc.want || writes != tc.writes {
				t.Fatalf("want %d after %d writes, got %d after %d: %s", tc.want, tc.writes, w.Code, writes, w.Body.String())
			}
			if tc.want == 409 && tc.method != "DELETE" && !strings.HasSuffix(tc.path, "/labels") && !strings.Contains(w.Body.String(), "-MGMT-UPDATE-006") {
				t.Fatalf("exhausted update retries must return the upstream conflict code: %s", w.Body.String())
			}
			stored := objects[tc.index].DeepCopyObject().(client.Object)
			if err := store.Get(context.Background(), key, stored); err != nil || stored.GetAnnotations()["competitor"] != "wrote" || stored.GetAnnotations()["team"] != tc.competitorTeam {
				t.Fatalf("newer state lost: tags=%v %v", stored.GetAnnotations(), err)
			}
			changed := false
			switch obj := stored.(type) {
			case *v1.Cluster:
				changed = obj.Spec.Properties["change"] == "requested"
			case *v1.NodePool:
				changed = obj.Spec.Labels["change"] == "requested"
			}
			if strings.HasSuffix(tc.path, "/labels") {
				changed = stored.GetAnnotations()["change"] == "requested"
			}
			if changed != (tc.want == 200 || tc.want == 204) {
				t.Fatalf("requested change persisted = %v, want %v", changed, tc.want == 200)
			}
			// A client retry after a delete conflict must check the competitor's new labels.
			if tc.method == "DELETE" && tc.competitorTeam == "red" {
				w = resourceRequest(srv, tc.method, tc.path, tc.body)
				if w.Code != 403 || writes != tc.writes || !strings.Contains(w.Body.String(), "AUTHZ-DENIED-001") {
					t.Fatalf("retry wrote unauthorized state: status=%d writes=%d body=%s", w.Code, writes, w.Body.String())
				}
			}
		})
	}
}
