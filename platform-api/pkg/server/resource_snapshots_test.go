package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/config"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/middleware"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

const resourceTestAccount = "123456789012"
const resourceTestClusterID = "550e8400-e29b-41d4-a716-446655440000"

func resourceServer(t *testing.T, customer, service []string, hooks interceptor.Funcs, objects ...client.Object) (*Server, client.Client) {
	t.Helper()
	bundle := map[string]any{"formatVersion": 1, "registeredAccounts": []string{resourceTestAccount}, "policies": []any{}, "attachments": []any{}}
	for _, domain := range []struct {
		policies, attachments, prefix string
		content                       []string
	}{{"policies", "attachments", "customer", customer}, {"serviceOperatorPolicies", "serviceOperatorAttachments", "service", service}} {
		if domain.content == nil && domain.prefix == "service" {
			continue
		}
		policies, attachments := []any{}, []any{}
		for i, content := range domain.content {
			id := fmt.Sprintf("%s-%d", domain.prefix, i)
			policies = append(policies, map[string]any{"id": id, "ownerAccountID": resourceTestAccount, "content": content})
			attachments = append(attachments, map[string]any{"id": id, "policyID": id, "principalARN": "arn:aws:iam::" + resourceTestAccount + ":user/test", "scope": "global"})
		}
		bundle[domain.policies] = policies
		bundle[domain.attachments] = attachments
	}
	content, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "authz.json")
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	authorizer, err := authz.LoadConfig(path, "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	scheme := runtime.NewScheme()
	if err := v1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	fc := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(hooks).Build()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := config.NewConfig()
	cfg.Regional.AWSRegion = "us-east-1"
	srv, err := New(cfg, hyperfleetdb.NewClientFrom(fc, logger), authorizer, logger)
	if err != nil {
		t.Fatal(err)
	}
	return srv, fc
}
func resourceRequest(srv *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/api/v0/"+path, strings.NewReader(body))
	r.Header.Set(middleware.HeaderAccountID, resourceTestAccount)
	r.Header.Set(middleware.HeaderCallerARN, "arn:aws:iam::"+resourceTestAccount+":user/test")
	w := httptest.NewRecorder()
	srv.apiServer.Handler.ServeHTTP(w, r)
	return w
}
func resourceFixtures() []client.Object {
	labels := map[string]string{"hyperfleet.io/account-id": resourceTestAccount, "team": "blue"}
	return []client.Object{
		&v1.Cluster{ObjectMeta: metav1.ObjectMeta{Name: "parent", Namespace: "cluster-" + resourceTestClusterID, Labels: labels}},
		&v1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "workers", Namespace: "cluster-" + resourceTestClusterID, Labels: labels}},
		&v1.OidcConfig{ObjectMeta: metav1.ObjectMeta{Name: "config", Namespace: "account-" + resourceTestAccount, Labels: labels}},
		&v1.ManagementCluster{ObjectMeta: metav1.ObjectMeta{Name: "mc", Labels: map[string]string{"team": "blue"}}, Spec: v1.ManagementClusterSpec{AccountID: "222222222222", Region: "us-west-2"}},
	}
}
func actionPermit(action string) string {
	return `permit(principal, action == HyperFleet::Action::"` + action + `", resource);`
}

