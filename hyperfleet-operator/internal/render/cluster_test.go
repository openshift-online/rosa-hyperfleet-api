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

func testRegionalConfig() RegionalConfig {
	return RegionalConfig{
		BaseDomain: "example.com",
		AWSRegion:  "us-east-1",
	}
}

func TestClusterResourcesCount(t *testing.T) {
	resources, err := ClusterResources(testCluster(), testRegionalConfig(), "f7a3.0.example.com")
	if err != nil {
		t.Fatalf("ClusterResources: %v", err)
	}
	if got := len(resources); got != 7 {
		t.Errorf("expected 7 resources, got %d", got)
	}
}

func TestClusterResourcesTypes(t *testing.T) {
	resources, err := ClusterResources(testCluster(), testRegionalConfig(), "f7a3.0.example.com")
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

func TestHostedClusterDNS(t *testing.T) {
	resources, err := ClusterResources(testCluster(), testRegionalConfig(), "f7a3.0.example.com")
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
}

func TestCreatorARNInAuthConfig(t *testing.T) {
	resources, err := ClusterResources(testCluster(), testRegionalConfig(), "f7a3.0.example.com")
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
