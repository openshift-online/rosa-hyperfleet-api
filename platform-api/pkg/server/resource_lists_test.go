package server

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestResourceCollectionsFilterBeforePaginationAndAbort(t *testing.T) {
	for _, tc := range []struct {
		path, list, describe string
		index                int
	}{
		{"clusters", "ListClusters", "DescribeCluster", 0}, {"nodepools", "ListNodePools", "DescribeNodePool", 1}, {"oidc_configs", "ListOIDCConfigs", "DescribeOIDCConfig", 2}, {"management_clusters", "ListManagementClusters", "DescribeManagementCluster", 3},
	} {
		for _, mode := range []string{"filtered", "list-only", "describe-only", "late-error"} {
			t.Run(tc.path+"/"+mode, func(t *testing.T) {
				objects := resourceFixtures()
				first := objects[tc.index]
				first.SetName("a-visible")
				second := first.DeepCopyObject().(client.Object)
				second.SetName("z-hidden")
				second.SetLabels(map[string]string{"hyperfleet.io/account-id": resourceTestAccount, "team": "red", "fault": "yes"})
				if tc.index == 0 {
					second.SetNamespace("cluster-second")
				}
				if tc.index == 3 {
					second.SetLabels(map[string]string{"team": "red", "fault": "yes"})
				}
				objects = append(objects, second)
				list := actionPermit(tc.list)
				describe := `permit(principal, action == HyperFleet::Action::"` + tc.describe + `", resource) when { resource.hasTag("team") && resource.getTag("team") == "blue" };`
				policies := []string{list, describe}
				if mode == "list-only" {
					policies = []string{list}
				}
				if mode == "describe-only" {
					policies = []string{describe}
				}
				if mode == "late-error" {
					policies = append(policies, `forbid(principal, action == HyperFleet::Action::"`+tc.describe+`", resource) when { resource.hasTag("fault") && 9223372036854775807 + 1 == 0 };`)
				}
				var customer, service []string
				if tc.index == 3 {
					service = policies
				} else {
					customer = policies
				}
				srv, _ := resourceServer(t, customer, service, interceptor.Funcs{}, objects...)
				w := resourceRequest(srv, "GET", tc.path+"?limit=1&offset=0", "")
				want := 200
				if mode == "describe-only" {
					want = 403
				}
				if mode == "late-error" {
					want = 500
				}
				if w.Code != want {
					t.Fatalf("want %d got %d: %s", want, w.Code, w.Body.String())
				}
				var body map[string]json.RawMessage
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
					t.Fatal(err)
				}
				if want != 200 {
					if _, ok := body["items"]; ok {
						t.Fatalf("partial success after error: %s", w.Body.String())
					}
					return
				}
				var total int
				var items []json.RawMessage
				_ = json.Unmarshal(body["total"], &total)
				_ = json.Unmarshal(body["items"], &items)
				expected := 1
				if mode == "list-only" {
					expected = 0
				}
				if total != expected || len(items) != expected {
					t.Fatalf("filter before total/page failed: %s", w.Body.String())
				}
				if tc.index != 3 {
					w = resourceRequest(srv, "GET", tc.path+"?limit=1&offset=1", "")
					_ = json.Unmarshal(w.Body.Bytes(), &body)
					_ = json.Unmarshal(body["items"], &items)
					_ = json.Unmarshal(body["total"], &total)
					if total != expected || len(items) != 0 {
						t.Fatalf("offset counted hidden item: %s", w.Body.String())
					}
				}
			})
		}
	}
}

func TestNodePoolFirstMatchIsNotAuthorizedAlternate(t *testing.T) {
	const secondID = "550e8400-e29b-41d4-a716-446655440001"
	objects := resourceFixtures()
	second := objects[1].DeepCopyObject().(*v1.NodePool)
	second.Namespace = "cluster-" + secondID
	parent := objects[0].DeepCopyObject().(*v1.Cluster)
	parent.Namespace = second.Namespace
	parent.Name = "second-parent"
	objects = append(objects, second, parent)
	hooks := interceptor.Funcs{List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
		if err := c.List(ctx, list, opts...); err != nil {
			return err
		}
		if pools, ok := list.(*v1.NodePoolList); ok {
			sort.Slice(pools.Items, func(i, j int) bool { return pools.Items[i].Namespace < pools.Items[j].Namespace })
		}
		return nil
	}}
	policy := `permit(principal,action == HyperFleet::Action::"DescribeNodePool",resource in HyperFleet::Cluster::"123456789012/us-east-1/` + secondID + `");`
	srv, _ := resourceServer(t, []string{policy}, nil, hooks, objects...)
	for _, tc := range []struct {
		query string
		want  int
	}{{"", 403}, {"?clusterId=" + resourceTestClusterID, 403}, {"?clusterId=" + secondID, 200}} {
		w := resourceRequest(srv, "GET", "nodepools/workers"+tc.query, "")
		if w.Code != tc.want {
			t.Fatalf("query %s want %d got %d: %s", tc.query, tc.want, w.Code, w.Body.String())
		}
	}
	// List has independent collection permission; it filters actual selected parents.
	srv, _ = resourceServer(t, []string{actionPermit("ListNodePools"), policy}, nil, hooks, objects...)
	w := resourceRequest(srv, "GET", "nodepools?limit=1", "")
	var result struct {
		Items []struct{ Metadata struct{ Namespace string } }
		Total int
	}
	_ = json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Total != 1 || len(result.Items) != 1 || result.Items[0].Metadata.Namespace != second.Namespace {
		t.Fatalf("wrong per-parent list filter: %s", w.Body.String())
	}
}

