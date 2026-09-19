package render

import (
	"testing"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
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

// testClusterWithOidcConfig returns a cluster fixture using the
// OidcConfig-backed issuer path (OidcConfigID set).
func testClusterWithOidcConfig() *hyperfleetv1alpha1.Cluster {
	c := testCluster()
	c.Spec.OidcConfigID = "test-oidc-config"
	c.Spec.AccountID = "123456789012"
	return c
}

func TestClusterResourcesCount(t *testing.T) {
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	if got := len(resources); got != 8 {
		t.Errorf("expected 8 resources, got %d", got)
	}
}

func TestClusterResourcesTypes(t *testing.T) {
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}

	expected := []struct {
		resource string
		name     string
	}{
		{"namespaces", "cluster-abc12345"},
		{"configmaps", "cluster-config"},
		{"configmaps", "aws-iam-auth-config"},
		{"externalsecrets", "pull-secret"},
		{"certificates", "api-serving-cert"},
		{"certificates", "ingress-serving-cert"},
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
	resources, err := ClusterResources(testClusterWithOidcConfig(), true, "f7a3.0.example.com")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	if got := len(resources); got != 9 {
		t.Fatalf("expected 9 resources, got %d", got)
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
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com")
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
	resources, err := ClusterResources(testClusterWithOidcConfig(), false, "f7a3.0.example.com")
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

	resources, err := ClusterResources(cluster, false, "f7a3.0.example.com")
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

func TestExtractUUIDFromIssuerURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "full UUID in CloudFront URL",
			url:  "https://d2nd8tiva4zh6j.cloudfront.net/21305398-14aa-4003-96a3-f3b860e04a1c",
			want: "21305398-14aa-4003-96a3-f3b860e04a1c",
		},
		{
			name: "UUID with trailing slash",
			url:  "https://oidc.example.com/abc12345-1234-5678-90ab-cdef12345678/",
			want: "abc12345-1234-5678-90ab-cdef12345678",
		},
		{
			name: "short UUID-like string",
			url:  "https://oidc.example.com/abc12345",
			want: "",
		},
		{
			name: "no UUID in path",
			url:  "https://example.com/some-path",
			want: "",
		},
		{
			name: "empty string",
			url:  "",
			want: "",
		},
		{
			name: "path with no hyphens",
			url:  "https://example.com/nohyphens",
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractUUIDFromIssuerURL(tt.url)
			if got != tt.want {
				t.Errorf("extractUUIDFromIssuerURL(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

func TestHostedClusterDNS(t *testing.T) {
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com")
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

	// When using a pre-created OIDC config, InfraID should match the UUID from issuerURL
	// instead of the cluster ID, so HyperShift uploads to the correct S3 path.
	if got := hc.Spec.InfraID; got != "abc12345" {
		t.Errorf("infraID = %q, want %q (extracted from issuerURL)", got, "abc12345")
	}
}

func TestCreatorARNInAuthConfig(t *testing.T) {
	resources, err := ClusterResources(testCluster(), false, "f7a3.0.example.com")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}

	var cm *corev1.ConfigMap
	for _, m := range resources {
		if m.Name == "aws-iam-auth-config" {
			cm = m.Object.(*corev1.ConfigMap)
			break
		}
	}

	cfg := cm.Data["config.yaml"]
	if cfg == "" {
		t.Fatal("config.yaml is empty")
	}
	if !contains(cfg, "arn:aws:iam::123456789012:user/admin") {
		t.Error("config.yaml should contain the creator ARN")
	}
	if !contains(cfg, "cluster-creator") {
		t.Error("config.yaml should contain the cluster-creator username")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
