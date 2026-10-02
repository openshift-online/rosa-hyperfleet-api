//go:build integration

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
)

const (
	clusterAuthzRegion    = "us-east-1"
	clusterReadPermit     = `permit(principal, action in HyperFleet::Action::"ReadOnly", resource);`
	clusterListPermit     = `permit(principal, action == HyperFleet::Action::"ListClusters", resource);`
	clusterDescribePermit = `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource);`
	clusterLabelPermit    = `permit(principal, action == HyperFleet::Action::"DescribeCluster", resource)
when { resource.hasTag("example.com/team") && resource.getTag("example.com/team") == "blue" };`
	clusterFailingForbid = `forbid(principal, action == HyperFleet::Action::"DescribeCluster", resource)
when { resource.hasTag("example.com/fault") && resource.getTag("example.com/fault") == "fault-detail-secret" && 9223372036854775807 + 1 == 0 };`
)

type clusterResolverFunc func(context.Context, authz.Identity) ([]authz.ResolvedBinding, error)

func (f clusterResolverFunc) Resolve(ctx context.Context, id authz.Identity) ([]authz.ResolvedBinding, error) {
	return f(ctx, id)
}

func clusterBindings(id authz.Identity, policies ...string) []authz.ResolvedBinding {
	bindings := make([]authz.ResolvedBinding, 0, len(policies))
	for i, policy := range policies {
		attachment := fmt.Sprintf("grant-%d", i)
		bindings = append(bindings, authz.ResolvedBinding{
			Provenance: authz.Provenance{
				DiagnosticID: "attachment/" + attachment, PolicyID: "read", PolicyRevision: "test-revision",
				AttachmentID: attachment, AttachmentRevision: "test-revision", PrincipalARN: id.CallerARN, Scope: "global",
			},
			OwnerAccountID: id.AccountID, PolicyContent: policy, BindingMode: "exact-principal", Caller: id,
		})
	}
	return bindings
}

func clusterAuthzFrom(t *testing.T, resolver authz.PolicyResolver) *authz.Authorizer {
	t.Helper()
	a, err := authz.NewAuthorizer(resolver)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func clusterAuthorizer(t *testing.T, policies ...string) *authz.Authorizer {
	t.Helper()
	return clusterAuthzFrom(t, clusterResolverFunc(func(_ context.Context, id authz.Identity) ([]authz.ResolvedBinding, error) {
		return clusterBindings(id, policies...), nil
	}))
}

func clusterAuthzFixture(t *testing.T, fc client.Client, a *authz.Authorizer) (*ClusterHandler, *prometheus.Registry, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	h := NewClusterHandler(hyperfleetdb.NewClientFrom(fc, logger), "", 0, a, clusterAuthzRegion, logger)
	registry := prometheus.NewRegistry()
	metrics, err := authz.NewMetrics(registry)
	if err != nil {
		t.Fatal(err)
	}
	h.metrics = metrics
	return h, registry, &logs
}

// The fake still applies real storage selectors. Only its output order is controlled.
func clusterOrderedStore(clusters []*hyperfleetv1alpha1.Cluster, beforeList func(context.Context) error) client.WithWatch {
	objects := make([]client.Object, 0, len(clusters))
	for _, cr := range clusters {
		objects = append(objects, cr)
	}
	return fake.NewClientBuilder().WithScheme(newTestScheme()).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, obj client.ObjectList, opts ...client.ListOption) error {
			if beforeList != nil {
				if err := beforeList(ctx); err != nil {
					return err
				}
			}
			if err := c.List(ctx, obj, opts...); err != nil {
				return err
			}
			list := obj.(*hyperfleetv1alpha1.ClusterList)
			scoped := make(map[client.ObjectKey]hyperfleetv1alpha1.Cluster, len(list.Items))
			for _, cr := range list.Items {
				scoped[client.ObjectKeyFromObject(&cr)] = cr
			}
			list.Items = nil
			for _, cr := range clusters {
				if stored, exists := scoped[client.ObjectKeyFromObject(cr)]; exists {
					list.Items = append(list.Items, stored)
				}
			}
			return nil
		},
	}).Build()
}

func clusterAuthzRequest(h *ClusterHandler, operation authz.Action, query, id string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "/api/v0/clusters"+query, nil).WithContext(testContext(testAccountID))
	r = mux.SetURLVars(r, map[string]string{"id": id})
	w := httptest.NewRecorder()
	if operation == authz.ListClusters {
		h.List(w, r)
	} else {
		h.Get(w, r)
	}
	return w
}

