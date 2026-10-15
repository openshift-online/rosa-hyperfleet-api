package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	v1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func authzResourceRequest(t *testing.T, p *authzProcess, method, path, body, arn, operation string, want int) *APIResponse {
	t.Helper()
	before := authzMetrics(t, p)
	request, err := http.NewRequest(method, p.apiURL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("X-Amz-Account-Id", authzAccount)
	request.Header.Set("X-Amz-Caller-Arn", arn)
	request.Header.Set("Content-Type", "application/json")
	response := authzRawRequest(t, request)
	if response.StatusCode != want {
		t.Errorf("%s %s want %d got %d: %s", method, path, want, response.StatusCode, response.Body)
	}
	outcome := "allow"
	stage := ""
	if want == 403 || want == 404 {
		outcome = "deny"
	}
	if want == 500 {
		outcome = "error"
		stage = "evaluation"
	}
	authzMetricDelta(t, before, authzMetrics(t, p), operation, outcome, stage)
	if want >= 400 {
		authzAssertStatus(t, response, want)
	}
	return response
}

func authzResourceOperations(t *testing.T, p *authzProcess, store client.Client) {
	ctx := context.Background()
	blue := authzStoredClusters[1].name
	red := authzStoredClusters[0].name
	pools := []*v1.NodePool{}
	for _, fixture := range []struct{ cluster, team string }{{red, "red"}, {blue, "blue"}} {
		var parent v1.Cluster
		if err := store.Get(ctx, client.ObjectKey{Namespace: "account-" + authzAccount, Name: fixture.cluster}, &parent); err != nil {
			t.Fatal(err)
		}
		replicas := int32(3)
		np := &v1.NodePool{ObjectMeta: metav1.ObjectMeta{
			Name: fixture.cluster + ".workers", Namespace: parent.Namespace,
			Labels:          map[string]string{authzAccountLabel: authzAccount, authzTeamLabel: fixture.team, "hyperfleet.io/cluster-uid": string(parent.UID)},
			OwnerReferences: []metav1.OwnerReference{*metav1.NewControllerRef(&parent, v1.GroupVersion.WithKind("Cluster"))},
		}, Spec: v1.NodePoolSpec{AccountID: authzAccount}}
		np.Spec.NodePool.ClusterName = parent.Name
		np.Spec.NodePool.Replicas = &replicas
		if err := store.Create(ctx, np); err != nil {
			t.Fatal(err)
		}
		pools = append(pools, np)
	}
	for _, fixture := range []struct{ name, team string }{{"a-config", "blue"}, {"z-config", "red"}} {
		oc := &v1.OidcConfig{ObjectMeta: metav1.ObjectMeta{Name: fixture.name, Namespace: "account-" + authzAccount, Labels: map[string]string{authzAccountLabel: authzAccount, authzTeamLabel: fixture.team}}, Spec: v1.OidcConfigSpec{Type: v1.OidcConfigTypeManaged, AccountID: authzAccount, IssuerUrl: "https://example.com/" + fixture.name}}
		if err := store.Create(ctx, oc); err != nil {
			t.Fatal(err)
		}
		mc := &v1.ManagementCluster{ObjectMeta: metav1.ObjectMeta{Name: fixture.name, Labels: map[string]string{authzTeamLabel: fixture.team}}, Spec: v1.ManagementClusterSpec{AccountID: authzOtherAccount, Region: "us-west-2"}}
		if err := store.Create(ctx, mc); err != nil {
			t.Fatal(err)
		}
		dns := &v1.DNSReservation{ObjectMeta: metav1.ObjectMeta{Name: fixture.name, Namespace: "account-" + authzAccount, Labels: map[string]string{authzAccountLabel: authzAccount, authzTeamLabel: fixture.team}}}
		if err := store.Create(ctx, dns); err != nil {
			t.Fatal(err)
		}
	}
	poolPath := "/api/v0/nodepools/" + blue + ".workers"
	clusterPath := authzClustersPath + "/" + blue
	var configA v1.OidcConfig
	if err := store.Get(ctx, client.ObjectKey{Namespace: "account-" + authzAccount, Name: "a-config"}, &configA); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ method, path, body, action string }{
		{"GET", authzClustersPath, "", "ListClusters"}, {"GET", clusterPath, "", "DescribeCluster"}, {"POST", authzClustersPath, `{"metadata":{"name":"denied-cluster"},"spec":{"oidcConfigId":"` + string(configA.UID) + `"}}`, "CreateCluster"}, {"PUT", clusterPath, `{"spec":{"properties":{"state":"denied"}}}`, "UpdateCluster"}, {"PATCH", clusterPath, `{"spec":{"properties":{"state":"denied"}}}`, "UpdateCluster"}, {"DELETE", clusterPath, "", "DeleteCluster"},
		{"GET", "/api/v0/nodepools", "", "ListNodePools"}, {"GET", poolPath, "", "DescribeNodePool"}, {"POST", "/api/v0/nodepools", `{"metadata":{"name":"` + blue + `.denied"},"spec":{"labels":{"state":"denied"}}}`, "CreateNodePool"}, {"PUT", poolPath, `{"spec":{"labels":{"state":"denied"}}}`, "UpdateNodePool"}, {"DELETE", poolPath, "", "DeleteNodePool"},
		{"GET", "/api/v0/oidc_configs", "", "ListOIDCConfigs"}, {"GET", "/api/v0/oidc_configs/a-config", "", "DescribeOIDCConfig"}, {"POST", "/api/v0/oidc_configs", `{"metadata":{"name":"denied-config"},"spec":{"type":"unmanaged","issuerUrl":"https://example.com/denied","secretArn":"secret","installerRoleArn":"role"}}`, "CreateOIDCConfig"}, {"DELETE", "/api/v0/oidc_configs/a-config", "", "DeleteOIDCConfig"},
		{"GET", "/api/v0/dns_reservations", "", "ListDNSReservations"}, {"GET", "/api/v0/dns_reservations/a-config", "", "DescribeDNSReservation"}, {"POST", "/api/v0/dns_reservations", `{"metadata":{"name":"denied-zone"}}`, "CreateDNSReservation"}, {"DELETE", "/api/v0/dns_reservations/a-config", "", "DeleteDNSReservation"},
		{"GET", "/api/v0/management_clusters", "", "ListManagementClusters"}, {"GET", "/api/v0/management_clusters/a-config", "", "DescribeManagementCluster"}, {"POST", "/api/v0/management_clusters", `{"id":"denied","accountId":"222222222222","region":"us-west-2"}`, "CreateManagementCluster"},
	} {
		t.Run("default deny "+tc.action+tc.method, func(t *testing.T) {
			authzResourceRequest(t, p, tc.method, tc.path, tc.body, authzUser("bob"), tc.action, 403)
		})
	}
	var deniedOC v1.OidcConfig
	if err := store.Get(ctx, client.ObjectKey{Namespace: "account-" + authzAccount, Name: "a-config"}, &deniedOC); err != nil || deniedOC.Labels["hyperfleet.io/claimed-by-cluster-uid"] != "" {
		t.Fatalf("denied CreateCluster claimed OIDC: %+v %v", deniedOC, err)
	}

	// The service set is disjoint even from customer wildcard permits and account enrollment.
	operator := "arn:aws:sts::111111111111:assumed-role/service-operator/session"
	authzResourceRequest(t, p, "GET", "/api/v0/management_clusters", "", authzUser("wildcard"), "ListManagementClusters", 403)
	authzResourceRequest(t, p, "GET", authzClustersPath, "", operator, "ListClusters", 403)
	for _, tc := range []struct{ path, list, describe, listOnly, describeOnly, scoped string }{
		{"nodepools", "ListNodePools", "DescribeNodePool", "resource-list-only", "resource-describe-only", "resource-scoped"},
		{"oidc_configs", "ListOIDCConfigs", "DescribeOIDCConfig", "resource-list-only", "resource-describe-only", "resource-scoped"},
		{"dns_reservations", "ListDNSReservations", "DescribeDNSReservation", "resource-list-only", "resource-describe-only", "resource-scoped"},
		{"management_clusters", "ListManagementClusters", "DescribeManagementCluster", "operator-list-only", "operator-describe-only", "operator-scoped"},
	} {
		t.Run("collection "+tc.path, func(t *testing.T) {
			// List returns both fixtures; only the Describe-allowed one carries details.
			for _, check := range []struct {
				principal        string
				detailed, status int
			}{{tc.listOnly, 0, 200}, {tc.describeOnly, 0, 403}, {tc.scoped, 1, 200}} {
				response := authzResourceRequest(t, p, "GET", "/api/v0/"+tc.path, "", authzUser(check.principal), tc.list, check.status)
				if check.status == 200 {
					var body struct {
						Total int
						Items []json.RawMessage
					}
					err := json.Unmarshal(response.Body, &body)
					detailed := 0
					for _, item := range body.Items {
						if strings.Contains(string(item), authzTeamLabel) || strings.Contains(string(item), "us-west-2") {
							detailed++
						}
					}
					if err != nil || body.Total != 2 || len(body.Items) != 2 || detailed != check.detailed {
						t.Errorf("wrong list details: %s %v", response.Body, err)
					}
				}
			}
		})
	}
	// The qualified name selects the pool; clusterId cannot redirect Describe to another parent.
	authzResourceRequest(t, p, "GET", poolPath, "", authzUser("resource-scoped"), "DescribeNodePool", 200)
	authzResourceRequest(t, p, "GET", "/api/v0/nodepools/"+red+".workers", "", authzUser("resource-scoped"), "DescribeNodePool", 403)
	authzResourceRequest(t, p, "GET", "/api/v0/nodepools/"+red+".workers?clusterId="+authzStoredClusters[1].id, "", authzUser("resource-scoped"), "DescribeNodePool", 403)

	// Each specialized update and the mandatory base must allow; omitted replicas remain unchanged.
	for _, principal := range []string{"base-updater", "special-updater"} {
		authzResourceRequest(t, p, "PUT", clusterPath, `{"spec":{"hostedCluster":{"release":{"image":"new"}}}}`, authzUser(principal), "UpdateCluster", 403)
		authzResourceRequest(t, p, "PATCH", clusterPath, `{"spec":{"hostedCluster":{"release":{"image":"new"}}}}`, authzUser(principal), "UpdateCluster", 403)
		authzResourceRequest(t, p, "PUT", poolPath, `{"spec":{"nodePool":{"replicas":0,"release":{"image":"new"}}}}`, authzUser(principal), "UpdateNodePool", 403)
	}
	lifecycle := authzUser("lifecycle")
	authzResourceRequest(t, p, "PUT", poolPath, `{"spec":{"labels":{"state":"changed"}}}`, lifecycle, "UpdateNodePool", 200)
	var saved v1.NodePool
	if err := store.Get(ctx, client.ObjectKeyFromObject(pools[1]), &saved); err != nil || saved.Spec.NodePool.Replicas == nil || *saved.Spec.NodePool.Replicas != 3 {
		t.Fatalf("omitted replicas changed: %+v %v", saved, err)
	}
	authzResourceRequest(t, p, "PUT", poolPath, `{"spec":{"nodePool":{"replicas":0,"release":{"image":"new"}}}}`, lifecycle, "UpdateNodePool", 200)
	authzResourceRequest(t, p, "PUT", clusterPath, `{"spec":{"properties":{"state":"changed"}}}`, lifecycle, "UpdateCluster", 200)
	authzResourceRequest(t, p, "PATCH", clusterPath, `{"spec":{"hostedCluster":{"release":{"image":"new"}}}}`, lifecycle, "UpdateCluster", 200)

	// Actual HTTP create/get/delete for all customer families and global service registration.
	createCluster := authzResourceRequest(t, p, "POST", authzClustersPath, `{"metadata":{"name":"http-created","labels":{"example.com/team":"blue"}},"spec":{"properties":{"state":"created"}}}`, lifecycle, "CreateCluster", 201)
	var cluster publicResourceID
	_ = json.Unmarshal(createCluster.Body, &cluster)
	if cluster.Metadata.UID == "" {
		t.Fatal("create lacked UID")
	}
	authzResourceRequest(t, p, "GET", authzClustersPath+"/http-created", "", lifecycle, "DescribeCluster", 200)
	authzResourceRequest(t, p, "POST", "/api/v0/nodepools", `{"metadata":{"name":"http-created.pool"},"spec":{"labels":{"state":"created"}}}`, lifecycle, "CreateNodePool", 201)
	authzResourceRequest(t, p, "GET", "/api/v0/nodepools/http-created.pool", "", lifecycle, "DescribeNodePool", 200)
	authzResourceRequest(t, p, "DELETE", "/api/v0/nodepools/http-created.pool", "", lifecycle, "DeleteNodePool", 202)
	authzResourceRequest(t, p, "DELETE", authzClustersPath+"/http-created", "", lifecycle, "DeleteCluster", 202)
	createOC := authzResourceRequest(t, p, "POST", "/api/v0/oidc_configs", `{"metadata":{"name":"http-created","labels":{"example.com/team":"blue","hyperfleet.io/claimed-by-cluster-uid":"forged"}},"spec":{"type":"unmanaged","issuerUrl":"https://example.com/http-created","secretArn":"secret","installerRoleArn":"role"}}`, lifecycle, "CreateOIDCConfig", 201)
	var config publicResourceID
	if err := json.Unmarshal(createOC.Body, &config); err != nil || config.Metadata.UID == "" {
		t.Fatalf("OIDC create lacked UID: %s %v", createOC.Body, err)
	}
	authzResourceRequest(t, p, "GET", "/api/v0/oidc_configs/http-created", "", lifecycle, "DescribeOIDCConfig", 200)
	authzResourceRequest(t, p, "DELETE", "/api/v0/oidc_configs/http-created", "", lifecycle, "DeleteOIDCConfig", 202)
	authzResourceRequest(t, p, "POST", "/api/v0/dns_reservations", `{"metadata":{"name":"http-created","labels":{"example.com/team":"blue","hyperfleet.io/claimed-by-cluster-uid":"forged"}}}`, lifecycle, "CreateDNSReservation", 201)
	authzResourceRequest(t, p, "GET", "/api/v0/dns_reservations/http-created", "", lifecycle, "DescribeDNSReservation", 200)
	authzResourceRequest(t, p, "DELETE", "/api/v0/dns_reservations/http-created", "", lifecycle, "DeleteDNSReservation", 202)
	authzResourceRequest(t, p, "POST", "/api/v0/management_clusters", `{"id":"http-created","accountId":"222222222222","region":"us-west-2"}`, operator, "CreateManagementCluster", 201)
	authzResourceRequest(t, p, "GET", "/api/v0/management_clusters/http-created", "", operator, "DescribeManagementCluster", 200)
	var registered v1.ManagementCluster
	if err := store.Get(ctx, client.ObjectKey{Name: "http-created"}, &registered); err != nil || registered.Namespace != "" || registered.Spec.AccountID != authzOtherAccount {
		t.Fatalf("service registration was customer-scoped: %+v %v", registered, err)
	}

	// All new collections fail atomically on an off-page native evaluator diagnostic.
	for _, tc := range []struct {
		path, action, principal string
		list                    client.ObjectList
	}{
		{"nodepools", "ListNodePools", "resource-error", &v1.NodePoolList{}}, {"oidc_configs", "ListOIDCConfigs", "resource-error", &v1.OidcConfigList{}}, {"dns_reservations", "ListDNSReservations", "resource-error", &v1.DNSReservationList{}}, {"management_clusters", "ListManagementClusters", "operator-error", &v1.ManagementClusterList{}},
	} {
		t.Run("late diagnostic "+tc.path, func(t *testing.T) {
			var objects []client.Object
			if err := store.List(ctx, tc.list); err != nil {
				t.Fatal(err)
			}
			switch list := tc.list.(type) {
			case *v1.NodePoolList:
				for i := range list.Items {
					objects = append(objects, &list.Items[i])
				}
			case *v1.OidcConfigList:
				for i := range list.Items {
					objects = append(objects, &list.Items[i])
				}
			case *v1.DNSReservationList:
				for i := range list.Items {
					objects = append(objects, &list.Items[i])
				}
			case *v1.ManagementClusterList:
				for i := range list.Items {
					objects = append(objects, &list.Items[i])
				}
			}
			if len(objects) < 2 {
				t.Fatal("late diagnostic needs two candidates")
			}
			fault := objects[len(objects)-1]
			labels := fault.GetLabels()
			if labels == nil {
				labels = map[string]string{}
			}
			labels[authzFaultLabel] = authzFaultValue
			fault.SetLabels(labels)
			if err := store.Update(ctx, fault); err != nil {
				t.Fatal(err)
			}
			response := authzResourceRequest(t, p, "GET", "/api/v0/"+tc.path+"?limit=1", "", authzUser(tc.principal), tc.action, 500)
			if strings.Contains(string(response.Body), `"items"`) {
				t.Fatal("late error emitted partial success")
			}
		})
	}
	// Six context fields are available, and forwarding headers cannot impersonate the trusted IP.
	for _, source := range []string{"192.0.2.50", ""} {
		req, err := http.NewRequest("GET", p.apiURL+clusterPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("X-Amz-Account-Id", authzAccount)
		req.Header.Set("X-Amz-Caller-Arn", authzUser("context"))
		req.Header.Set("X-Amz-Source-Ip", source)
		req.Header.Set("X-Forwarded-For", "192.0.2.50")
		req.Header.Set("User-Agent", "local-authz-http")
		response := authzRawRequest(t, req)
		want := 200
		if source == "" {
			want = 403
		}
		if response.StatusCode != want {
			t.Errorf("trusted context want %d got %d: %s", want, response.StatusCode, response.Body)
		}
	}
}

type publicResourceID struct{ Metadata struct{ UID string } }
