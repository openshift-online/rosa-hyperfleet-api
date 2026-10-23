package e2e_test

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// getAndExpectOK performs a GET request and asserts no error. If bodyContains is non-empty,
// it asserts the response body contains that substring. Returns the response for further assertions or logging.
func getAndExpectOK(client *APIClient, path, accountID, bodyContains string) *APIResponse {
	response, err := client.Get(path, accountID)
	Expect(err).To(BeNil())
	if bodyContains != "" {
		Expect(response.Body).To(ContainSubstring(bodyContains))
	}
	return response
}

func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Seaplane API E2E Suite")
}

// Ordered for now, as our test size is small
var _ = Describe("Platform API", Ordered, func() {
	var (
		baseURL   string
		accountID string
		apiClient *APIClient
	)

	BeforeAll(func() {
		baseURL = os.Getenv("E2E_BASE_URL")
		accountID = os.Getenv("E2E_ACCOUNT_ID")
		if accountID == "" {
			GinkgoWriter.Printf("No E2E_ACCOUNT_ID set, using AWS STS caller identity\n")
			cmd := exec.Command("aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text")
			output, err := cmd.CombinedOutput()
			if err != nil {
				Fail("Failed to get AWS account ID: " + err.Error())
			}
			accountID = strings.TrimSpace(string(output))
		}
		apiClient = NewAPIClient(baseURL)
	})

	It("should basic passing test", func() {
		// Placeholder for future e2e tests
		Expect(true).To(BeTrue())
	})

	It("should have BASE_URL set with valid URL: "+baseURL, func() {

		Expect(baseURL).NotTo(BeEmpty())
		Expect(baseURL).To(MatchRegexp("^https?://.*$"))
		// Validate API Gateway URL format: https://<api-id>.execute-api.<region>.amazonaws.com[/<stage>]
		// Accepts any AWS region (e.g., us-east-1, eu-west-2, ap-southeast-1) and optional stage/path
		// Expect(baseURL).To(MatchRegexp("^https://[a-zA-Z0-9]+\\.execute-api\\.[a-z]+-[a-z]+-[0-9]+\\.amazonaws\\.com(/.*)?$"))
	})

	// it should successfully call the API GET /live endpoint
	// it should access endpoint using the sigv4 authentication protocol
	It("should successfully call the API GET /v0/live endpoint", func() {
		response := getAndExpectOK(apiClient, "/v0/live", accountID, "ok")
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(string(response.Body)).To(ContainSubstring("ok"))
		Expect(response.Headers).To(HaveKey("Content-Type"))
		Expect(response.Headers).To(HaveKey("X-Amz-Apigw-Id"))
	})

	It("should successfully call the API GET /v0/ready endpoint", func() {
		response := getAndExpectOK(apiClient, "/v0/ready", accountID, "ok")
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(string(response.Body)).To(ContainSubstring("ok"))
		Expect(response.Headers).To(HaveKey("Content-Type"))
		Expect(response.Headers).To(HaveKey("X-Amz-Apigw-Id"))
	})

	It("should successfully call the API GET /api/v0/ready endpoint", func() {
		response := getAndExpectOK(apiClient, "/api/v0/ready", accountID, "ok")
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		Expect(string(response.Body)).To(ContainSubstring("ok"))
		Expect(response.Headers).To(HaveKey("Content-Type"))
		Expect(response.Headers).To(HaveKey("X-Amz-Apigw-Id"))
	})

	// it should be able to GET /clusters endpoint and return an array
	// Uses Eventually because in CI the seaplane-api depends on seaplane-operator,
	// which may not be fully routable until ArgoCD finishes all sync cycles.
	It("should have the clusters endpoint defined", func() {
		Eventually(func() bool {
			response, err := apiClient.Get("/api/v0/clusters", accountID)
			if err != nil {
				GinkgoWriter.Printf("clusters request error: %v\n", err)
				return false
			}
			if response.StatusCode != http.StatusOK {
				GinkgoWriter.Printf("clusters returned status %d, waiting for 200\n", response.StatusCode)
				return false
			}
			Expect(response.Headers).To(HaveKey("Content-Type"))
			Expect(response.Headers).To(HaveKey("X-Amz-Apigw-Id"))
			var list struct {
				Items  []map[string]interface{} `json:"items"`
				Limit  int                      `json:"limit"`
				Offset int                      `json:"offset"`
				Total  int                      `json:"total"`
			}
			err = json.Unmarshal(response.Body, &list)
			Expect(err).To(BeNil())
			Expect(list.Items).NotTo(BeNil())
			return true
		}, "2m", "5s").Should(BeTrue(), "clusters endpoint did not return 200 - seaplane-operator may not be ready")
	})
})