func assertClusterAuthzStatus(t *testing.T, w *httptest.ResponseRecorder, want int, message string) {
	t.Helper()
	var status metav1.Status
	if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil {
		t.Fatalf("invalid Status body: %v, %s", err, w.Body.String())
	}
	if w.Code != want || status.Code != int32(want) || status.Kind != "Status" || status.APIVersion != "v1" || status.Status != metav1.StatusFailure || status.Message != message || status.Details != nil {
		t.Fatalf("want safe %d Status %q without details, got HTTP %d %+v", want, message, w.Code, status)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("unexpected Content-Type: %v", w.Header())
	}
}

func assertClusterAuthzMetrics(t *testing.T, registry *prometheus.Registry, operation authz.Action, outcome authz.Outcome, stage authz.Stage) {
	t.Helper()
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	var requests, samples, failures float64
	for _, family := range families {
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["operation"] != string(operation) {
				t.Errorf("unexpected operation: %v", labels)
			}
			switch family.GetName() {
			case "authz_requests_total":
				requests += metric.GetCounter().GetValue()
				if len(labels) != 2 || labels["outcome"] != string(outcome) {
					t.Errorf("unexpected request labels: %v", labels)
				}
			case "authz_duration_seconds":
				samples += float64(metric.GetHistogram().GetSampleCount())
				if len(labels) != 2 || labels["outcome"] != string(outcome) || metric.GetHistogram().GetSampleSum() <= 0 {
					t.Errorf("unexpected duration: %v, %+v", labels, metric.GetHistogram())
				}
			case "authz_failures_total":
				failures += metric.GetCounter().GetValue()
				if len(labels) != 2 || labels["stage"] != string(stage) {
					t.Errorf("unexpected failure labels: %v", labels)
				}
			default:
				t.Errorf("unexpected collector: %s", family.GetName())
			}
		}
	}
	wantFailures := float64(0)
	if outcome == authz.OutcomeError {
		wantFailures = 1
	}
	if requests != 1 || samples != 1 || failures != wantFailures {
		t.Fatalf("want one request/sample and %v failures, got %v/%v/%v", wantFailures, requests, samples, failures)
	}
}

func TestClusterAuthz_NoGrants(t *testing.T) {
	for _, operation := range []authz.Action{authz.ListClusters, authz.DescribeCluster} {
		t.Run(string(operation), func(t *testing.T) {
			reads := 0
			fc := clusterOrderedStore([]*hyperfleetv1alpha1.Cluster{testClusterCR("owned-id", "owned", testAccountID)}, func(context.Context) error {
				reads++
				return nil
			})
			h, registry, _ := clusterAuthzFixture(t, fc, clusterAuthorizer(t))
			w := clusterAuthzRequest(h, operation, "", "owned-id")
			assertClusterAuthzStatus(t, w, http.StatusForbidden, "AUTHZ-DENIED-001: Access denied")
			assertClusterAuthzMetrics(t, registry, operation, authz.OutcomeDeny, authz.StageNone)
			wantReads := 0
			if operation == authz.DescribeCluster {
				wantReads = 1
			}
			if reads != wantReads {
				t.Fatalf("storage reads = %d, want %d", reads, wantReads)
			}
		})
	}
}