func TestImplementedRoutesActionSpecific(t *testing.T) {
	for _, tc := range []struct {
		action, method, path, body string
		status                     int
	}{
		{"ListClusters", "GET", "clusters", "", 200}, {"DescribeCluster", "GET", "clusters/" + resourceTestClusterID, "", 200}, {"CreateCluster", "POST", "clusters", `{"metadata":{"name":"new"},"spec":{"displayName":"new"}}`, 201}, {"UpdateCluster", "PUT", "clusters/" + resourceTestClusterID, `{"spec":{"displayName":"new"}}`, 200}, {"UpdateCluster", "PATCH", "clusters/" + resourceTestClusterID, `{"spec":{"displayName":"new"}}`, 200}, {"DeleteCluster", "DELETE", "clusters/" + resourceTestClusterID, "", 202},
		{"ListNodePools", "GET", "nodepools", "", 200}, {"DescribeNodePool", "GET", "nodepools/workers", "", 200}, {"CreateNodePool", "POST", "nodepools", `{"metadata":{"name":"new","namespace":"cluster-` + resourceTestClusterID + `"},"spec":{"displayName":"new"}}`, 201}, {"UpdateNodePool", "PUT", "nodepools/workers", `{"spec":{"displayName":"new"}}`, 200}, {"DeleteNodePool", "DELETE", "nodepools/workers", "", 202},
		{"ListOIDCConfigs", "GET", "oidc_configs", "", 200}, {"DescribeOIDCConfig", "GET", "oidc_configs/config", "", 200}, {"CreateOIDCConfig", "POST", "oidc_configs", `{"spec":{"type":"unmanaged","issuerUrl":"https://example.com","secretArn":"secret","installerRoleArn":"role"}}`, 201}, {"DeleteOIDCConfig", "DELETE", "oidc_configs/config", "", 202},
		{"ListManagementClusters", "GET", "management_clusters", "", 200}, {"DescribeManagementCluster", "GET", "management_clusters/mc", "", 200}, {"CreateManagementCluster", "POST", "management_clusters", `{"id":"new","accountId":"222222222222","region":"us-west-2"}`, 201},
	} {
		for _, grant := range []string{"matching", "wrong action", "unbound principal", "runtime error"} {
			t.Run(fmt.Sprintf("%s %s grant=%s", tc.method, tc.path, grant), func(t *testing.T) {
				canAccess := grant == "matching"
				action := tc.action
				if grant == "wrong action" {
					action = "CreateCluster"
					if tc.action == action {
						action = "DescribeCluster"
					}
				}
				policies := []string{actionPermit(action)}
				if grant == "runtime error" {
					policies = append(policies, `forbid(principal, action == HyperFleet::Action::"`+action+`", resource) when { 9223372036854775807 + 1 == 0 };`)
				}
				var customer, service []string
				if strings.Contains(tc.action, "ManagementCluster") {
					service = policies
				} else {
					customer = policies
				}
				writes := 0
				hooks := interceptor.Funcs{
					Create: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.CreateOption) error {
						writes++
						return c.Create(ctx, o, opts...)
					},
					Update: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.UpdateOption) error {
						writes++
						return c.Update(ctx, o, opts...)
					},
					Delete: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.DeleteOption) error {
						writes++
						return c.Delete(ctx, o, opts...)
					},
				}
				srv, _ := resourceServer(t, customer, service, hooks, resourceFixtures()...)
				r := httptest.NewRequest(tc.method, "/api/v0/"+tc.path, strings.NewReader(tc.body))
				r.Header.Set(middleware.HeaderAccountID, resourceTestAccount)
				principal := "test"
				if grant == "unbound principal" {
					principal = "no-grants"
				}
				r.Header.Set(middleware.HeaderCallerARN, "arn:aws:iam::"+resourceTestAccount+":user/"+principal)
				before := authzMetricSamples(t)
				w := httptest.NewRecorder()
				srv.apiServer.Handler.ServeHTTP(w, r)
				want := tc.status
				if !canAccess {
					want = 403
				}
				if grant == "runtime error" {
					want = 500
				}
				if w.Code != want {
					t.Fatalf("want %d got %d: %s", want, w.Code, w.Body.String())
				}
				if grant == "runtime error" {
					var status metav1.Status
					if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || status.Kind != "Status" || status.Code != 500 || status.Message != "AUTHZ-FAILED-001: Authorization failed" || status.Details != nil || strings.Contains(w.Body.String(), `"items"`) {
						t.Fatalf("unsafe runtime error response: %s (%v)", w.Body.String(), err)
					}
					after := authzMetricSamples(t)
					for _, key := range []string{"authz_requests_total/operation=" + action + "/outcome=error", "authz_duration_seconds/operation=" + action + "/outcome=error", "authz_failures_total/operation=" + action + "/stage=evaluation"} {
						if after[key]-before[key] != 1 {
							t.Fatalf("runtime failure sample %s increased by %v, want 1", key, after[key]-before[key])
						}
					}
				}
				expectedWrites := 0
				if canAccess && tc.method != "GET" {
					expectedWrites = 1
				}
				if writes != expectedWrites {
					t.Fatalf("writes=%d want=%d", writes, expectedWrites)
				}
				if strings.HasPrefix(tc.action, "List") && canAccess {
					var b struct{ Total int }
					if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil || b.Total != 0 {
						t.Fatalf("List without Describe must return empty: %s (%v)", w.Body.String(), err)
					}
				}
			})
		}
	}
}

