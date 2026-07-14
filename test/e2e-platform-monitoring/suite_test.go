package e2e_monitoring_test

import (
	"net/http"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	awstest "github.com/openshift/rosa-hyperfleet-api/internal/test/aws"
)

var (
	rhobsAPIURL string
	rhobsClient *awstest.APIClient
)

func TestE2EMonitoring(t *testing.T) {
	if os.Getenv("E2E_RHOBS_API_URL") == "" {
		t.Skip("E2E_RHOBS_API_URL not set — skipping monitoring tests")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "ROSA Regional Platform API Monitoring E2E Suite")
}

var _ = BeforeSuite(func() {
	rhobsAPIURL = os.Getenv("E2E_RHOBS_API_URL")
	rhobsClient = awstest.NewAPIClient(rhobsAPIURL)

	By("Verifying Thanos query endpoint is reachable")
	Eventually(func() int {
		resp, err := rhobsClient.Get("/api/v1/query?query=up", "")
		if err != nil {
			GinkgoWriter.Printf("Thanos query probe error: %v\n", err)
			return 0
		}
		GinkgoWriter.Printf("Thanos query probe: %d\n", resp.StatusCode)
		return resp.StatusCode
	}, "5m", "15s").Should(Equal(http.StatusOK),
		"Thanos /api/v1/query endpoint should return 200")

	By("Verifying Thanos rules endpoint is reachable")
	Eventually(func() int {
		resp, err := rhobsClient.Get("/api/v1/rules", "")
		if err != nil {
			GinkgoWriter.Printf("Thanos rules probe error: %v\n", err)
			return 0
		}
		GinkgoWriter.Printf("Thanos rules probe: %d\n", resp.StatusCode)
		return resp.StatusCode
	}, "5m", "15s").Should(Equal(http.StatusOK),
		"Thanos /api/v1/rules endpoint should return 200")
})