func TestClusterAuthz_ListVisibility(t *testing.T) {
	hidden := testClusterCR("hidden-first", "hidden", testAccountID)
	hidden.Labels["example.com/team"] = "red"
	first := testClusterCR("visible-first", "first", testAccountID)
	first.Labels["example.com/team"] = "blue"
	missing := testClusterCR("missing-label", "missing", testAccountID)
	second := testClusterCR("visible-second", "second", testAccountID)
	second.Labels["example.com/team"] = "blue"
	foreign := testClusterCR("foreign", "foreign", "999999999999")
	foreign.Labels["example.com/team"] = "blue"
	all := []*hyperfleetv1alpha1.Cluster{hidden, first, missing, second, foreign}
	for _, tc := range []struct {
		name                         string
		policies                     []string
		objects                      []*hyperfleetv1alpha1.Cluster
		query                        string
		ids                          []string
		total, limit, offset, status int
	}{
		{"both actions", []string{clusterReadPermit}, all, "", []string{"hidden-first", "visible-first", "missing-label", "visible-second"}, 4, 50, 0, http.StatusOK},
		{"hidden first page", []string{clusterListPermit, clusterLabelPermit}, all, "?limit=1", []string{"visible-first"}, 2, 1, 0, http.StatusOK},
		{"hidden items consume no slots", []string{clusterListPermit, clusterLabelPermit}, all, "?limit=1&offset=1", []string{"visible-second"}, 2, 1, 1, http.StatusOK},
		{"offset beyond visible set", []string{clusterListPermit, clusterLabelPermit}, all, "?limit=1&offset=2", []string{}, 2, 1, 2, http.StatusOK},
		{"maximum offset", []string{clusterListPermit, clusterLabelPermit}, all, "?offset=" + strconv.Itoa(int(^uint(0)>>1)), []string{}, 2, 50, int(^uint(0) >> 1), http.StatusOK},
		{"all denied", []string{clusterListPermit, clusterLabelPermit}, []*hyperfleetv1alpha1.Cluster{hidden, missing}, "", []string{}, 0, 50, 0, http.StatusOK},
		{"empty storage", []string{clusterReadPermit}, nil, "", []string{}, 0, 50, 0, http.StatusOK},
		{"list grant only", []string{clusterListPermit}, all, "", []string{}, 0, 50, 0, http.StatusOK},
		{"describe grant only", []string{clusterDescribePermit}, all, "", nil, 0, 50, 0, http.StatusForbidden},
		{"collection forbid", []string{clusterReadPermit, `forbid(principal, action == HyperFleet::Action::"ListClusters", resource);`}, all, "", nil, 0, 50, 0, http.StatusForbidden},
		{"invalid paging retains defaults", []string{clusterListPermit, clusterLabelPermit}, all, "?limit=101&offset=-1", []string{"visible-first", "visible-second"}, 2, 50, 0, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			preparations, reads := 0, 0
			a := clusterAuthzFrom(t, clusterResolverFunc(func(_ context.Context, id authz.Identity) ([]authz.ResolvedBinding, error) {
				preparations++
				return clusterBindings(id, tc.policies...), nil
			}))
			fc := clusterOrderedStore(tc.objects, func(context.Context) error { reads++; return nil })
			h, registry, _ := clusterAuthzFixture(t, fc, a)
			w := clusterAuthzRequest(h, authz.ListClusters, tc.query, "")
			if preparations != 1 {
				t.Fatalf("prepared %d policy sets, want exactly one", preparations)
			}
			if tc.status == http.StatusForbidden {
				assertClusterAuthzStatus(t, w, tc.status, "AUTHZ-DENIED-001: Access denied")
				assertClusterAuthzMetrics(t, registry, authz.ListClusters, authz.OutcomeDeny, authz.StageNone)
				if reads != 0 {
					t.Fatal("collection denial reached storage")
				}
				return
			}
			var body struct {
				Items  []public.Cluster `json:"items"`
				Total  int              `json:"total"`
				Limit  int              `json:"limit"`
				Offset int              `json:"offset"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			ids := make([]string, 0, len(body.Items))
			for _, item := range body.Items {
				ids = append(ids, string(item.UID))
			}
			if w.Code != http.StatusOK || !reflect.DeepEqual(ids, tc.ids) || body.Items == nil || body.Total != tc.total || body.Limit != tc.limit || body.Offset != tc.offset || reads != 1 {
				t.Fatalf("want ids=%v total=%d limit=%d offset=%d, got HTTP %d ids=%v body=%s reads=%d", tc.ids, tc.total, tc.limit, tc.offset, w.Code, ids, w.Body.String(), reads)
			}
			assertClusterAuthzMetrics(t, registry, authz.ListClusters, authz.OutcomeAllow, authz.StageNone)
		})
	}
}

func TestClusterAuthz_TrustedMetadata(t *testing.T) {
	for _, tc := range []struct {
		name, label, tag string
		policies         []string
		status           int
	}{
		{"metadata grants despite tags", "blue", "red", []string{clusterLabelPermit}, http.StatusOK},
		{"tags cannot grant", "red", "blue", []string{clusterLabelPermit}, http.StatusForbidden},
		{"absent metadata label", "", "blue", []string{clusterLabelPermit}, http.StatusForbidden},
		{"namespace identity and label ownership", "blue", "red", []string{`permit(principal, action == HyperFleet::Action::"DescribeCluster", resource == HyperFleet::Cluster::"123456789012/us-east-1/stable-id") when { resource.account == "123456789012" && resource.region == "us-east-1" };`}, http.StatusOK},
		{"list grant cannot describe", "blue", "blue", []string{clusterListPermit}, http.StatusForbidden},
		{"describe grant", "red", "blue", []string{clusterDescribePermit}, http.StatusOK},
		{"forbid overrides permit", "blue", "blue", []string{clusterReadPermit, `forbid(principal, action == HyperFleet::Action::"DescribeCluster", resource);`}, http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cr := testClusterCR("stable-id", "untrusted-name", testAccountID)
			cr.UID = types.UID("fleetdb-uid-not-cluster-id")
			cr.Spec.InternalID = "untrusted-spec-id"
			cr.Spec.AccountID = "999999999999"
			cr.Spec.HostedCluster.Platform.AWS.Region = "us-west-2"
			cr.Spec.Tags = map[string]string{"example.com/team": tc.tag}
			if tc.label != "" {
				cr.Labels["example.com/team"] = tc.label
			}
			h, registry, _ := clusterAuthzFixture(t, clusterOrderedStore([]*hyperfleetv1alpha1.Cluster{cr}, nil), clusterAuthorizer(t, tc.policies...))
			r := httptest.NewRequest(http.MethodGet, "/api/v0/clusters/stable-id?example.com/team=blue&accountId=999999999999&region=us-west-2", bytes.NewBufferString(`{"metadata":{"uid":"spoof-id","labels":{"example.com/team":"blue","hyperfleet.io/account-id":"999999999999"}},"spec":{"tags":{"example.com/team":"blue"}}}`)).WithContext(testContext(testAccountID))
			r.Header.Set("X-Resource-Labels", `{"example.com/team":"blue"}`)
			r.Header.Set("X-Account-ID", "999999999999")
			r = mux.SetURLVars(r, map[string]string{"id": "stable-id"})
			w := httptest.NewRecorder()
			h.Get(w, r)
			if tc.status == http.StatusForbidden {
				assertClusterAuthzStatus(t, w, tc.status, "AUTHZ-DENIED-001: Access denied")
				assertClusterAuthzMetrics(t, registry, authz.DescribeCluster, authz.OutcomeDeny, authz.StageNone)
				return
			}
			var body public.Cluster
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || string(body.UID) != "stable-id" || body.Labels["hyperfleet.io/account-id"] != testAccountID {
				t.Fatalf("stored namespace and label ownership not used: %d %s", w.Code, w.Body.String())
			}
			assertClusterAuthzMetrics(t, registry, authz.DescribeCluster, authz.OutcomeAllow, authz.StageNone)
		})
	}
}

func TestClusterAuthz_ForeignAndMissing(t *testing.T) {
	foreign := testClusterCR("foreign", "foreign", "999999999999")
	foreign.Spec.AccountID = testAccountID
	fc := clusterOrderedStore([]*hyperfleetv1alpha1.Cluster{foreign}, nil)
	for _, id := range []string{"foreign", "missing"} {
		t.Run(id, func(t *testing.T) {
			h, registry, _ := clusterAuthzFixture(t, fc, clusterAuthorizer(t, clusterReadPermit))
			w := clusterAuthzRequest(h, authz.DescribeCluster, "", id)
			assertClusterAuthzStatus(t, w, http.StatusNotFound, ErrClusterGetNotFound.Code+": "+ErrClusterGetNotFound.Message)
			assertClusterAuthzMetrics(t, registry, authz.DescribeCluster, authz.OutcomeDeny, authz.StageNone)
		})
	}
	h, registry, _ := clusterAuthzFixture(t, fc, clusterAuthorizer(t, clusterReadPermit))
	w := clusterAuthzRequest(h, authz.ListClusters, "", "")
	var body struct {
		Items []public.Cluster `json:"items"`
		Total int              `json:"total"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || body.Total != 0 || body.Items == nil || len(body.Items) != 0 {
		t.Fatalf("permissive policy crossed account scope: %d %s", w.Code, w.Body.String())
	}
	assertClusterAuthzMetrics(t, registry, authz.ListClusters, authz.OutcomeAllow, authz.StageNone)
}

func TestClusterAuthz_RuntimePreparationErrors(t *testing.T) {
	for _, operation := range []authz.Action{authz.ListClusters, authz.DescribeCluster} {
		for _, tc := range []struct {
			name    string
			stage   authz.Stage
			mutate  func([]authz.ResolvedBinding) ([]authz.ResolvedBinding, error)
			logText string
		}{
			{"resolution", authz.StageResolution, func(b []authz.ResolvedBinding) ([]authz.ResolvedBinding, error) {
				return b, errors.New("resolver-detail-secret")
			}, "resolver-detail-secret"},
			{"late parsing", authz.StageParsing, func(b []authz.ResolvedBinding) ([]authz.ResolvedBinding, error) {
				b[1].PolicyContent = "forbid("
				return b, nil
			}, "attachment/grant-1"},
			{"late binding", authz.StageBinding, func(b []authz.ResolvedBinding) ([]authz.ResolvedBinding, error) {
				b[1].BindingMode = "binding-detail-secret"
				return b, nil
			}, "attachment/grant-1"},
		} {
			t.Run(string(operation)+"/"+tc.name, func(t *testing.T) {
				reads := 0
				fc := clusterOrderedStore([]*hyperfleetv1alpha1.Cluster{testClusterCR("owned", "owned", testAccountID)}, func(context.Context) error { reads++; return nil })
				a := clusterAuthzFrom(t, clusterResolverFunc(func(_ context.Context, id authz.Identity) ([]authz.ResolvedBinding, error) {
					return tc.mutate(clusterBindings(id, clusterReadPermit, clusterDescribePermit))
				}))
				h, registry, logs := clusterAuthzFixture(t, fc, a)
				w := clusterAuthzRequest(h, operation, "", "owned")
				assertClusterAuthzStatus(t, w, http.StatusInternalServerError, "AUTHZ-FAILED-001: Authorization failed")
				assertClusterAuthzMetrics(t, registry, operation, authz.OutcomeError, tc.stage)
				if reads != 0 || !bytes.Contains(logs.Bytes(), []byte(tc.logText)) || !bytes.Contains(logs.Bytes(), []byte(`"cause":`)) {
					t.Fatalf("preparation failure reached storage or lost cause/provenance: reads=%d logs=%s", reads, logs.String())
				}
			})
		}
	}
}

func TestClusterAuthz_LoadingErrors(t *testing.T) {
	for _, operation := range []authz.Action{authz.ListClusters, authz.DescribeCluster} {
		t.Run(string(operation), func(t *testing.T) {
			fc := clusterOrderedStore(nil, func(context.Context) error { return errors.New("storage-detail-secret") })
			h, registry, logs := clusterAuthzFixture(t, fc, clusterAuthorizer(t, clusterReadPermit))
			w := clusterAuthzRequest(h, operation, "", "owned")
			want := ErrClusterList
			if operation == authz.DescribeCluster {
				want = ErrClusterGetFailed
			}
			assertClusterAuthzStatus(t, w, want.HTTPStatus, want.Code+": "+want.Message)
			assertClusterAuthzMetrics(t, registry, operation, authz.OutcomeError, authz.StageResourceLoading)
			if !bytes.Contains(logs.Bytes(), []byte("storage-detail-secret")) {
				t.Fatalf("storage cause not logged: %s", logs.String())
			}
		})
	}
}

func TestClusterAuthz_StoredResourceFaults(t *testing.T) {
	for _, operation := range []authz.Action{authz.ListClusters, authz.DescribeCluster} {
		for _, fault := range []string{"missing ownership", "foreign ownership", "missing namespace", "wrong namespace prefix", "invalid namespace ID"} {
			t.Run(string(operation)+"/"+fault, func(t *testing.T) {
				cr := testClusterCR("owned", "owned", testAccountID)
				switch fault {
				case "missing ownership":
					cr.Labels = nil
				case "foreign ownership":
					cr.Labels["hyperfleet.io/account-id"] = "999999999999"
				case "missing namespace":
					cr.Namespace = "cluster-"
				case "wrong namespace prefix":
					cr.Namespace = "valid-looking-id"
				case "invalid namespace ID":
					cr.Namespace = "cluster-../escape"
				}
				// Corrupt storage material must fail closed even if a storage adapter breaks its selector contract.
				fc := fake.NewClientBuilder().WithScheme(newTestScheme()).WithInterceptorFuncs(interceptor.Funcs{
					List: func(_ context.Context, _ client.WithWatch, obj client.ObjectList, _ ...client.ListOption) error {
						obj.(*hyperfleetv1alpha1.ClusterList).Items = []hyperfleetv1alpha1.Cluster{*cr.DeepCopy()}
						return nil
					},
				}).Build()
				h, registry, logs := clusterAuthzFixture(t, fc, clusterAuthorizer(t, clusterReadPermit))
				w := clusterAuthzRequest(h, operation, "", "owned")
				assertClusterAuthzStatus(t, w, http.StatusInternalServerError, "AUTHZ-FAILED-001: Authorization failed")
				assertClusterAuthzMetrics(t, registry, operation, authz.OutcomeError, authz.StageEntityValidation)
				if !bytes.Contains(logs.Bytes(), []byte(`"cause":`)) {
					t.Fatalf("resource failure cause not logged: %s", logs.String())
				}
			})
		}
	}
}

func TestClusterAuthz_LateEvaluationError(t *testing.T) {
	first := testClusterCR("first", "first", testAccountID)
	late := testClusterCR("late", "late", testAccountID)
	late.Labels["example.com/fault"] = "fault-detail-secret"
	for _, operation := range []authz.Action{authz.ListClusters, authz.DescribeCluster} {
		t.Run(string(operation), func(t *testing.T) {
			h, registry, logs := clusterAuthzFixture(t, clusterOrderedStore([]*hyperfleetv1alpha1.Cluster{first, late}, nil), clusterAuthorizer(t, clusterReadPermit, clusterFailingForbid))
			w := clusterAuthzRequest(h, operation, "?limit=1", "late")
			assertClusterAuthzStatus(t, w, http.StatusInternalServerError, "AUTHZ-FAILED-001: Authorization failed")
			assertClusterAuthzMetrics(t, registry, operation, authz.OutcomeError, authz.StageEvaluation)
			if !bytes.Contains(logs.Bytes(), []byte("attachment/grant-1")) || !bytes.Contains(logs.Bytes(), []byte("test-revision")) || !bytes.Contains(logs.Bytes(), []byte("cedar evaluation diagnostics")) || !bytes.Contains(logs.Bytes(), []byte(`"diagnostics":[{`)) {
				t.Fatalf("evaluation failure lost detailed diagnostics/provenance: %s", logs.String())
			}
		})
	}
}

type clusterWriteFailure struct {
	header         http.Header
	beforeWrite    func()
	writes, status int
}

func (w *clusterWriteFailure) Header() http.Header    { return w.header }
func (w *clusterWriteFailure) WriteHeader(status int) { w.beforeWrite(); w.status = status }
func (w *clusterWriteFailure) Write([]byte) (int, error) {
	w.writes++
	return 0, errors.New("socket-detail-secret")
}

func TestClusterAuthz_WriteFailureKeepsAllow(t *testing.T) {
	for _, operation := range []authz.Action{authz.ListClusters, authz.DescribeCluster} {
		t.Run(string(operation), func(t *testing.T) {
			fc := clusterOrderedStore([]*hyperfleetv1alpha1.Cluster{testClusterCR("owned", "owned", testAccountID)}, nil)
			h, registry, logs := clusterAuthzFixture(t, fc, clusterAuthorizer(t, clusterReadPermit))
			w := &clusterWriteFailure{header: http.Header{}, beforeWrite: func() {
				assertClusterAuthzMetrics(t, registry, operation, authz.OutcomeAllow, authz.StageNone)
			}}
			r := httptest.NewRequest(http.MethodGet, "/api/v0/clusters", nil).WithContext(testContext(testAccountID))
			r = mux.SetURLVars(r, map[string]string{"id": "owned"})
			if operation == authz.ListClusters {
				h.List(w, r)
			} else {
				h.Get(w, r)
			}
			if w.status != http.StatusOK || w.writes != 1 || !bytes.Contains(logs.Bytes(), []byte("socket-detail-secret")) {
				t.Fatalf("unexpected serialization count/status/log: %+v %s", w, logs.String())
			}
			assertClusterAuthzMetrics(t, registry, operation, authz.OutcomeAllow, authz.StageNone)
		})
	}
}

func TestClusterAuthz_UnmappedWritesExcluded(t *testing.T) {
	fc := fake.NewClientBuilder().WithScheme(newTestScheme()).Build()
	h, registry, _ := clusterAuthzFixture(t, fc, clusterAuthorizer(t))
	for _, invoke := range []func(http.ResponseWriter, *http.Request){h.Create, h.Update, h.Delete} {
		r := httptest.NewRequest(http.MethodPost, "/api/v0/clusters", nil).WithContext(testContext(testAccountID))
		r = mux.SetURLVars(r, map[string]string{"id": "missing"})
		invoke(httptest.NewRecorder(), r)
	}
	families, err := registry.Gather()
	if err != nil || len(families) != 0 {
		t.Fatalf("unmapped writes entered authorization metrics: %v %v", families, err)
	}
}