func TestNodePoolParentFailureAbortsList(t *testing.T) {
	objects := resourceFixtures()
	orphan := objects[1].DeepCopyObject().(*v1.NodePool)
	orphan.Name = "z-orphan"
	orphan.Namespace = "cluster-missing"
	objects = append(objects, orphan)
	srv, _ := resourceServer(t, []string{`permit(principal,action in HyperFleet::Action::"ReadOnly",resource);`}, nil, interceptor.Funcs{}, objects...)
	w := resourceRequest(srv, "GET", "nodepools?limit=1", "")
	if w.Code != 500 || strings.Contains(w.Body.String(), `"items"`) {
		t.Fatalf("unchecked parent yielded partial success: %s", w.Body.String())
	}
}

func TestCreatesUseValidatedCandidateLabelsAndOwnership(t *testing.T) {
	for _, tc := range []struct{ action, path, body string }{
		{"CreateCluster", "clusters", `{"metadata":{"name":"new","labels":{"team":"blue","hyperfleet.io/account-id":"222222222222"}},"spec":{"displayName":"new"}}`},
		{"CreateNodePool", "nodepools?clusterId=foreign", `{"metadata":{"name":"new","namespace":"cluster-` + resourceTestClusterID + `","labels":{"team":"blue","hyperfleet.io/account-id":"222222222222"}},"spec":{"displayName":"new"}}`},
		{"CreateOIDCConfig", "oidc_configs", `{"metadata":{"labels":{"team":"blue","hyperfleet.io/account-id":"222222222222","hyperfleet.io/cluster-namespace":"forged"}},"spec":{"type":"unmanaged","issuerUrl":"https://example.com","secretArn":"secret","installerRoleArn":"role"}}`},
	} {
		for _, label := range []string{"blue", "red"} {
			t.Run(tc.action+"/"+label, func(t *testing.T) {
				policy := `permit(principal,action == HyperFleet::Action::"` + tc.action + `",resource) when { resource.account == "123456789012" && resource.hasTag("team") && resource.getTag("team") == "` + label + `" };`
				srv, _ := resourceServer(t, []string{policy}, nil, interceptor.Funcs{}, resourceFixtures()...)
				w := resourceRequest(srv, "POST", tc.path, tc.body)
				want := 201
				if label == "red" {
					want = 403
				}
				if w.Code != want {
					t.Fatalf("candidate tags/identity want %d got %d: %s", want, w.Code, w.Body.String())
				}
			})
		}
	}
}

func TestDeniedClusterCreateDoesNotClaimOIDC(t *testing.T) {
	objects := resourceFixtures()
	oc := objects[2].(*v1.OidcConfig)
	oc.Spec.Type = v1.OidcConfigTypeManaged
	oc.Spec.IssuerUrl = "https://example.com/config"
	writes := 0
	hooks := interceptor.Funcs{Update: func(context.Context, client.WithWatch, client.Object, ...client.UpdateOption) error {
		writes++
		return fmt.Errorf("unexpected OIDC write")
	}, Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
		writes++
		return fmt.Errorf("unexpected create")
	}}
	srv, store := resourceServer(t, nil, nil, hooks, objects...)
	w := resourceRequest(srv, "POST", "clusters", `{"metadata":{"name":"new"},"spec":{"oidcConfigId":"config"}}`)
	if w.Code != 403 || writes != 0 {
		t.Fatalf("denied Create claimed OIDC: %d writes=%d %s", w.Code, writes, w.Body.String())
	}
	var stored v1.OidcConfig
	if err := store.Get(context.Background(), client.ObjectKeyFromObject(oc), &stored); err != nil || stored.Labels["hyperfleet.io/cluster-namespace"] != "" {
		t.Fatalf("claim changed: %+v %v", stored, err)
	}
}

func TestUnsupportedMethodsStayTyped405(t *testing.T) {
	srv, _ := resourceServer(t, nil, nil, interceptor.Funcs{}, resourceFixtures()...)
	for _, tc := range []struct{ method, path string }{{"PATCH", "nodepools/workers"}, {"PUT", "oidc_configs/config"}, {"DELETE", "management_clusters/mc"}, {"PUT", "management_clusters/mc"}} {
		w := resourceRequest(srv, tc.method, tc.path, `{}`)
		var status metav1.Status
		if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || w.Code != 405 || status.Kind != "Status" {
			t.Fatalf("unsupported route changed: %s %s %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
}
