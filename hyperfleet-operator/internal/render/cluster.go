package render

import (
	"fmt"

	configv1 "github.com/openshift/api/config/v1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

// ClusterResources generates the 7 Kubernetes resources for a cluster on the MC.
func ClusterResources(cluster *hyperfleetv1alpha1.Cluster, rcfg RegionalConfig) ([]Resource, error) {
	clusterID := ClusterIDFromNamespace(cluster.Namespace)
	clusterName := cluster.Name // human-readable
	ns := cluster.Namespace     // already "cluster-<uuid>"
	h4 := hash4(clusterID)
	// Zone shard 0 is hardcoded; will be dynamically assigned per-cluster in a future phase.
	zoneDomain := fmt.Sprintf("0.%s", rcfg.BaseDomain)

	hc, err := hostedCluster(cluster, h4, zoneDomain)
	if err != nil {
		return nil, err
	}

	return []Resource{
		namespace(clusterID, ns),
		clusterConfig(clusterID, clusterName, ns),
		awsIAMAuthConfig(clusterID, clusterName, ns, cluster.Spec.CreatorARN),
		pullSecret(clusterID, ns),
		apiServingCert(clusterID, clusterName, h4, zoneDomain, ns),
		ingressServingCert(clusterID, clusterName, h4, zoneDomain, ns),
		hc,
		sshKey(clusterID, ns),
	}, nil
}

func namespace(clusterID, ns string) Resource {
	return Resource{
		Group: "", Version: "v1", Resource: "namespaces",
		Name: ns, Namespace: "",
		Object: &corev1.Namespace{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Namespace"},
			ObjectMeta: metav1.ObjectMeta{
				Name: ns,
				Labels: map[string]string{
					"hyperfleet.io/cluster-id":    clusterID,
					"hyperfleet.io/managed-by":    "hyperfleet-operator",
					"hyperfleet.io/resource-type": "namespace",
				},
			},
		},
	}
}

func clusterConfig(clusterID, clusterName, ns string) Resource {
	return Resource{
		Group: "", Version: "v1", Resource: "configmaps",
		Name: "cluster-config", Namespace: ns,
		Object: &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "cluster-config",
				Namespace: ns,
				Labels: map[string]string{
					"hyperfleet.io/cluster-id": clusterID,
				},
			},
			Data: map[string]string{
				"cluster_id":   clusterID,
				"cluster_name": clusterName,
			},
		},
	}
}

func awsIAMAuthConfig(clusterID, clusterName, ns, creatorARN string) Resource {
	mapUsers := "      mapUsers: []\n"
	if creatorARN != "" {
		mapUsers = fmt.Sprintf(`      mapUsers:
        - userARN: %s
          username: cluster-creator
          groups:
            - system:masters
`, creatorARN)
	}

	configYAML := fmt.Sprintf("clusterID: %s\nserver:\n%s", clusterID, mapUsers)

	return Resource{
		Group: "", Version: "v1", Resource: "configmaps",
		Name: "aws-iam-auth-config", Namespace: ns,
		Object: &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "aws-iam-auth-config",
				Namespace: ns,
				Labels: map[string]string{
					"hyperfleet.io/cluster-id": clusterID,
				},
				Annotations: map[string]string{
					"hypershift.openshift.io/cluster": fmt.Sprintf("%s/%s", ns, clusterName),
				},
			},
			Data: map[string]string{
				"config.yaml": configYAML,
			},
		},
	}
}

func pullSecret(clusterID, ns string) Resource {
	return Resource{
		Group: "external-secrets.io", Version: "v1", Resource: "externalsecrets",
		Name: "pull-secret", Namespace: ns,
		Object: &ExternalSecret{
			TypeMeta: metav1.TypeMeta{APIVersion: "external-secrets.io/v1", Kind: "ExternalSecret"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pull-secret",
				Namespace: ns,
				Labels: map[string]string{
					"hyperfleet.io/cluster-id":    clusterID,
					"hyperfleet.io/resource-type": "pull-secret",
				},
			},
			Spec: ExternalSecretSpec{
				RefreshInterval: "1h",
				SecretStoreRef: SecretStoreRef{
					Name: "aws-parameter-store",
					Kind: "ClusterSecretStore",
				},
				Target: ExternalSecretTarget{
					Name:           "pull-secret",
					CreationPolicy: "Orphan",
					Template: ExternalSecretTargetTemplate{
						Type: "kubernetes.io/dockerconfigjson",
					},
				},
				Data: []ExternalSecretDataEntry{
					{
						SecretKey: ".dockerconfigjson",
						RemoteRef: ExternalRemoteRef{Key: "/infra/pull-secret"},
					},
				},
			},
		},
	}
}

