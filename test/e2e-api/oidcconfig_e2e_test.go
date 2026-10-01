package e2e_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// oidcConfigMetadata extracts the metadata sub-object from a decoded OidcConfig response body.
func oidcConfigMetadata(config map[string]interface{}) map[string]interface{} {
	metadata, _ := config["metadata"].(map[string]interface{})
	return metadata
}

// oidcConfigSpec extracts the spec sub-object from a decoded OidcConfig response body.
func oidcConfigSpec(config map[string]interface{}) map[string]interface{} {
	spec, _ := config["spec"].(map[string]interface{})
	return spec
}

// apiErrorCode extracts only the sanitized platform error code from an API error response body
// (e.g. "OIDCCONFIGS-MGMT-CREATE-002"), so failure diagnostics never echo the full body, which can
// carry customer-supplied issuerUrl or other OIDC config fields. The API serializes errors as a
// metav1.Status whose "message" is "<code>: <reason>"; this returns the "<code>" prefix.
func apiErrorCode(body []byte) string {
	var status struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &status)
	code, _, _ := strings.Cut(status.Message, ": ")
	return code
}

var _ = Describe("OIDC Config", Ordered, Label("oidcconfig"), func() {
	var (
		baseURL         string
		accountID       string
		apiClient       *APIClient
		createdConfigID string
	)

	BeforeAll(func() {
		baseURL = os.Getenv("E2E_BASE_URL")
		Expect(baseURL).NotTo(BeEmpty(), "E2E_BASE_URL must be set")

		accountID = os.Getenv("E2E_ACCOUNT_ID")
		if accountID == "" {
			GinkgoWriter.Printf("No E2E_ACCOUNT_ID set, using AWS STS caller identity\n")
			cmd := exec.Command("aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text")
			output, err := cmd.CombinedOutput()
			Expect(err).NotTo(HaveOccurred(), "Failed to get AWS account ID via STS")
			accountID = strings.TrimSpace(string(output))
		}

		apiClient = NewAPIClient(baseURL)
	})

	It("should create a managed OIDC config", func() {
		createReq := map[string]interface{}{
			"spec": map[string]interface{}{
				"type": "managed",
			},
		}

		response, err := apiClient.Post("/api/v0/oidc_configs", createReq, accountID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusCreated), "code=%s", apiErrorCode(response.Body))
		Expect(response.Headers).To(HaveKey("Content-Type"))

		var created map[string]interface{}
		Expect(json.Unmarshal(response.Body, &created)).To(Succeed())

		spec := oidcConfigSpec(created)
		Expect(spec["type"]).To(Equal("managed"))
		Expect(spec["issuerUrl"]).NotTo(BeEmpty(), "managed config should have a computed issuerUrl")

		metadata := oidcConfigMetadata(created)
		name, _ := metadata["name"].(string)
		Expect(name).NotTo(BeEmpty(), "response should include metadata.name as the config ID")
		Expect(metadata["uid"]).NotTo(BeEmpty(), "response should include the database-minted metadata.uid")
		createdConfigID = name

		GinkgoWriter.Printf("Created OIDC config id=%s issuerUrl=%v\n", createdConfigID, spec["issuerUrl"])
	})

	It("should get the created OIDC config by id", func() {
		Expect(createdConfigID).NotTo(BeEmpty(), "requires a config created by a previous test")

		response, err := apiClient.Get("/api/v0/oidc_configs/"+createdConfigID, accountID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK), "code=%s", apiErrorCode(response.Body))

		var fetched map[string]interface{}
		Expect(json.Unmarshal(response.Body, &fetched)).To(Succeed())

		Expect(oidcConfigMetadata(fetched)["name"]).To(Equal(createdConfigID))
		Expect(oidcConfigSpec(fetched)["type"]).To(Equal("managed"))
	})

	It("should list OIDC configs and include the created config", func() {
		Expect(createdConfigID).NotTo(BeEmpty(), "requires a config created by a previous test")

		response, err := apiClient.Get("/api/v0/oidc_configs", accountID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK), "code=%s", apiErrorCode(response.Body))

		var list struct {
			Items []map[string]interface{} `json:"items"`
			Total int                      `json:"total"`
		}
		Expect(json.Unmarshal(response.Body, &list)).To(Succeed())

		found := false
		for _, item := range list.Items {
			if name, _ := oidcConfigMetadata(item)["name"].(string); name == createdConfigID {
				found = true
				break
			}
		}
		Expect(found).To(BeTrue(), "expected created config %s to appear in list", createdConfigID)
	})

	It("should reject creating an OIDC config with a missing type", func() {
		createReq := map[string]interface{}{
			"spec": map[string]interface{}{},
		}

		response, err := apiClient.Post("/api/v0/oidc_configs", createReq, accountID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusBadRequest), "code=%s", apiErrorCode(response.Body))
		Expect(apiErrorCode(response.Body)).To(Equal("OIDCCONFIGS-MGMT-CREATE-002"))
	})

	It("should reject creating an OIDC config with an invalid type", func() {
		createReq := map[string]interface{}{
			"spec": map[string]interface{}{
				"type": "bogus",
			},
		}

		response, err := apiClient.Post("/api/v0/oidc_configs", createReq, accountID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusBadRequest), "code=%s", apiErrorCode(response.Body))
		Expect(apiErrorCode(response.Body)).To(Equal("OIDCCONFIGS-MGMT-CREATE-004"))
	})

	It("should return 404 for a nonexistent OIDC config", func() {
		response, err := apiClient.Get("/api/v0/oidc_configs/does-not-exist", accountID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusNotFound))
	})

	It("should delete the created OIDC config", func() {
		Expect(createdConfigID).NotTo(BeEmpty(), "requires a config created by a previous test")

		response, err := apiClient.Delete("/api/v0/oidc_configs/"+createdConfigID, accountID)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusAccepted), "code=%s", apiErrorCode(response.Body))

		var deleted map[string]interface{}
		Expect(json.Unmarshal(response.Body, &deleted)).To(Succeed())
		Expect(fmt.Sprintf("%v", deleted["config_id"])).To(Equal(createdConfigID))

		By("waiting for the config to actually disappear from Get")
		Eventually(func(g Gomega) {
			resp, err := apiClient.Get("/api/v0/oidc_configs/"+createdConfigID, accountID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusNotFound),
				"expected config %s to be gone after delete (status=%d)", createdConfigID, resp.StatusCode)
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())

		By("waiting for the config to disappear from List")
		Eventually(func(g Gomega) {
			resp, err := apiClient.Get("/api/v0/oidc_configs", accountID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK), "code=%s", apiErrorCode(resp.Body))

			var list struct {
				Items []map[string]interface{} `json:"items"`
			}
			g.Expect(json.Unmarshal(resp.Body, &list)).To(Succeed())

			for _, item := range list.Items {
				g.Expect(oidcConfigMetadata(item)["name"]).NotTo(Equal(createdConfigID),
					"deleted config %s should no longer appear in list", createdConfigID)
			}
		}).WithTimeout(2 * time.Minute).WithPolling(5 * time.Second).Should(Succeed())
	})
})
