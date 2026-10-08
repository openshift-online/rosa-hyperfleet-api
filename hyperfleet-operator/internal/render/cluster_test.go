package render

import (
	"slices"
	"testing"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

func testCluster() *hyperfleetv1alpha1.Cluster {
	return &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "cluster-abc12345",
		},
		Spec: hyperfleetv1alpha1.ClusterSpec{
			CreatorARN: "arn:aws:iam::123456789012:user/admin",
			HostedCluster: hyperfleetv1alpha1.HostedClusterSpecPassthrough{
				Release:   hypershiftv1beta1.Release{Image: "quay.io/ocp:4.17"},
				IssuerURL: "https://oidc.example.com/abc12345",
				Configuration: &hypershiftv1beta1.ClusterConfiguration{
					Proxy: &configv1.ProxySpec{
						HTTPProxy:  "http://proxy.example.com:8080",
						HTTPSProxy: "https://proxy.example.com:8443",
						NoProxy:    "localhost,127.0.0.1",
					},
				},
				Networking: hypershiftv1beta1.ClusterNetworking{
					ClusterNetwork: []hypershiftv1beta1.ClusterNetworkEntry{{CIDR: mustParseCIDR("10.128.0.0/14")}},
					ServiceNetwork: []hypershiftv1beta1.ServiceNetworkEntry{{CIDR: mustParseCIDR("172.30.0.0/16")}},
					MachineNetwork: []hypershiftv1beta1.MachineNetworkEntry{{CIDR: mustParseCIDR("10.0.0.0/16")}},
				},
				Platform: hypershiftv1beta1.PlatformSpec{
					Type: hypershiftv1beta1.AWSPlatform,
					AWS: &hypershiftv1beta1.AWSPlatformSpec{
						Region: "us-east-1",
						CloudProviderConfig: &hypershiftv1beta1.AWSCloudProviderConfig{
							VPC:  "vpc-abc",
							Zone: "us-east-1a",
							Subnet: &hypershiftv1beta1.AWSResourceReference{
								ID: ptr.To("subnet-1"),
							},
						},
						RolesRef: hypershiftv1beta1.AWSRolesRef{
							ControlPlaneOperatorARN: "arn:cpo",
							IngressARN:              "arn:ingress",
							ImageRegistryARN:        "arn:registry",
							KubeCloudControllerARN:  "arn:kccm",
							NodePoolManagementARN:   "arn:npm",
							NetworkARN:              "arn:network",
							StorageARN:              "arn:storage",
						},
					},
				},
			},
		},
	}
}

func TestClusterResourcesPreservesProxyConfiguration(t *testing.T) {
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}

	var hostedCluster *hypershiftv1beta1.HostedCluster
	for _, resource := range resources {
		if resource.Resource == "hostedclusters" {
			hostedCluster = resource.Object.(*hypershiftv1beta1.HostedCluster)
			break
		}
	}
	if hostedCluster == nil {
		t.Fatal("no hostedcluster resource found")
	}
	if hostedCluster.Spec.Configuration == nil || hostedCluster.Spec.Configuration.Proxy == nil {
		t.Fatal("expected proxy configuration to be preserved")
	}

	proxy := hostedCluster.Spec.Configuration.Proxy
	if proxy.HTTPProxy != "http://proxy.example.com:8080" ||
		proxy.HTTPSProxy != "https://proxy.example.com:8443" ||
		proxy.NoProxy != "localhost,127.0.0.1" {
		t.Errorf("proxy configuration was not preserved: %+v", proxy)
	}
}

// testClusterWithOidcConfig returns a cluster fixture using the
// OidcConfig-backed issuer path (OidcConfigID set).
func testClusterWithOidcConfig() *hyperfleetv1alpha1.Cluster {
	c := testCluster()
	c.Spec.OidcConfigID = "test-oidc-config"
	c.Spec.AccountID = "123456789012"
	return c
}

