package e2e_monitoring_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/openshift/rosa-hyperfleet-api/internal/test/thanos"
)

var _ = Describe("Observability", FlakeAttempts(2), func() {

	It("should have RC CloudWatch metrics in Thanos", func() {
		query := `count(aws_eks_apiserver_storage_size_bytes_maximum{cluster_type="regional-cluster"}) > 0`
		Eventually(func() bool {
			resp := thanos.Query(rhobsClient, query)
			return resp.Status == "success" && len(resp.Data.Result) > 0
		}, "1m", "15s").Should(BeTrue(),
			"Expected CloudWatch EKS metrics with cluster_type=regional-cluster in Thanos "+
				"(CW Exporter → RC Prometheus → Thanos Receive)")
	})

	It("should have MC CloudWatch metrics in Thanos via remote-write", func() {
		query := `count(aws_eks_apiserver_storage_size_bytes_maximum{cluster_type="management-cluster"}) > 0`
		Eventually(func() bool {
			resp := thanos.Query(rhobsClient, query)
			return resp.Status == "success" && len(resp.Data.Result) > 0
		}, "1m", "15s").Should(BeTrue(),
			"Expected CloudWatch EKS metrics with cluster_type=management-cluster in Thanos "+
				"(CW Exporter → MC Prometheus → remote-write → RHOBS API GW → Thanos Receive)")
	})

	It("should have Vector metrics from RC in Thanos", func() {
		query := `count(vector_component_sent_events_total{cluster_type="regional-cluster",component_type="loki"}) > 0`
		Eventually(func() bool {
			resp := thanos.Query(rhobsClient, query)
			return resp.Status == "success" && len(resp.Data.Result) > 0
		}, "1m", "15s").Should(BeTrue(),
			"Expected Vector sink metrics with cluster_type=regional-cluster in Thanos "+
				"(Vector PodMonitor → RC Prometheus → Thanos Receive)")
	})

	It("should have Vector metrics from MC in Thanos via remote-write", func() {
		query := `count(vector_component_sent_events_total{cluster_type="management-cluster",component_type="loki"}) > 0`
		Eventually(func() bool {
			resp := thanos.Query(rhobsClient, query)
			return resp.Status == "success" && len(resp.Data.Result) > 0
		}, "1m", "15s").Should(BeTrue(),
			"Expected Vector sink metrics with cluster_type=management-cluster in Thanos "+
				"(Vector PodMonitor → MC Prometheus → sigv4-proxy → RHOBS API GW → Thanos Receive)")
	})

	It("should have Loki distributor metrics in Thanos", func() {
		query := `count(loki_distributor_bytes_received_total{cluster_type="regional-cluster"}) > 0`
		Eventually(func() bool {
			resp := thanos.Query(rhobsClient, query)
			return resp.Status == "success" && len(resp.Data.Result) > 0
		}, "1m", "15s").Should(BeTrue(),
			"Expected Loki distributor metrics with cluster_type=regional-cluster in Thanos "+
				"(Loki ServiceMonitor → RC Prometheus → Thanos Receive)")
	})

})

var _ = Describe("Alerting", FlakeAttempts(2), func() {

	Context("HCP recording rules", func() {
		It("should have hcp:hostedcluster_available recording rule loaded", func() {
			Eventually(func() bool {
				return thanos.HasRule(rhobsClient, "record", "hcp:hostedcluster_available")
			}, "1m", "15s").Should(BeTrue(),
				"Expected recording rule hcp:hostedcluster_available to be loaded in Thanos Ruler")
		})

		It("should have hcp:lifecycle_installing recording rule loaded", func() {
			Eventually(func() bool {
				return thanos.HasRule(rhobsClient, "record", "hcp:lifecycle_installing")
			}, "1m", "15s").Should(BeTrue(),
				"Expected recording rule hcp:lifecycle_installing to be loaded in Thanos Ruler")
		})

		It("should have hcp:lifecycle_deleting recording rule loaded", func() {
			Eventually(func() bool {
				return thanos.HasRule(rhobsClient, "record", "hcp:lifecycle_deleting")
			}, "1m", "15s").Should(BeTrue(),
				"Expected recording rule hcp:lifecycle_deleting to be loaded in Thanos Ruler")
		})
	})

	Context("HCP SLA alerting rules", func() {
		It("should have HCPAvailabilityErrorBudgetFastBurn alert loaded", func() {
			Eventually(func() bool {
				return thanos.HasRule(rhobsClient, "alert", "HCPAvailabilityErrorBudgetFastBurn")
			}, "1m", "15s").Should(BeTrue(),
				"Expected alerting rule HCPAvailabilityErrorBudgetFastBurn to be loaded in Thanos Ruler "+
					"(14.4x burn rate, ~6min window)")
		})

		It("should have HCPAvailabilityErrorBudgetSlowBurn alert loaded", func() {
			Eventually(func() bool {
				return thanos.HasRule(rhobsClient, "alert", "HCPAvailabilityErrorBudgetSlowBurn")
			}, "1m", "15s").Should(BeTrue(),
				"Expected alerting rule HCPAvailabilityErrorBudgetSlowBurn to be loaded in Thanos Ruler "+
					"(6x burn rate, ~32min window)")
		})
	})

	Context("HCP installation alerting rules", func() {
		It("should have HCPInstallTimeout15m alert loaded", func() {
			Eventually(func() bool {
				return thanos.HasRule(rhobsClient, "alert", "HCPInstallTimeout15m")
			}, "1m", "15s").Should(BeTrue(),
				"Expected alerting rule HCPInstallTimeout15m to be loaded in Thanos Ruler")
		})
	})
})