func apiServingCert(clusterID, clusterName, h4, baseDomain, ns string) Resource {
	return Resource{
		Group: "cert-manager.io", Version: "v1", Resource: "certificates",
		Name: "api-serving-cert", Namespace: ns,
		Object: &Certificate{
			TypeMeta: metav1.TypeMeta{APIVersion: "cert-manager.io/v1", Kind: "Certificate"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "api-serving-cert",
				Namespace: ns,
				Labels: map[string]string{
					"hyperfleet.io/cluster-id": clusterID,
				},
			},
			Spec: CertificateSpec{
				SecretName: "api-serving-cert",
				IssuerRef: CertificateIssuerRef{
					Name: "letsencrypt-dns01",
					Kind: "ClusterIssuer",
				},
				DNSNames: []string{
					fmt.Sprintf("*.%s.%s.%s", clusterName, h4, baseDomain),
				},
			},
		},
	}
}

func ingressServingCert(clusterID, clusterName, h4, baseDomain, ns string) Resource {
	return Resource{
		Group: "cert-manager.io", Version: "v1", Resource: "certificates",
		Name: "ingress-serving-cert", Namespace: ns,
		Object: &Certificate{
			TypeMeta: metav1.TypeMeta{APIVersion: "cert-manager.io/v1", Kind: "Certificate"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ingress-serving-cert",
				Namespace: ns,
				Labels: map[string]string{
					"hyperfleet.io/cluster-id": clusterID,
				},
			},
			Spec: CertificateSpec{
				SecretName: "ingress-serving-cert",
				IssuerRef: CertificateIssuerRef{
					Name: "letsencrypt-dns01",
					Kind: "ClusterIssuer",
				},
				DNSNames: []string{
					fmt.Sprintf("*.apps.in.%s.%s.%s", clusterName, h4, baseDomain),
				},
			},
		},
	}
}

func hostedCluster(cluster *hyperfleetv1alpha1.Cluster, h4, zoneDomain string) (Resource, error) {
	clusterID := ClusterIDFromNamespace(cluster.Namespace)
	clusterName := cluster.Name // human-readable
	ns := cluster.Namespace     // already "cluster-<uuid>"
	baseDomain := zoneDomain
	apiHost := fmt.Sprintf("api.%s.%s.%s", clusterName, h4, baseDomain)

	hcSpec, err := toHostedClusterSpec(&cluster.Spec.HostedCluster)
	if err != nil {
		return Resource{}, fmt.Errorf("converting HostedClusterSpec for cluster %s/%s: %w", ns, clusterName, err)
	}

	// --- Platform-managed overrides (always set by the operator) ---
	hcSpec.InfraID = clusterID
	hcSpec.DNS = hypershiftv1beta1.DNSSpec{
		BaseDomain: fmt.Sprintf("%s.%s", h4, baseDomain),
	}
	hcSpec.PullSecret = corev1.LocalObjectReference{Name: "pull-secret"}
	hcSpec.SSHKey = corev1.LocalObjectReference{Name: "ssh-key"}
	hcSpec.KubeAPIServerDNSName = apiHost
	hcSpec.Services = servicePublishingStrategies(clusterName, h4, baseDomain)
	if hcSpec.Configuration == nil {
		hcSpec.Configuration = apiServerConfiguration()
	} else {
		hcSpec.Configuration.APIServer = apiServerConfiguration().APIServer
	}
	ingressDomain := fmt.Sprintf("apps.in.%s.%s.%s", clusterName, h4, baseDomain)
	hcSpec.Configuration.Ingress = &configv1.IngressSpec{
		Domain: ingressDomain,
	}

	// --- Defaults (only set if customer didn't specify) ---
	if hcSpec.Etcd.ManagementType == "" {
		hcSpec.Etcd = defaultEtcdSpec()
	}
	if hcSpec.Networking.NetworkType == "" {
		hcSpec.Networking.NetworkType = hypershiftv1beta1.OVNKubernetes
	}
	if hcSpec.InfrastructureAvailabilityPolicy == "" {
		hcSpec.InfrastructureAvailabilityPolicy = hypershiftv1beta1.HighlyAvailable
	}
	if hcSpec.ControllerAvailabilityPolicy == "" {
		hcSpec.ControllerAvailabilityPolicy = hypershiftv1beta1.HighlyAvailable
	}
	if len(hcSpec.Networking.ClusterNetwork) == 0 {
		hcSpec.Networking.ClusterNetwork = []hypershiftv1beta1.ClusterNetworkEntry{
			{CIDR: mustParseCIDR("10.132.0.0/14")},
		}
	}
	if len(hcSpec.Networking.ServiceNetwork) == 0 {
		hcSpec.Networking.ServiceNetwork = []hypershiftv1beta1.ServiceNetworkEntry{
			{CIDR: mustParseCIDR("172.31.0.0/16")},
		}
	}
	if len(hcSpec.Networking.MachineNetwork) == 0 {
		hcSpec.Networking.MachineNetwork = []hypershiftv1beta1.MachineNetworkEntry{
			{CIDR: mustParseCIDR("10.0.0.0/16")},
		}
	}
	hcSpec.Release.Image = "quay.io/openshift-release-dev/ocp-release:5.0.0-ec.6-multi"

	// --- Platform overrides ---
	if hcSpec.Platform.AWS != nil {
		hcSpec.Platform.AWS.EndpointAccess = hypershiftv1beta1.PublicAndPrivate
		hcSpec.Platform.AWS.ResourceTags = appendSystemTags(hcSpec.Platform.AWS.ResourceTags, clusterID)
	}

	return Resource{
		Group: "hypershift.openshift.io", Version: "v1beta1", Resource: "hostedclusters",
		Name: clusterName, Namespace: ns,
		Object: &hypershiftv1beta1.HostedCluster{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "hypershift.openshift.io/v1beta1",
				Kind:       "HostedCluster",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      clusterName,
				Namespace: ns,
				Labels: map[string]string{
					"hyperfleet.io/cluster-id": clusterID,
				},
				Annotations: map[string]string{
					hypershiftv1beta1.PodSecurityAdmissionLabelOverrideAnnotation: "privileged",
					hypershiftv1beta1.ControlPlaneOperatorImageAnnotation:         "quay.io/cbusse_openshift/control-plane-operator:managed-ingress-dns-a0906f3af0",
					"hypershift.openshift.io/aws-iam-authenticator":               "true",
					hypershiftv1beta1.ManagedIngressDNSAnnotation:                  "true",
				},
			},
			Spec: *hcSpec,
		},
	}, nil
}

