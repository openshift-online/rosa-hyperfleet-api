package e2e_cli_test

// CLI E2E Tests - HCP Cluster Creation via rosactl
//
// Run individual tests using label filters:
//
// Setup phase:
//   ginkgo --label-filter="setup" ./test/e2e-cli         # All setup tests
//   ginkgo --label-filter="vpc-create" ./test/e2e-cli    # Just VPC creation
//   ginkgo --label-filter="iam-create" ./test/e2e-cli    # Just IAM creation
//
// Create phase:
//   ginkgo --label-filter="create" ./test/e2e-cli        # Cluster creation
//   ginkgo --label-filter="hcp-create" ./test/e2e-cli    # Just HCP cluster
//
// Monitor phase:
//   ginkgo --label-filter="monitor" ./test/e2e-cli       # Status checks
//   ginkgo --label-filter="cluster-status" ./test/e2e-cli # Just status polling
//
// Cleanup phase:
//   ginkgo --label-filter="cleanup" ./test/e2e-cli       # All cleanup tests
//   ginkgo --label-filter="vpc-delete" ./test/e2e-cli    # Just VPC deletion
//
// Available labels:
//   help, login, vpc-create, vpc-list, iam-create, iam-list, account-add,
//   hcp-create, oidc-create, oidc-list, cluster-status, dns-verify, nodepools-wait,
//   hcp-patch, bundles-delete, bundles-wait, oidc-delete, iam-delete, vpc-delete
//
// Group labels: setup, create, monitor, update, cleanup

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	awstest "github.com/openshift/rosa-hyperfleet-api/internal/test/aws"
	"github.com/openshift/rosa-hyperfleet-api/internal/test/thanos"
)

func recordTiming(phase string) func() {
	start := float64(time.Now().UnixNano()) / 1e9
	return func() {
		end := float64(time.Now().UnixNano()) / 1e9
		shared := os.Getenv("SHARED_DIR")
		if shared == "" {
			return
		}
		status := "ok"
		if CurrentSpecReport().Failed() {
			status = "error"
		}
		record := fmt.Sprintf(`{"phase":%q,"start":%.3f,"end":%.3f,"step":"e2e","status":%q}`,
			phase, start, end, status)
		f, err := os.OpenFile(filepath.Join(shared, "timing.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return
		}
		defer f.Close()
		fmt.Fprintln(f, record)
	}
}

func customerEnv() []string {
	return []string{"AWS_PROFILE=" + os.Getenv("CUSTOMER_AWS_PROFILE")}
}

type bundleItem struct {
	ID   string
	Name string
}

func listClusterBundles(apiClient *awstest.APIClient, clusterID, accountID string) []bundleItem {
	var matched []bundleItem
	page := 1
	for {
		resp, err := apiClient.Get(fmt.Sprintf("/api/v0/resource_bundles?page=%d&size=100", page), accountID)
		if err != nil || resp.StatusCode != http.StatusOK {
			break
		}
		var list struct {
			Total int                      `json:"total"`
			Items []map[string]interface{} `json:"items"`
		}
		if json.Unmarshal(resp.Body, &list) != nil {
			break
		}
		for _, item := range list.Items {
			meta, _ := item["metadata"].(map[string]interface{})
			name, _ := meta["name"].(string)
			if strings.Contains(name, clusterID) {
				id, _ := item["id"].(string)
				matched = append(matched, bundleItem{ID: id, Name: name})
			}
		}
		if len(list.Items) == 0 || page*100 >= list.Total {
			break
		}
		page++
	}
	return matched
}

func deleteClusterBundles(apiClient *awstest.APIClient, clusterID, accountID string) int {
	bundles := listClusterBundles(apiClient, clusterID, accountID)
	for _, b := range bundles {
		GinkgoWriter.Printf("Deleting bundle %s (%s)\n", b.ID, b.Name)
		apiClient.Delete("/api/v0/resource_bundles/"+b.ID, accountID) //nolint:errcheck
	}
	return len(bundles)
}

func waitForBundleRemoval(apiClient *awstest.APIClient, clusterID, accountID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if len(listClusterBundles(apiClient, clusterID, accountID)) == 0 {
			GinkgoWriter.Printf("All resource bundles for cluster %s removed\n", clusterID)
			return true
		}
		GinkgoWriter.Printf("Resource bundles still present, waiting...\n")
		time.Sleep(30 * time.Second)
	}
	return false
}

