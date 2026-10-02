package v1alpha1

import (
	"testing"

	rest "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
)

func TestUnprojectClusterPreservesProxyConfiguration(t *testing.T) {
	got := UnprojectCluster(&rest.ClusterSpec{
		HostedCluster: rest.HostedClusterSpecPassthrough{
			Configuration: &rest.ClusterConfiguration{
				Proxy: &rest.ProxyConfiguration{
					HTTPProxy:  "http://proxy.example.com:8080",
					HTTPSProxy: "https://proxy.example.com:8443",
					NoProxy:    "localhost,127.0.0.1",
				},
			},
		},
	}, nil)
	if got == nil || got.HostedCluster.Configuration == nil || got.HostedCluster.Configuration.Proxy == nil {
		t.Fatal("expected proxy configuration to be preserved in the CRD")
	}

	proxy := got.HostedCluster.Configuration.Proxy
	if proxy.HTTPProxy != "http://proxy.example.com:8080" ||
		proxy.HTTPSProxy != "https://proxy.example.com:8443" ||
		proxy.NoProxy != "localhost,127.0.0.1" {
		t.Errorf("proxy configuration was not preserved: %+v", proxy)
	}
}