func TestEveryChangedUpdateActionBeforeWrite(t *testing.T) {
	for _, tc := range []struct{ method, path, body, base, extra string }{
		{"PUT", "clusters/" + resourceTestClusterID, `{"spec":{"hostedCluster":{"release":{"image":"new"}}}}`, "UpdateCluster", "UpdateClusterVersion"},
		{"PATCH", "clusters/" + resourceTestClusterID, `{"spec":{"hostedCluster":{"release":{"image":"new"}}}}`, "UpdateCluster", "UpdateClusterVersion"},
		{"PUT", "clusters/" + resourceTestClusterID, `{"spec":{"hostedCluster":{"configuration":{"proxy":{"httpProxy":"http://new"}}}}}`, "UpdateCluster", "UpdateClusterConfig"},
		{"PATCH", "clusters/" + resourceTestClusterID, `{"spec":{"hostedCluster":{"configuration":{"proxy":{"httpProxy":"http://new"}}}}}`, "UpdateCluster", "UpdateClusterConfig"},
		{"PUT", "nodepools/workers", `{"spec":{"nodePool":{"replicas":0,"release":{"image":"new"}}}}`, "UpdateNodePool", "ScaleNodePool"},
	} {
		missingActions := []string{"", tc.base, tc.extra}
		if tc.base == "UpdateNodePool" {
			missingActions = append(missingActions, "UpdateNodePoolVersion")
		}
		for _, missing := range missingActions {
			t.Run(tc.method+tc.path+" missing="+missing, func(t *testing.T) {
				objects := resourceFixtures()
				replicas := int32(3)
				objects[1].(*v1.NodePool).Spec.NodePool.Replicas = &replicas
				permits := []string{}
				actions := []string{tc.base, tc.extra}
				if tc.base == "UpdateNodePool" {
					actions = append(actions, "UpdateNodePoolVersion")
				}
				for _, action := range actions {
					if action != missing {
						permits = append(permits, actionPermit(action))
					}
				}
				writes := 0
				srv, store := resourceServer(t, permits, nil, interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, o client.Object, opts ...client.UpdateOption) error {
					writes++
					return c.Update(ctx, o, opts...)
				}}, objects...)
				w := resourceRequest(srv, tc.method, tc.path, tc.body)
				want := 200
				if missing != "" {
					want = 403
				}
				if w.Code != want {
					t.Fatalf("want %d got %d: %s", want, w.Code, w.Body.String())
				}
				if missing != "" && writes != 0 {
					t.Fatalf("denied update wrote %d times", writes)
				}
				if missing != "" {
					var np v1.NodePool
					if err := store.Get(context.Background(), client.ObjectKeyFromObject(objects[1]), &np); err != nil || *np.Spec.NodePool.Replicas != 3 {
						t.Fatalf("denial changed stored candidate: %+v %v", np, err)
					}
				}
			})
		}
	}
}