func servicePublishingStrategies(clusterName, h4, baseDomain string) []hypershiftv1beta1.ServicePublishingStrategyMapping {
	apiHost := fmt.Sprintf("api.%s.%s.%s", clusterName, h4, baseDomain)
	return []hypershiftv1beta1.ServicePublishingStrategyMapping{
		{
			Service: hypershiftv1beta1.APIServer,
			ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
				Type:  hypershiftv1beta1.Route,
				Route: &hypershiftv1beta1.RoutePublishingStrategy{Hostname: apiHost},
			},
		},
		{
			Service: hypershiftv1beta1.OAuthServer,
			ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
				Type:  hypershiftv1beta1.Route,
				Route: &hypershiftv1beta1.RoutePublishingStrategy{Hostname: fmt.Sprintf("oauth.%s.%s.%s", clusterName, h4, baseDomain)},
			},
		},
		{
			Service: hypershiftv1beta1.Konnectivity,
			ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
				Type: hypershiftv1beta1.Route,
			},
		},
		{
			Service: hypershiftv1beta1.Ignition,
			ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{
				Type: hypershiftv1beta1.Route,
			},
		},
	}
}

func apiServerConfiguration() *hypershiftv1beta1.ClusterConfiguration {
	return &hypershiftv1beta1.ClusterConfiguration{
		APIServer: &configv1.APIServerSpec{
			ServingCerts: configv1.APIServerServingCerts{
				NamedCertificates: []configv1.APIServerNamedServingCert{
					{
						ServingCertificate: configv1.SecretNameReference{
							Name: "api-serving-cert",
						},
					},
				},
			},
		},
	}
}

func defaultEtcdSpec() hypershiftv1beta1.EtcdSpec {
	return hypershiftv1beta1.EtcdSpec{
		ManagementType: hypershiftv1beta1.Managed,
		Managed: &hypershiftv1beta1.ManagedEtcdSpec{
			Storage: hypershiftv1beta1.ManagedEtcdStorageSpec{
				Type: hypershiftv1beta1.PersistentVolumeEtcdStorage,
				PersistentVolume: &hypershiftv1beta1.PersistentVolumeEtcdStorageSpec{
					Size:             ptr.To(resource.MustParse("32Gi")),
					StorageClassName: ptr.To("gp3"),
				},
			},
		},
	}
}

func appendSystemTags(existing []hypershiftv1beta1.AWSResourceTag, clusterID string) []hypershiftv1beta1.AWSResourceTag {
	tags := []hypershiftv1beta1.AWSResourceTag{
		{Key: "red-hat-managed", Value: "true"},
	}
	if clusterID != "" {
		tags = append(tags, hypershiftv1beta1.AWSResourceTag{
			Key: fmt.Sprintf("kubernetes.io/cluster/%s", clusterID), Value: "owned",
		})
	}
	return append(tags, existing...)
}

func mustParseCIDR(s string) ipnet.IPNet {
	parsed, err := ipnet.ParseCIDR(s)
	if err != nil {
		panic(fmt.Sprintf("invalid CIDR %q: %v", s, err))
	}
	return *parsed
}

func sshKey(clusterID, ns string) Resource {
	return Resource{
		Group: "", Version: "v1", Resource: "secrets",
		Name: "ssh-key", Namespace: ns,
		Object: &corev1.Secret{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ssh-key",
				Namespace: ns,
				Labels: map[string]string{
					"hyperfleet.io/cluster-id": clusterID,
				},
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				"id_rsa.pub": {},
			},
		},
	}
}
