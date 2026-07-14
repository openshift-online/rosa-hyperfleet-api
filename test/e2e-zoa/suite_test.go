package e2e_zoa_test

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	awstest "github.com/openshift/rosa-hyperfleet-api/internal/test/aws"
)

var (
	apiClient *awstest.APIClient
	accountID string
	mcName    string
)

func TestE2EZOA(t *testing.T) {
	if os.Getenv("E2E_BASE_URL") == "" {
		t.Skip("E2E_BASE_URL not set — skipping ZOA e2e tests")
	}
	RegisterFailHandler(Fail)
	RunSpecs(t, "ROSA Regional Platform ZOA E2E Suite")
}

var _ = BeforeSuite(func() {
	baseURL := os.Getenv("E2E_BASE_URL")
	Expect(baseURL).NotTo(BeEmpty(), "E2E_BASE_URL must be set")
	apiClient = awstest.NewAPIClient(baseURL)

	accountID = os.Getenv("E2E_ACCOUNT_ID")
	if accountID == "" {
		cmd := exec.Command("aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text")
		output, err := cmd.CombinedOutput()
		Expect(err).NotTo(HaveOccurred(), "Failed to get AWS account ID")
		accountID = strings.TrimSpace(string(output))
	}
	GinkgoWriter.Printf("Account ID: %s\n", accountID)

	By("Discovering management cluster name via API")
	mcName = discoverMC(apiClient, accountID)
	GinkgoWriter.Printf("Management cluster: %s\n", mcName)
})

func discoverMC(client *awstest.APIClient, acctID string) string {
	resp, err := client.Get("/api/v0/management_clusters", acctID)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, resp.StatusCode).To(Equal(http.StatusOK))

	var list struct {
		Items []struct {
			Name string `json:"name"`
		} `json:"items"`
	}
	err = json.Unmarshal(resp.Body, &list)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, list.Items).NotTo(BeEmpty(), "No management clusters registered")

	for _, item := range list.Items {
		if !strings.HasPrefix(item.Name, "test-") {
			return item.Name
		}
	}
	Fail("No non-test management cluster found")
	return ""
}