func TestClusterResourcesCount(t *testing.T) {
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	if got := len(resources); got != 6 {
		t.Errorf("expected 6 resources, got %d", got)
	}
}

func TestClusterResourcesTypes(t *testing.T) {
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}

	expected := []struct {
		resource string
		name     string
	}{
		{"namespaces", "cluster-abc12345"},
		{"configmaps", "cluster-config"},
		{"externalsecrets", "pull-secret"},
		{"certificates", "api-serving-cert"},
		{"hostedclusters", "my-cluster"},
		{"secrets", "ssh-key"},
	}

	for i, e := range expected {
		if resources[i].Resource != e.resource {
			t.Errorf("resource[%d]: expected resource %q, got %q", i, e.resource, resources[i].Resource)
		}
		if resources[i].Name != e.name {
			t.Errorf("resource[%d]: expected name %q, got %q", i, e.name, resources[i].Name)
		}
	}
}

// TestClusterResourcesWithOidcConfig verifies the OIDC signing key
// ExternalSecret and ServiceAccountSigningKey reference are rendered when the
// referenced OidcConfig is type=unmanaged (oidcSigningKeyExternal=true).
func TestClusterResourcesWithOidcConfig(t *testing.T) {
	resources, err := ClusterResources(testClusterWithOidcConfig(), true, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	if got := len(resources); got != 7 {
		t.Fatalf("expected 7 resources, got %d", got)
	}

	last := resources[len(resources)-1]
	if last.Resource != "externalsecrets" || last.Name != "oidc-signing-key" {
		t.Errorf("expected last resource to be externalsecrets/oidc-signing-key, got %s/%s", last.Resource, last.Name)
	}
	es, ok := last.Object.(*ExternalSecret)
	if !ok {
		t.Fatalf("expected *ExternalSecret, got %T", last.Object)
	}
	wantPath := "/hyperfleet/oidc/123456789012/test-oidc-config/signing-key"
	if len(es.Spec.Data) != 1 || es.Spec.Data[0].RemoteRef.Key != wantPath {
		t.Errorf("expected remote ref key %q, got %+v", wantPath, es.Spec.Data)
	}

	var hc *hypershiftv1beta1.HostedCluster
	for _, r := range resources {
		if r.Resource == "hostedclusters" {
			hc = r.Object.(*hypershiftv1beta1.HostedCluster)
			break
		}
	}
	if hc == nil {
		t.Fatal("no hostedcluster resource found")
	}
	if hc.Spec.ServiceAccountSigningKey == nil || hc.Spec.ServiceAccountSigningKey.Name != "oidc-signing-key" {
		t.Errorf("expected ServiceAccountSigningKey to reference oidc-signing-key, got %+v", hc.Spec.ServiceAccountSigningKey)
	}
}

// TestClusterResourcesWithoutOidcConfig_NoExternalSecret verifies the legacy
// path renders no OIDC signing key ExternalSecret.
func TestClusterResourcesWithoutOidcConfig_NoExternalSecret(t *testing.T) {
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	for _, r := range resources {
		if r.Resource == "externalsecrets" && r.Name == "oidc-signing-key" {
			t.Error("expected no oidc-signing-key ExternalSecret without OidcConfigID")
		}
	}

	var hc *hypershiftv1beta1.HostedCluster
	for _, r := range resources {
		if r.Resource == "hostedclusters" {
			hc = r.Object.(*hypershiftv1beta1.HostedCluster)
			break
		}
	}
	if hc == nil {
		t.Fatal("no hostedcluster resource found")
	}
	if hc.Spec.ServiceAccountSigningKey != nil {
		t.Errorf("expected ServiceAccountSigningKey to be nil, got %+v", hc.Spec.ServiceAccountSigningKey)
	}
}

// TestClusterResourcesWithManagedOidcConfig_NoExternalSecret verifies that a
// managed OidcConfig (OidcConfigID set, oidcSigningKeyExternal=false) renders
// no ExternalSecret/ServiceAccountSigningKey, since managed configs don't
// store a signing key in Secrets Manager for ESO to deliver.
func TestClusterResourcesWithManagedOidcConfig_NoExternalSecret(t *testing.T) {
	resources, err := ClusterResources(testClusterWithOidcConfig(), false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	for _, r := range resources {
		if r.Resource == "externalsecrets" && r.Name == "oidc-signing-key" {
			t.Error("expected no oidc-signing-key ExternalSecret for a managed OidcConfig")
		}
	}

	var hc *hypershiftv1beta1.HostedCluster
	for _, r := range resources {
		if r.Resource == "hostedclusters" {
			hc = r.Object.(*hypershiftv1beta1.HostedCluster)
			break
		}
	}
	if hc == nil {
		t.Fatal("no hostedcluster resource found")
	}
	if hc.Spec.ServiceAccountSigningKey != nil {
		t.Errorf("expected ServiceAccountSigningKey to be nil for a managed OidcConfig, got %+v", hc.Spec.ServiceAccountSigningKey)
	}
}

// TestClusterResourcesClearsStaleServiceAccountSigningKey verifies that a
// ServiceAccountSigningKey already present on the Cluster CR's spec (e.g. a
// stale value from a prior generation) is cleared when oidcSigningKeyExternal
// is false, rather than passed through to the rendered HostedCluster.
func TestClusterResourcesClearsStaleServiceAccountSigningKey(t *testing.T) {
	cluster := testCluster()
	cluster.Spec.HostedCluster.ServiceAccountSigningKey = &corev1.LocalObjectReference{Name: "stale-key"}

	resources, err := ClusterResources(cluster, false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}

	var hc *hypershiftv1beta1.HostedCluster
	for _, r := range resources {
		if r.Resource == "hostedclusters" {
			hc = r.Object.(*hypershiftv1beta1.HostedCluster)
			break
		}
	}
	if hc == nil {
		t.Fatal("no hostedcluster resource found")
	}
	if hc.Spec.ServiceAccountSigningKey != nil {
		t.Errorf("expected stale ServiceAccountSigningKey to be cleared, got %+v", hc.Spec.ServiceAccountSigningKey)
	}
}

func TestHostedClusterDNS(t *testing.T) {
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}

	var hc *hypershiftv1beta1.HostedCluster
	for _, m := range resources {
		if m.Resource == "hostedclusters" {
			hc = m.Object.(*hypershiftv1beta1.HostedCluster)
			break
		}
	}
	if hc == nil {
		t.Fatal("no hostedcluster resource found")
	}

	if got := hc.Spec.DNS.BaseDomain; got != "f7a3.0.example.com" {
		t.Errorf("dns.baseDomain = %q, want %q", got, "f7a3.0.example.com")
	}

	if want := "api.my-cluster.f7a3.0.example.com"; hc.Spec.KubeAPIServerDNSName != want {
		t.Errorf("kubeAPIServerDNSName = %q, want %q", hc.Spec.KubeAPIServerDNSName, want)
	}

	if got := hc.Spec.IssuerURL; got != "https://oidc.example.com/abc12345" {
		t.Errorf("issuerURL = %q, want %q", got, "https://oidc.example.com/abc12345")
	}

	if got := hc.Spec.Release.Image; got != "quay.io/ocp:4.17" {
		t.Errorf("release.image = %q, want %q", got, "quay.io/ocp:4.17")
	}

	// testCluster() has no OidcConfigID, so InfraID defaults to clusterID.
	if got := hc.Spec.InfraID; got != "abc12345" {
		t.Errorf("infraID = %q, want %q (clusterID)", got, "abc12345")
	}
}

// TestHostedClusterInfraIDManagedOidcConfig verifies InfraID is derived from issuerURL's trailing
// path segment for a managed OidcConfig, since that's the S3 key HyperShift uploads OIDC
// discovery docs to.
func TestHostedClusterInfraIDManagedOidcConfig(t *testing.T) {
	cluster := testClusterWithOidcConfig()
	cluster.Spec.HostedCluster.IssuerURL = "https://oidc.example.com/2130539814aa400396a3f3b860e04a1c"

	resources, err := ClusterResources(cluster, false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	hc := hostedClusterFrom(t, resources)

	want := "2130539814aa400396a3f3b860e04a1c"
	if got := hc.Spec.InfraID; got != want {
		t.Errorf("infraID = %q, want %q", got, want)
	}
}

// TestHostedClusterInfraIDUnmanagedOidcConfig verifies InfraID stays clusterID for an unmanaged
// OidcConfig, regardless of the customer-supplied issuerURL's shape.
func TestHostedClusterInfraIDUnmanagedOidcConfig(t *testing.T) {
	cluster := testClusterWithOidcConfig()
	cluster.Spec.HostedCluster.IssuerURL = "https://customer-idp.example.com/some/arbitrary/path"

	resources, err := ClusterResources(cluster, true, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	hc := hostedClusterFrom(t, resources)

	const clusterID = "abc12345" // from namespace "cluster-abc12345"
	if got := hc.Spec.InfraID; got != clusterID {
		t.Errorf("infraID = %q, want %q", got, clusterID)
	}
}

// hostedClusterFrom returns the rendered HostedCluster from a resource slice.
func hostedClusterFrom(t *testing.T, resources []Resource) *hypershiftv1beta1.HostedCluster {
	t.Helper()
	for _, r := range resources {
		if r.Resource == "hostedclusters" {
			return r.Object.(*hypershiftv1beta1.HostedCluster)
		}
	}
	t.Fatal("no hostedcluster resource found")
	return nil
}

// renderHostedCluster renders cluster and returns its HostedCluster, failing the
// test if one was not produced.
func renderHostedCluster(t *testing.T, cluster *hyperfleetv1alpha1.Cluster) *hypershiftv1beta1.HostedCluster {
	t.Helper()
	resources, err := ClusterResources(cluster, false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	return hostedClusterFrom(t, resources)
}

// TestHostedClusterControlPlaneOperatorImageAnnotation verifies the CPO image
// override is stamped as an annotation when set, and omitted when empty.
func TestHostedClusterControlPlaneOperatorImageAnnotation(t *testing.T) {
	const cpoImage = "quay.io/me/hypershift:pr-1234"

	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com", cpoImage)
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	hc := hostedClusterFrom(t, resources)
	if got := hc.Annotations[hypershiftv1beta1.ControlPlaneOperatorImageAnnotation]; got != cpoImage {
		t.Errorf("CPO annotation = %q, want %q", got, cpoImage)
	}

	resources, err = ClusterResources(testCluster(), false, "f7a3.0.example.com", "")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	hc = hostedClusterFrom(t, resources)
	if _, ok := hc.Annotations[hypershiftv1beta1.ControlPlaneOperatorImageAnnotation]; ok {
		t.Errorf("CPO annotation should be absent when override is empty, got %q",
			hc.Annotations[hypershiftv1beta1.ControlPlaneOperatorImageAnnotation])
	}
}

func tagPairs(tags []hypershiftv1beta1.AWSResourceTag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, tag := range tags {
		out[tag.Key] = tag.Value
	}
	return out
}

func TestHostedClusterSystemTagsOnly(t *testing.T) {
	hc := renderHostedCluster(t, testCluster())

	got := tagPairs(hc.Spec.Platform.AWS.ResourceTags)
	if len(got) != 2 {
		t.Fatalf("resourceTags = %v, want only the 2 system tags", got)
	}
	if got["red-hat-managed"] != "true" {
		t.Errorf("red-hat-managed = %q, want %q", got["red-hat-managed"], "true")
	}
	if got["kubernetes.io/cluster/abc12345"] != "owned" {
		t.Errorf("kubernetes.io/cluster/abc12345 = %q, want %q", got["kubernetes.io/cluster/abc12345"], "owned")
	}
}

func TestHostedClusterCustomerTags(t *testing.T) {
	cluster := testCluster()
	cluster.Spec.Tags = map[string]string{
		"cost-center": "cc-1234",
		"environment": "production",
	}

	hc := renderHostedCluster(t, cluster)

	got := tagPairs(hc.Spec.Platform.AWS.ResourceTags)
	if got["cost-center"] != "cc-1234" {
		t.Errorf("cost-center = %q, want %q", got["cost-center"], "cc-1234")
	}
	if got["environment"] != "production" {
		t.Errorf("environment = %q, want %q", got["environment"], "production")
	}
	// Customer tags must not displace the system tags.
	if got["red-hat-managed"] != "true" {
		t.Errorf("red-hat-managed = %q, want %q", got["red-hat-managed"], "true")
	}
	if got["kubernetes.io/cluster/abc12345"] != "owned" {
		t.Errorf("kubernetes.io/cluster/abc12345 = %q, want %q", got["kubernetes.io/cluster/abc12345"], "owned")
	}
}

// A customer must not be able to overwrite the tags the platform relies on for
// ownership and billing attribution.
func TestHostedClusterCustomerTagsCannotShadowSystemTags(t *testing.T) {
	cluster := testCluster()
	cluster.Spec.Tags = map[string]string{
		"red-hat-managed":                "false",
		"kubernetes.io/cluster/abc12345": "shared",
	}

	hc := renderHostedCluster(t, cluster)

	tags := hc.Spec.Platform.AWS.ResourceTags
	if len(tags) != 2 {
		t.Fatalf("resourceTags = %v, want 2 entries with no duplicate keys", tags)
	}
	got := tagPairs(tags)
	if got["red-hat-managed"] != "true" {
		t.Errorf("red-hat-managed = %q, want %q", got["red-hat-managed"], "true")
	}
	if got["kubernetes.io/cluster/abc12345"] != "owned" {
		t.Errorf("kubernetes.io/cluster/abc12345 = %q, want %q", got["kubernetes.io/cluster/abc12345"], "owned")
	}
}

// Map iteration order is random, so renders of the same Cluster must still
// produce an identical tag slice or the operator would churn the HostedCluster.
func TestHostedClusterCustomerTagsDeterministicOrder(t *testing.T) {
	cluster := testCluster()
	cluster.Spec.Tags = map[string]string{
		"delta": "4", "alpha": "1", "charlie": "3", "bravo": "2", "echo": "5",
	}

	first := renderHostedCluster(t, cluster).Spec.Platform.AWS.ResourceTags
	for i := range 10 {
		got := renderHostedCluster(t, cluster).Spec.Platform.AWS.ResourceTags
		if !slices.Equal(got, first) {
			t.Fatalf("render %d produced %v, want %v", i, got, first)
		}
	}

	// System tags first, then customer tags sorted by key.
	want := []string{
		"red-hat-managed", "kubernetes.io/cluster/abc12345",
		"alpha", "bravo", "charlie", "delta", "echo",
	}
	for i, key := range want {
		if first[i].Key != key {
			t.Errorf("resourceTags[%d].Key = %q, want %q", i, first[i].Key, key)
		}
	}
}

// Tags set on the HostedCluster passthrough take precedence over a colliding
// customer tag, since the passthrough is service-set.
func TestHostedClusterCustomerTagsYieldToPassthroughTags(t *testing.T) {
	cluster := testCluster()
	cluster.Spec.HostedCluster.Platform.AWS.ResourceTags = []hypershiftv1beta1.AWSResourceTag{
		{Key: "environment", Value: "service-managed"},
	}
	cluster.Spec.Tags = map[string]string{"environment": "customer-set"}

	hc := renderHostedCluster(t, cluster)

	tags := hc.Spec.Platform.AWS.ResourceTags
	if len(tags) != 3 {
		t.Fatalf("resourceTags = %v, want 3 entries with no duplicate keys", tags)
	}
	if got := tagPairs(tags)["environment"]; got != "service-managed" {
		t.Errorf("environment = %q, want %q", got, "service-managed")
	}
}