func fireAndForgetInfraDelete(rosactlBin, clusterName, region string, resources []string) {
	for _, subCmd := range resources {
		GinkgoWriter.Printf("Cleanup: firing %s delete %s (fire-and-forget)\n", subCmd, clusterName)
		cmd := exec.Command(rosactlBin, subCmd, "delete", clusterName, "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		if err := cmd.Start(); err != nil {
			GinkgoWriter.Printf("Cleanup WARNING: failed to start %s delete: %v\n", subCmd, err)
		} else if cmd.Process != nil {
			_ = cmd.Process.Release()
		}
	}
}

var _ = Describe("ROSACTL CLI E2E Tests", Ordered, func() {
	var (
		baseURL           string
		accountID         string
		customerAccountID string
		ROSACTL_BIN       string
		clusterName       string
		clusterID         string
		cloudUrl          string
		region            string
		apiClient         *awstest.APIClient

		// Track which resources were created so DeferCleanup knows what to tear down.
		hcpCreated  bool
		vpcCreated  bool
		iamCreated  bool
		oidcCreated bool

		// Set to true when the normal cleanup specs complete successfully.
		// DeferCleanup uses this to skip redundant work on the happy path.
		cleanupCompleted bool
	)

	BeforeAll(func() {

		//--------------------------------
		// Required environment variables for e2e testing
		//--------------------------------
		baseURL = os.Getenv("BASE_URL")
		if baseURL == "" {
			Skip("BASE_URL is not set")
		}
		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = "us-east-1"
			GinkgoWriter.Printf("No AWS_REGION set, defaulting to %s\n", region)
		}
		ROSACTL_BIN = os.Getenv("ROSACTL_BIN")
		if ROSACTL_BIN == "" {
			Skip("ROSACTL_BIN is not set")
		}
		if os.Getenv("CUSTOMER_AWS_PROFILE") == "" {
			Skip("CUSTOMER_AWS_PROFILE is not set — no customer AWS profile available")
		}

		// this is the RC account id, a privileged account id to the baseURL orAPI_URL
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
		GinkgoWriter.Printf("E2E_ACCOUNT_ID: %s\n", accountID)

		customerAccountID = os.Getenv("E2E_CUSTOMER_ACCOUNT_ID")
		if customerAccountID == "" {
			GinkgoWriter.Printf("No E2E_CUSTOMER_ACCOUNT_ID set, using AWS STS caller identity\n")
			cmd := exec.Command("aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text")
			cmd.Env = append(os.Environ(), customerEnv()...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				Fail("Failed to get AWS customer account ID: " + err.Error())
			}
			customerAccountID = strings.TrimSpace(string(output))
			GinkgoWriter.Printf("Customer account ID: %s\n", customerAccountID)
		}

		//--------------------------------
		// Optional: development overrides
		//--------------------------------
		if os.Getenv("HCP_CLUSTER_NAME") != "" {
			clusterName = os.Getenv("HCP_CLUSTER_NAME")
		} else {
			// Default to e2e-<timestamp>
			clusterName = fmt.Sprintf("e2e-%d", time.Now().Unix())
		}

		apiClient = awstest.NewAPIClient(baseURL)

		// Safety-net cleanup: runs after the Ordered container finishes,
		// but only does work when the normal cleanup specs were skipped
		// (i.e., a mid-suite failure caused Ginkgo to skip them).
		DeferCleanup(func() {
			if os.Getenv("E2E_SKIP_CLEANUP") != "" {
				GinkgoWriter.Printf("\n=== DeferCleanup: E2E_SKIP_CLEANUP is set, skipping teardown ===\n")
				return
			}
			if cleanupCompleted {
				GinkgoWriter.Printf("\n=== DeferCleanup: normal cleanup already ran, nothing to do ===\n")
				return
			}
			GinkgoWriter.Printf("\n=== DeferCleanup: safety-net cleanup (normal cleanup was skipped) ===\n")

			if hook := os.Getenv("PRE_CLEANUP_HOOK"); hook != "" {
				GinkgoWriter.Printf("Running pre-cleanup hook (DeferCleanup path): %s\n", hook)
				cmd := exec.Command("bash", "-c", hook)
				cmd.Stdout = GinkgoWriter
				cmd.Stderr = GinkgoWriter
				if err := cmd.Run(); err != nil {
					GinkgoWriter.Printf("WARNING: pre-cleanup hook failed: %v (continuing with cleanup)\n", err)
				}
			}

			if hcpCreated && clusterID != "" {
				GinkgoWriter.Printf("Cleanup: deleting HCP cluster %s (id: %s)\n", clusterName, clusterID)
				resp, err := apiClient.Delete("/api/v0/clusters/"+clusterID, accountID)
				if err != nil {
					GinkgoWriter.Printf("Cleanup WARNING: failed to call delete cluster API: %v\n", err)
				} else if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNotFound {
					GinkgoWriter.Printf("Cleanup WARNING: delete cluster returned status %d: %s\n", resp.StatusCode, string(resp.Body))
				} else {
					GinkgoWriter.Printf("Cleanup: HCP cluster delete accepted (status %d)\n", resp.StatusCode)
					deadline := time.Now().Add(5 * time.Minute)
					for time.Now().Before(deadline) {
						time.Sleep(15 * time.Second)
						r, e := apiClient.Get("/api/v0/clusters/"+clusterID, accountID)
						if e != nil {
							GinkgoWriter.Printf("Cleanup: transient error polling cluster status: %v\n", e)
							continue
						}
						if r.StatusCode == http.StatusNotFound || r.StatusCode == http.StatusGone {
							GinkgoWriter.Printf("Cleanup: HCP cluster confirmed deleted\n")
							break
						}
					}
				}

				deleteClusterBundles(apiClient, clusterID, accountID)
				waitForBundleRemoval(apiClient, clusterID, accountID, 5*time.Minute)
			}

			var stacks []string
			if oidcCreated {
				stacks = append(stacks, "cluster-oidc")
			}
			if vpcCreated {
				stacks = append(stacks, "cluster-vpc")
			}
			if iamCreated {
				stacks = append(stacks, "cluster-iam")
			}
			if len(stacks) > 0 && clusterName != "" && ROSACTL_BIN != "" {
				fireAndForgetInfraDelete(ROSACTL_BIN, clusterName, region, stacks)
			}

			GinkgoWriter.Printf("=== DeferCleanup complete ===\n")
		})
	})

	It("should be able to have help", Label("help"), func() {
		cmd := exec.Command(ROSACTL_BIN, "help")
		output, err := cmd.CombinedOutput()
		if err != nil {
			Fail("Failed to get help: " + err.Error())
		}
		fmt.Println(string(output))
		Expect(string(output)).To(ContainSubstring("Usage:"))
	})

	// Add your CLI-based cluster tests here
	// locate the rosactl cli command
	// run the rosactl cli command
	// it should be able to run the rosactl command and login to the e2e_base_url
	// it should be able to create a new cluster with the given name and region
	It("should be able to login to the BASE_URL", Label("login", "setup"), func() {
		GinkgoWriter.Printf("Logging in to BASE_URL: %s\n", baseURL)

		cmd := exec.Command(ROSACTL_BIN, "login", "--url", baseURL)
		output, err := cmd.CombinedOutput()
		if err != nil {
			Fail("Failed to login to the BASE_URL: " + err.Error())
		}
		fmt.Println(string(output))
	})

	// create a new cluster-vpc
	It("should be able to create a new cluster-vpc", Label("vpc-create", "setup"), func() {
		defer recordTiming("hcp-vpc-create")()
		GinkgoWriter.Printf("Creating new cluster-vpc: %s\n", clusterName)
		// GinkgoWriter.Printf("Command: %s %s %s %s %s\n", ROSACTL_BIN, "cluster-vpc", "create", clusterName, "--region", region, "--availability-zones", "us-east-1a")
		cmd := exec.Command(ROSACTL_BIN, "cluster-vpc", "create", clusterName, "--region", region, "--availability-zones", "us-east-1a")
		cmd.Env = append(os.Environ(), customerEnv()...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			Fail("Failed to create a new cluster-vpc: " + err.Error())
		}
		vpcCreated = true
		GinkgoWriter.Printf("Cluster-VPC created successfully: %s\n", clusterName)
	})

	// it should be able to list the cluster-vpc and find that cluster in the list
	It("should be able to list the cluster-vpc and find that cluster in the list", Label("vpc-list", "setup"), func() {
		GinkgoWriter.Printf("Listing cluster-vpc: %s\n", clusterName)
		cmd := exec.Command(ROSACTL_BIN, "cluster-vpc", "list", "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			Fail("Failed to list the cluster-vpc: " + err.Error())
		}
		fmt.Println(string(output))
		Expect(string(output)).To(ContainSubstring(clusterName))
	})

	// create a new cluster-iam
	It("should be able to create the cluster-iam", Label("iam-create", "setup"), func() {
		defer recordTiming("hcp-iam-create")()
		GinkgoWriter.Printf("Creating new cluster-iam: %s\n", clusterName)
		cmd := exec.Command(ROSACTL_BIN, "cluster-iam", "create", clusterName, "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			Fail("Failed to create the cluster-iam: " + err.Error())
		}
		iamCreated = true
		GinkgoWriter.Printf("Cluster-IAM created successfully: %s\n", clusterName)
	})

	It("should be able to list the cluster-iam and find that cluster in the list", Label("iam-list", "setup"), func() {
		GinkgoWriter.Printf("Listing cluster-iam: %s\n", clusterName)
		cmd := exec.Command(ROSACTL_BIN, "cluster-iam", "list", "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			Fail("Failed to list the cluster-iam: " + err.Error())
		}
		fmt.Println(string(output))
		Expect(string(output)).To(ContainSubstring(clusterName))
	})

	It("should be able to add the customer account to the platform api accounts", Label("account-add", "setup"), func() {
		GinkgoWriter.Printf("Adding customer account to the platform api accounts: %s %s\n", accountID, customerAccountID)
		body := map[string]interface{}{
			"accountId":  customerAccountID,
			"privileged": true,
		}
		response, err := apiClient.Post("/api/v0/accounts", body, accountID)
		Expect(err).ToNot(HaveOccurred())
		switch response.StatusCode {
		case http.StatusCreated:
			GinkgoWriter.Printf("Customer account %s enabled\n", customerAccountID)
		case http.StatusConflict:
			var errBody map[string]interface{}
			Expect(json.Unmarshal(response.Body, &errBody)).To(Succeed())
			Expect(errBody["code"]).To(Equal("account-exists"), "unexpected 409 body: %s", string(response.Body))
			GinkgoWriter.Printf("Customer account %s already enabled (409 account-exists)\n", customerAccountID)
		default:
			Fail(fmt.Sprintf("failed to enable customer account: status %d body: %s", response.StatusCode, string(response.Body)))
		}
		GinkgoWriter.Printf("Customer account %s ready in platform api accounts (RC %s)\n", customerAccountID, accountID)
	})

	It("should be able to create the hcp cluster", Label("hcp-create", "create"), func() {
		defer recordTiming("hcp-cluster-create")()
		GinkgoWriter.Printf("Creating new HCP cluster: %s\n", clusterName)
		cmd := exec.Command(ROSACTL_BIN, "cluster", "create", clusterName, "--region", region, "--output", "json")
		cmd.Env = append(os.Environ(), customerEnv()...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()

		// Check if cluster creation failed due to conflict (cluster already exists)
		if err != nil {
			stderrStr := stderr.String()
			// Check for 409 Conflict or "already exists" in stderr
			if strings.Contains(stderrStr, "409") || strings.Contains(stderrStr, "already exists") || strings.Contains(stderrStr, "Conflict") {
				GinkgoWriter.Printf("Cluster %s already exists (409 Conflict), retrieving existing cluster\n", clusterName)
				// List clusters to find the existing one
				response, listErr := apiClient.Get("/api/v0/clusters?limit=100", accountID)
				Expect(listErr).ToNot(HaveOccurred())
				Expect(response.StatusCode).To(Equal(http.StatusOK))

				var clusterList struct {
					Items []map[string]interface{} `json:"items"`
				}
				Expect(json.Unmarshal(response.Body, &clusterList)).To(Succeed())

				// Find our cluster by name
				var found bool
				for _, item := range clusterList.Items {
					if item["name"] == clusterName {
						clusterID = item["id"].(string)
						if spec, ok := item["spec"].(map[string]interface{}); ok {
							if issuerUrl, ok := spec["cloudUrl"].(string); ok {
								cloudUrl = issuerUrl
							}
						}
						found = true
						break
					}
				}
				Expect(found).To(BeTrue(), "cluster %s should exist after 409 conflict", clusterName)
				hcpCreated = true
				GinkgoWriter.Printf("Found existing HCP cluster ID: %s\n", clusterID)
				GinkgoWriter.Printf("Found existing HCP cluster cloud url: %s\n", cloudUrl)
				return
			}
			Fail("Failed to create the HCP cluster: " + err.Error() + "\nstderr: " + stderrStr)
		}

		if stderr.Len() > 0 {
			GinkgoWriter.Printf("HCP cluster create stderr: %s\n", stderr.String())
		}
		output := stdout.Bytes()

		// Print the create cluster output
		if os.Getenv("E2E_CREATE_CLUSTER_LOG") != "" {
			fmt.Println(string(output))
		}

		var cluster map[string]interface{}
		err = json.Unmarshal(output, &cluster)
		Expect(err).To(BeNil())
		clusterID = cluster["id"].(string)
		if spec, ok := cluster["spec"].(map[string]interface{}); ok {
			if issuerUrl, ok := spec["cloudUrl"].(string); ok {
				cloudUrl = issuerUrl
			}
		}
		hcpCreated = true
		GinkgoWriter.Printf("HCP cluster ID: %s\n", clusterID)
		GinkgoWriter.Printf("HCP cluster cloud url: %s\n", cloudUrl)
		GinkgoWriter.Printf("HCP cluster created successfully: %s\n", clusterName)
	})

	It("should be able to create the cluster-oidc", Label("oidc-create", "setup"), func() {
		defer recordTiming("hcp-oidc-create")()
		GinkgoWriter.Printf("Creating new cluster-oidc: %s\n", clusterName)
		if cloudUrl == "" {
			cloudUrl = os.Getenv("HCP_ROSA_ISSUER_URL")
		}
		GinkgoWriter.Printf("HCP cluster cloud url: %s\n", cloudUrl)
		cmd := exec.Command(ROSACTL_BIN, "cluster-oidc", "create", clusterName, "--region", region, "--oidc-issuer-url", cloudUrl)
		cmd.Env = append(os.Environ(), customerEnv()...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		err := cmd.Run()
		if err != nil {
			Fail("Failed to create the cluster-oidc: " + err.Error())
		}
		oidcCreated = true
		GinkgoWriter.Printf("HCP cluster-oidc created successfully: %s\n", clusterName)
	})

	// it should be able to list the cluster-oidc and find that cluster in the list
	It("should be able to list the cluster-oidc and find that cluster in the list", Label("oidc-list", "setup"), func() {
		GinkgoWriter.Printf("Listing cluster-oidc: %s\n", clusterName)
		cmd := exec.Command(ROSACTL_BIN, "cluster-oidc", "list", "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			Fail("Failed to list the cluster-oidc: " + err.Error())
		}
		fmt.Println(string(output))
		Expect(string(output)).To(ContainSubstring(clusterName))
	})

	// GET /api/v0/clusters/{id} and /statuses use the Hyperfleet resource id (e.g. "2pdl6eud5btdtvgv2f4roaca96e9mvtn"),
	// not the cluster display name. List responses are { "items": [ { "id", "name", "spec", "status", ... } ], ... }.
	It("should be able to wait for the hcp cluster to be ready", Label("cluster-status", "monitor"), func() {
		defer recordTiming("hcp-cluster-ready-wait")()
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "set clusterID from hcp-create (Ordered) or HCP_INSTANCE_ID when running cluster-status alone")

		GinkgoWriter.Printf("Querying platform api /clusters/%s and .../statuses (HCP cluster resource id)\n", id)
		response, err := apiClient.Get("/api/v0/clusters/"+id, accountID)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusOK))
		// get the status from the response body
		var cluster map[string]interface{}
		err = json.Unmarshal(response.Body, &cluster)
		Expect(err).To(BeNil())
		statusRaw, ok := cluster["status"].(map[string]interface{})
		Expect(ok).To(BeTrue(), "cluster response missing status object")
		Expect(statusRaw).ToNot(BeEmpty())
		statusJSON, err := json.MarshalIndent(statusRaw, "", "  ")
		Expect(err).To(BeNil())
		GinkgoWriter.Printf("HCP initial cluster status:\n%s\n", string(statusJSON))

		// Top-level status uses camelCase; message/reason live on conditions[], not on status root.
		// GinkgoWriter.Printf("Cluster status phase: %v lastUpdateTime: %v observedGeneration: %v\n",
		// statusRaw["phase"], statusRaw["lastUpdateTime"], statusRaw["observedGeneration"])

		// Response is pkg/types.ClusterStatusResponse: { "cluster_id", "status", "controller_statuses": [...] }.
		// Poll until reconcilers report every condition as True (right after create they are often False).
		//
		// Logging notes:
		// - Code after a failing g.Expect never runs, so you only see logs that run *before* the assertion that fails.
		// - GinkgoWriter is buffered unless you run `ginkgo -v` (then it usually streams); it is not the same as os.Stdout.
		// - For a snapshot on every poll (including failed attempts), set E2E_STATUS_POLL_LOG=1 (writes to stderr).
		Eventually(func(g Gomega) {
			resp, err := apiClient.Get("/api/v0/clusters/"+id+"/statuses", accountID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var statusEnvelope struct {
				ClusterID          string                   `json:"cluster_id"`
				Status             map[string]interface{}   `json:"status"`
				ControllerStatuses []map[string]interface{} `json:"controller_statuses"`
			}
			g.Expect(json.Unmarshal(resp.Body, &statusEnvelope)).To(Succeed())

			if os.Getenv("E2E_STATUS_POLL_LOG") != "" {
				snap, mErr := json.MarshalIndent(statusEnvelope, "", "  ")
				if mErr == nil {
					_, _ = fmt.Fprintf(os.Stderr, "\n[%s] GET /clusters/%s/statuses (poll)\n%s\n",
						time.Now().Format(time.RFC3339), id, snap)
				}
			}
			GinkgoWriter.Printf("[%s] polled cluster /statuses (stream with: ginkgo -v)\n", time.Now().Format(time.RFC3339))

			g.Expect(statusEnvelope.ControllerStatuses).NotTo(BeEmpty(), "controller_statuses should be populated")

			// Nested JSON arrays decode as []interface{} with map elements, not []map[string]interface{}.
			for _, cs := range statusEnvelope.ControllerStatuses {
				raw, ok := cs["conditions"].([]interface{})
				g.Expect(ok).To(BeTrue(), "controller status should include conditions: %#v", cs)
				g.Expect(raw).NotTo(BeEmpty(), "conditions should be non-empty while cluster reconciles")
				for _, item := range raw {
					cond, ok := item.(map[string]interface{})
					g.Expect(ok).To(BeTrue())
					g.Expect(cond["status"]).To(Equal("True"), "condition %#v should be True", cond)
				}
			}
		}).WithTimeout(35*time.Minute).WithPolling(20*time.Second).Should(Succeed(),
			"all controller_statuses conditions should become True")

		resp, err := apiClient.Get("/api/v0/clusters/"+id+"/statuses", accountID)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var finalStatus map[string]interface{}
		Expect(json.Unmarshal(resp.Body, &finalStatus)).To(Succeed())
		finalJSON, err := json.MarshalIndent(finalStatus, "", "  ")
		Expect(err).ToNot(HaveOccurred())
		GinkgoWriter.Printf("HCP final cluster statuses:\n%s\n", string(finalJSON))
	})

	It("should have valid DNS and TLS for the KAS endpoint", Label("dns-verify", "monitor"), func() {
		defer recordTiming("hcp-dns-tls-verify")()
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "set clusterID from hcp-create (Ordered) or HCP_INSTANCE_ID when running dns-verify alone")

		resp, err := apiClient.Get("/api/v0/clusters/"+id+"/statuses", accountID)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var statusEnvelope struct {
			ControllerStatuses []struct {
				Data map[string]interface{} `json:"data"`
			} `json:"controller_statuses"`
		}
		Expect(json.Unmarshal(resp.Body, &statusEnvelope)).To(Succeed())

		var apiEndpoint string
		for _, cs := range statusEnvelope.ControllerStatuses {
			if hc, ok := cs.Data["hostedCluster"].(map[string]interface{}); ok {
				if ep, ok := hc["apiEndpoint"].(string); ok && ep != "" {
					apiEndpoint = ep
					break
				}
			}
		}
		Expect(apiEndpoint).ToNot(BeEmpty(), "apiEndpoint should be present in controller_statuses after cluster is Ready")
		GinkgoWriter.Printf("KAS apiEndpoint: %s\n", apiEndpoint)

		parsedURL, err := url.Parse(apiEndpoint)
		Expect(err).ToNot(HaveOccurred())
		hostname := parsedURL.Hostname()
		port := parsedURL.Port()
		if port == "" {
			port = "443"
		}

		hostPort := net.JoinHostPort(hostname, port)

		Eventually(func(g Gomega) {
			addrs, err := net.LookupHost(hostname)
			g.Expect(err).ToNot(HaveOccurred(), "DNS should resolve for %s", hostname)
			g.Expect(addrs).ToNot(BeEmpty())
			GinkgoWriter.Printf("DNS resolved %s to %v\n", hostname, addrs)

			conn, err := tls.DialWithDialer(
				&net.Dialer{Timeout: 10 * time.Second},
				"tcp", hostPort,
				&tls.Config{},
			)
			g.Expect(err).ToNot(HaveOccurred(), "TLS handshake should succeed for %s", hostPort)
			g.Expect(conn.Close()).To(Succeed())
			GinkgoWriter.Printf("TLS handshake succeeded for %s\n", hostPort)
		}).WithTimeout(2 * time.Minute).WithPolling(10 * time.Second).Should(Succeed())
	})

	It("should have nodepools ready", Label("nodepools-wait", "monitor"), func() {
		defer recordTiming("hcp-nodepools-wait")()
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "set clusterID from hcp-create (Ordered) or HCP_INSTANCE_ID when running nodepools-wait alone")

		GinkgoWriter.Printf("Polling resource_bundles for NodePool readyCondition (cluster %s)\n", id)

		Eventually(func(g Gomega) {
			searchQuery := fmt.Sprintf("payload->'metadata'->'labels'->>'hyperfleet.io/cluster-id'='%s'", id)
			resp, err := apiClient.Get("/api/v0/resource_bundles?search="+url.QueryEscape(searchQuery)+"&size=100", accountID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var list struct {
				Items []map[string]interface{} `json:"items"`
			}
			g.Expect(json.Unmarshal(resp.Body, &list)).To(Succeed())
			g.Expect(list.Items).NotTo(BeEmpty(), "no resource bundles found for cluster %s", id)

			foundNodePool := false
			for _, bundle := range list.Items {
				manifests, _ := bundle["manifests"].([]interface{})
				status, _ := bundle["status"].(map[string]interface{})
				resourceStatuses, _ := status["resourceStatus"].([]interface{})

				for i, raw := range manifests {
					m, _ := raw.(map[string]interface{})
					if m == nil || m["kind"] != "NodePool" {
						continue
					}
					foundNodePool = true
					meta, _ := m["metadata"].(map[string]interface{})
					name, _ := meta["name"].(string)

					readyValue := ""
					if i < len(resourceStatuses) {
						rs, _ := resourceStatuses[i].(map[string]interface{})
						feedback, _ := rs["statusFeedback"].(map[string]interface{})
						values, _ := feedback["values"].([]interface{})
						for _, v := range values {
							vm, _ := v.(map[string]interface{})
							if vm["name"] == "readyCondition" {
								fv, _ := vm["fieldValue"].(map[string]interface{})
								readyValue, _ = fv["string"].(string)
							}
						}
					}

					if os.Getenv("E2E_STATUS_POLL_LOG") != "" {
						_, _ = fmt.Fprintf(os.Stderr, "[%s] nodepool %s: readyCondition=%s\n",
							time.Now().Format(time.RFC3339), name, readyValue)
					}
					GinkgoWriter.Printf("  nodepool %s: readyCondition=%s\n", name, readyValue)
					g.Expect(readyValue).To(Equal("True"), "nodepool %s readyCondition should be True, got %s", name, readyValue)
				}
			}
			g.Expect(foundNodePool).To(BeTrue(), "no NodePool manifest found in resource bundles for cluster %s", id)
		}).WithTimeout(15*time.Minute).WithPolling(30*time.Second).Should(Succeed(),
			"all nodepools should have readyCondition=True")

		GinkgoWriter.Printf("All nodepools ready for cluster %s\n", id)
	})

	It("should have hcp:hostedcluster_available metric in Thanos", Label("hcp-metrics", "monitor"), func() {
		rhobsAPIURL := os.Getenv("E2E_RHOBS_API_URL")
		if rhobsAPIURL == "" {
			Skip("E2E_RHOBS_API_URL not set — skipping HCP metrics test")
		}
		rhobsClient := awstest.NewAPIClient(rhobsAPIURL)
		query := `count(hcp:hostedcluster_available)`
		Eventually(func() bool {
			resp := thanos.Query(rhobsClient, query)
			return resp.Status == "success" && len(resp.Data.Result) > 0
		}, "5m", "15s").Should(BeTrue(),
			"Expected hcp:hostedcluster_available metric to be queryable in Thanos "+
				"(PrometheusRule → Thanos Ruler evaluation)")
	})

	It("should be able to delete the hcp cluster", Label("hcp-delete", "cleanup"), func() {
		defer recordTiming("hcp-cluster-delete")()
		if clusterID == "" {
			clusterID = os.Getenv("HCP_INSTANCE_ID")
			if clusterID == "" {
				Skip("clusterID not set - run full Ordered suite or set HCP_INSTANCE_ID")
			}
		}
		GinkgoWriter.Printf("Deleting the hcp clusterId: %s\n", clusterID)
		response, err := apiClient.Delete("/api/v0/clusters/"+clusterID, accountID)
		Expect(err).ToNot(HaveOccurred())
		Expect(response.StatusCode).To(Equal(http.StatusAccepted))
		GinkgoWriter.Printf("HCP cluster deleted successfully: %s\n", clusterName)
	})

	// it should be able to query the /cluster/id until it is deleted
	It("should be able to query the /cluster/id until it is deleted", Label("hcp-delete", "cluster-query", "cleanup"), func() {
		defer recordTiming("hcp-cluster-delete-wait")()
		GinkgoWriter.Printf("Querying the hcp clusterId: %s\n", clusterID)
		if clusterID == "" {
			clusterID = os.Getenv("HCP_INSTANCE_ID")
			if clusterID == "" {
				Skip("clusterID not set - run full Ordered suite or set HCP_INSTANCE_ID")
			}
		}
		Eventually(func(g Gomega) {
			response, err := apiClient.Get("/api/v0/clusters/"+clusterID, accountID)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.StatusCode).To(Or(Equal(http.StatusNotFound), Equal(http.StatusGone)))
		}).WithTimeout(10*time.Minute).WithPolling(30*time.Second).Should(Succeed(), "cluster should be deleted")
		GinkgoWriter.Printf("HCP cluster deleted successfully: %s\n", clusterName)
	})

	It("should be able to delete the resource bundles", Label("hcp-delete", "bundles-delete", "cleanup"), func() {
		defer recordTiming("hcp-bundles-delete")()
		if clusterID == "" {
			clusterID = os.Getenv("HCP_INSTANCE_ID")
			if clusterID == "" {
				Skip("clusterID not set - run full Ordered suite or set HCP_INSTANCE_ID")
			}
		}

		deleted := deleteClusterBundles(apiClient, clusterID, accountID)
		GinkgoWriter.Printf("Deleted %d resource bundles for cluster %s\n", deleted, clusterID)
	})

	It("should wait for resource bundles to be fully removed", Label("bundles-wait", "cleanup"), func() {
		defer recordTiming("hcp-bundles-wait")()
		if clusterID == "" {
			clusterID = os.Getenv("HCP_INSTANCE_ID")
			if clusterID == "" {
				Skip("clusterID not set - run full Ordered suite or set HCP_INSTANCE_ID")
			}
		}

		GinkgoWriter.Printf("Waiting for resource bundles for cluster %s to be fully removed...\n", clusterID)

		Eventually(func(g Gomega) {
			g.Expect(listClusterBundles(apiClient, clusterID, accountID)).To(BeEmpty(),
				"resource bundles for cluster %s still exist", clusterID)
		}).WithTimeout(15*time.Minute).WithPolling(30*time.Second).Should(Succeed(),
			"resource bundles should be fully removed before proceeding with infrastructure teardown")

		GinkgoWriter.Printf("All resource bundles for cluster %s have been removed\n", clusterID)
	})

	It("should be able to delete the cluster-oidc", Label("oidc-delete", "cleanup"), func() {
		defer recordTiming("hcp-oidc-delete")()
		GinkgoWriter.Printf("Deleting the cluster-oidc: %s\n", clusterName)
		cmd := exec.Command(ROSACTL_BIN, "cluster-oidc", "delete", clusterName, "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			Fail(fmt.Sprintf("Failed to delete the cluster-oidc: %v\nOutput:\n%s", err, string(output)))
		}
		GinkgoWriter.Printf("Cluster-OIDC deleted successfully: %s\n", clusterName)
	})

	// Delete cluster-vpc with up to 3 attempts; fail the spec if all attempts return an error.
	It("should be able to try to delete the cluster-vpc, trying 3 times", Label("vpc-delete", "cleanup"), func() {
		defer recordTiming("hcp-vpc-delete")()
		const maxAttempts = 3
		const backoffBetweenAttempts = 5 * time.Minute

		var lastErr error
		var lastOutput []byte
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			GinkgoWriter.Printf("cluster-vpc delete attempt %d/%d\n", attempt, maxAttempts)

			// before trying to delete the cluster-vpc, we should list and
			// grep if the cluster-vpc is still there
			cmd := exec.Command(ROSACTL_BIN, "cluster-vpc", "list", "--region", region)
			cmd.Env = append(os.Environ(), customerEnv()...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				Fail(fmt.Sprintf("Failed to list the cluster-vpc: %v\nOutput:\n%s", err, string(output)))
			}
			if !strings.Contains(string(output), clusterName) {
				GinkgoWriter.Printf("cluster-vpc does not exist: %s\n", clusterName)
				return
			}

			cmd = exec.Command(ROSACTL_BIN, "cluster-vpc", "delete", clusterName, "--region", region)
			cmd.Env = append(os.Environ(), customerEnv()...)
			// rosactl may block with its own internal wait
			output, err = cmd.CombinedOutput()
			if err == nil {
				GinkgoWriter.Printf("cluster-vpc deleted successfully: %s\n", clusterName)
				return
			}
			lastErr, lastOutput = err, output
			GinkgoWriter.Printf("cluster-vpc delete attempt %d failed: %v\nOutput:\n%s\n", attempt, err, string(output))
			if attempt < maxAttempts {
				time.Sleep(backoffBetweenAttempts)
			}
		}
		Fail(fmt.Sprintf("cluster-vpc delete failed after %d attempts: %v\nOutput:\n%s", maxAttempts, lastErr, string(lastOutput)))
	})

	It("should be able to delete the cluster-iam", Label("iam-delete", "cleanup"), func() {
		defer recordTiming("hcp-iam-delete")()
		GinkgoWriter.Printf("Deleting the cluster-iam: %s\n", clusterName)
		cmd := exec.Command(ROSACTL_BIN, "cluster-iam", "delete", clusterName, "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			Fail(fmt.Sprintf("Failed to delete the cluster-iam: %v\nOutput:\n%s", err, string(output)))
		}
		GinkgoWriter.Printf("Cluster-IAM deleted successfully: %s\n", clusterName)

		cleanupCompleted = true
	})

})
