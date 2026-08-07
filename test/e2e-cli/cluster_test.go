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
//   hcp-create, oidc-create, oidc-list, cluster-status, kubeconfig,
//   nodepool-create, nodepool-list, dns-verify, nodepools-wait, nodepool-delete,
//   hcp-patch, cluster-delete, bundles-delete, bundles-wait, oidc-delete, iam-delete, vpc-delete
//
// Group labels: setup, create, monitor, update, cleanup

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	awstest "github.com/openshift-online/rosa-hyperfleet-api/test/helpers/aws"
	"github.com/openshift-online/rosa-hyperfleet-api/test/helpers/thanos"
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
		defer func() { _ = f.Close() }()
		_, _ = fmt.Fprintln(f, record)
	}
}

func customerEnv() []string {
	return []string{"AWS_PROFILE=" + os.Getenv("CUSTOMER_AWS_PROFILE")}
}

func fireAndForgetInfraDelete(rosactlBin, clusterName, region string, resources []string) {
	for _, subCmd := range resources {
		GinkgoWriter.Printf("Cleanup: firing %s delete %s (fire-and-forget)\n", subCmd, clusterName)
		args := []string{subCmd, "delete", clusterName, "--region", region, "--no-wait"}
		cmd := exec.Command(rosactlBin, args...)
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
		oidcIssuerURL     string
		region            string
		apiClient         *awstest.APIClient
		customerApiClient *awstest.APIClient

		// Track which resources were created so DeferCleanup knows what to tear down.
		hcpCreated      bool
		vpcCreated      bool
		iamCreated      bool
		oidcCreated     bool
		nodepoolCreated bool
		nodepoolID      string

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
		customerApiClient = awstest.NewAPIClient(baseURL)
		customerApiClient.AWSProfile = os.Getenv("CUSTOMER_AWS_PROFILE")

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

			if nodepoolCreated && nodepoolID != "" {
				GinkgoWriter.Printf("Cleanup: deleting nodepool %s\n", nodepoolID)
				resp, err := customerApiClient.Delete("/api/v0/nodepools/"+nodepoolID, customerAccountID)
				if err != nil {
					GinkgoWriter.Printf("Cleanup WARNING: failed to call delete nodepool API: %v\n", err)
				} else if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNotFound {
					GinkgoWriter.Printf("Cleanup WARNING: delete nodepool returned status %d: %s\n", resp.StatusCode, string(resp.Body))
				} else {
					GinkgoWriter.Printf("Cleanup: nodepool delete accepted (status %d)\n", resp.StatusCode)
				}
			}

			if hcpCreated && clusterID != "" {
				GinkgoWriter.Printf("Cleanup: deleting HCP cluster %s (id: %s)\n", clusterName, clusterID)
				resp, err := customerApiClient.Delete("/api/v0/clusters/"+clusterID, customerAccountID)
				if err != nil {
					GinkgoWriter.Printf("Cleanup WARNING: failed to call delete cluster API: %v\n", err)
				} else if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusNotFound {
					GinkgoWriter.Printf("Cleanup WARNING: delete cluster returned status %d: %s\n", resp.StatusCode, string(resp.Body))
				} else {
					GinkgoWriter.Printf("Cleanup: HCP cluster delete accepted (status %d)\n", resp.StatusCode)
					deadline := time.Now().Add(5 * time.Minute)
					for time.Now().Before(deadline) {
						time.Sleep(15 * time.Second)
						r, e := customerApiClient.Get("/api/v0/clusters/"+clusterID, customerAccountID)
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
	// create-vpc returns immediately (async), so poll until the VPC appears
	XIt("should be able to list the cluster-vpc and find that cluster in the list", Label("vpc-list", "setup"), func() {
		GinkgoWriter.Printf("Waiting for cluster-vpc %s to appear in list\n", clusterName)
		Eventually(func() string {
			cmd := exec.Command(ROSACTL_BIN, "cluster-vpc", "list", "--region", region)
			cmd.Env = append(os.Environ(), customerEnv()...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				GinkgoWriter.Printf("cluster-vpc list failed: %s\n", err.Error())
				return ""
			}
			GinkgoWriter.Printf("cluster-vpc list output: %s\n", string(output))
			return string(output)
		}, 5*time.Minute, 60*time.Second).Should(ContainSubstring(clusterName))
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
				response, listErr := customerApiClient.Get("/api/v0/clusters?limit=100", customerAccountID)
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
						if issuerUrl, ok := item["oidc_issuer_url"].(string); ok {
							oidcIssuerURL = issuerUrl
						}
						found = true
						break
					}
				}
				Expect(found).To(BeTrue(), "cluster %s should exist after 409 conflict", clusterName)
				hcpCreated = true
				GinkgoWriter.Printf("Found existing HCP cluster ID: %s\n", clusterID)
				GinkgoWriter.Printf("Found existing HCP cluster OIDC issuer URL: %s\n", oidcIssuerURL)
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
		if issuerUrl, ok := cluster["oidc_issuer_url"].(string); ok {
			oidcIssuerURL = issuerUrl
		}
		hcpCreated = true
		GinkgoWriter.Printf("HCP cluster ID: %s\n", clusterID)
		GinkgoWriter.Printf("HCP cluster OIDC issuer URL: %s\n", oidcIssuerURL)
		GinkgoWriter.Printf("HCP cluster created successfully: %s\n", clusterName)
	})

	It("should be able to create the cluster-oidc", Label("oidc-create", "setup"), func() {
		defer recordTiming("hcp-oidc-create")()
		GinkgoWriter.Printf("Creating new cluster-oidc: %s\n", clusterName)
		if oidcIssuerURL == "" {
			oidcIssuerURL = os.Getenv("HCP_ROSA_ISSUER_URL")
		}
		Expect(oidcIssuerURL).ToNot(BeEmpty(), "OIDC issuer URL is required; set HCP_ROSA_ISSUER_URL or ensure the cluster response includes it")
		GinkgoWriter.Printf("HCP cluster OIDC issuer URL: %s\n", oidcIssuerURL)
		cmd := exec.Command(ROSACTL_BIN, "cluster-oidc", "create", clusterName, "--region", region, "--oidc-issuer-url", oidcIssuerURL)
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
	XIt("should be able to list the cluster-oidc and find that cluster in the list", Label("oidc-list", "setup"), func() {
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
		var statusRaw map[string]interface{}
		Eventually(func(g Gomega) {
			response, err := customerApiClient.Get("/api/v0/clusters/"+id, customerAccountID)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.StatusCode).To(Equal(http.StatusOK))
			var cluster map[string]interface{}
			g.Expect(json.Unmarshal(response.Body, &cluster)).To(Succeed())
			s, ok := cluster["status"].(map[string]interface{})
			g.Expect(ok).To(BeTrue(), "cluster response missing status object")
			g.Expect(s).ToNot(BeEmpty())
			statusRaw = s
		}).WithTimeout(30*time.Second).WithPolling(5*time.Second).Should(Succeed(),
			"operator should set cluster status")
		statusJSON, err := json.MarshalIndent(statusRaw, "", "  ")
		Expect(err).To(BeNil())
		GinkgoWriter.Printf("HCP initial cluster status:\n%s\n", string(statusJSON))

		// Top-level status uses camelCase; message/reason live on conditions[], not on status root.
		// GinkgoWriter.Printf("Cluster status phase: %v lastUpdateTime: %v observedGeneration: %v\n",
		// statusRaw["phase"], statusRaw["lastUpdateTime"], statusRaw["observedGeneration"])

		// Poll until the operator sets status.phase to "Ready".
		// The hyperfleet-operator advances phase to Ready once Available=True
		// and Degraded!=True on the Cluster CR, so this is the single
		// authoritative readiness signal.
		Eventually(func(g Gomega) {
			resp, err := customerApiClient.Get("/api/v0/clusters/"+id+"/statuses", customerAccountID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var statusEnvelope struct {
				ClusterID string                 `json:"cluster_id"`
				Status    map[string]interface{} `json:"status"`
			}
			g.Expect(json.Unmarshal(resp.Body, &statusEnvelope)).To(Succeed())

			if os.Getenv("E2E_STATUS_POLL_LOG") != "" {
				snap, mErr := json.MarshalIndent(statusEnvelope, "", "  ")
				if mErr == nil {
					_, _ = fmt.Fprintf(os.Stderr, "\n[%s] GET /clusters/%s/statuses (poll)\n%s\n",
						time.Now().Format(time.RFC3339), id, snap)
				}
			}

			g.Expect(statusEnvelope.Status).NotTo(BeNil(), "status should be present")
			phase, _ := statusEnvelope.Status["phase"].(string)
			GinkgoWriter.Printf("[%s] polled cluster /statuses — phase=%s\n", time.Now().Format(time.RFC3339), phase)
			g.Expect(phase).To(Equal("Ready"), "cluster phase should be Ready, got %s", phase)
		}).WithTimeout(35*time.Minute).WithPolling(20*time.Second).Should(Succeed(),
			"cluster status.phase should become Ready")

		resp, err := customerApiClient.Get("/api/v0/clusters/"+id+"/statuses", customerAccountID)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var finalStatus map[string]interface{}
		Expect(json.Unmarshal(resp.Body, &finalStatus)).To(Succeed())
		finalJSON, err := json.MarshalIndent(finalStatus, "", "  ")
		Expect(err).ToNot(HaveOccurred())
		GinkgoWriter.Printf("HCP final cluster statuses:\n%s\n", string(finalJSON))
	})

	It("should generate a working kubeconfig", Label("kubeconfig", "monitor"), func() {
		defer recordTiming("hcp-kubeconfig")()
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "clusterID required — run full Ordered suite or set HCP_INSTANCE_ID")

		name := clusterName
		if name == "" {
			name = os.Getenv("HCP_CLUSTER_NAME")
		}
		Expect(name).ToNot(BeEmpty(), "clusterName required — run full Ordered suite or set HCP_CLUSTER_NAME")

		GinkgoWriter.Printf("Generating kubeconfig for cluster %s (id=%s)\n", name, id)

		cmd := exec.Command(ROSACTL_BIN, "cluster", "kubeconfig", name, "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		Expect(err).ToNot(HaveOccurred(), "rosactl cluster kubeconfig failed: %s", stderr.String())
		Expect(stdout.Len()).To(BeNumerically(">", 0), "kubeconfig output should not be empty")

		kubeconfigFile, err := os.CreateTemp("", "e2e-kubeconfig-*.yaml")
		Expect(err).ToNot(HaveOccurred())
		defer func() {
			kubeconfigFile.Close()
			os.Remove(kubeconfigFile.Name())
		}()
		_, err = kubeconfigFile.Write(stdout.Bytes())
		Expect(err).ToNot(HaveOccurred())
		Expect(kubeconfigFile.Close()).To(Succeed())

		if _, lookErr := exec.LookPath("kubectl"); lookErr != nil {
			GinkgoWriter.Printf("kubectl not found in PATH, skipping healthz validation\n")
			Skip("kubectl not available in this environment")
		}
		GinkgoWriter.Printf("Validating kubeconfig with kubectl (file=%s)\n", kubeconfigFile.Name())
		healthCmd := exec.Command("kubectl", "--kubeconfig", kubeconfigFile.Name(), "get", "--raw", "/healthz")
		healthCmd.Env = append(os.Environ(), customerEnv()...)
		healthOutput, err := healthCmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "kubectl get --raw /healthz failed:\n%s", string(healthOutput))
		Expect(strings.TrimSpace(string(healthOutput))).To(Equal("ok"), "healthz should return ok")
		GinkgoWriter.Printf("kubectl healthz check passed\n")
	})

	It("should be able to create a nodepool via CLI", Label("nodepool-create", "monitor"), func() {
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "clusterID required — run full Ordered suite or set HCP_INSTANCE_ID")

		npName := "e2e-np-" + clusterName
		GinkgoWriter.Printf("Creating nodepool %s for cluster %s\n", npName, id)

		cmd := exec.Command(ROSACTL_BIN, "nodepool", "create", npName,
			"--cluster-id", id,
			"--region", region,
			"--output", "json",
		)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "rosactl nodepool create failed:\n%s", string(output))

		var result map[string]interface{}
		Expect(json.Unmarshal(output, &result)).To(Succeed(), "failed to parse nodepool create response:\n%s", string(output))

		id2, ok := result["id"].(string)
		Expect(ok).To(BeTrue(), "response missing 'id' field")
		Expect(id2).ToNot(BeEmpty())

		nodepoolID = id2
		nodepoolCreated = true
		GinkgoWriter.Printf("Nodepool created: id=%s name=%s\n", nodepoolID, npName)
	})

	It("should be able to list nodepools via CLI", Label("nodepool-list", "monitor"), func() {
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "clusterID required — run full Ordered suite or set HCP_INSTANCE_ID")

		cmd := exec.Command(ROSACTL_BIN, "nodepool", "list",
			"--cluster-id", id,
			"--region", region,
			"--output", "json",
		)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "rosactl nodepool list failed:\n%s", string(output))

		var result struct {
			Items []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		}
		Expect(json.Unmarshal(output, &result)).To(Succeed(), "failed to parse nodepool list response:\n%s", string(output))
		Expect(result.Items).ToNot(BeEmpty(), "nodepool list should contain at least one item")

		if nodepoolID != "" {
			found := false
			for _, np := range result.Items {
				if np.ID == nodepoolID {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "created nodepool %s should appear in list", nodepoolID)
		}

		GinkgoWriter.Printf("Listed %d nodepools for cluster %s\n", len(result.Items), id)
	})

	It("should have valid DNS and TLS for the KAS endpoint", Label("dns-verify", "monitor"), func() {
		defer recordTiming("hcp-dns-tls-verify")()
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "set clusterID from hcp-create (Ordered) or HCP_INSTANCE_ID when running dns-verify alone")

		resp, err := customerApiClient.Get("/api/v0/clusters/"+id+"/statuses", customerAccountID)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var statusEnvelope struct {
			Status struct {
				ControlPlaneEndpoint struct {
					Host string `json:"host"`
					Port int32  `json:"port"`
				} `json:"controlPlaneEndpoint"`
			} `json:"status"`
		}
		Expect(json.Unmarshal(resp.Body, &statusEnvelope)).To(Succeed())

		ep := statusEnvelope.Status.ControlPlaneEndpoint
		Expect(ep.Host).ToNot(BeEmpty(), "controlPlaneEndpoint.host should be present in status after cluster is Ready")
		GinkgoWriter.Printf("KAS controlPlaneEndpoint: %s:%d\n", ep.Host, ep.Port)

		hostname := ep.Host
		port := "6443"
		if ep.Port > 0 {
			port = fmt.Sprintf("%d", ep.Port)
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

		GinkgoWriter.Printf("Polling nodepools for readiness (cluster %s)\n", id)

		Eventually(func(g Gomega) {
			resp, err := customerApiClient.Get("/api/v0/nodepools", customerAccountID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var list struct {
				Items []map[string]interface{} `json:"items"`
			}
			g.Expect(json.Unmarshal(resp.Body, &list)).To(Succeed())

			foundNodePool := false
			for _, np := range list.Items {
				npClusterID, _ := np["cluster_id"].(string)
				if npClusterID != id {
					continue
				}
				foundNodePool = true
				npID, _ := np["id"].(string)
				npName, _ := np["name"].(string)

				statusResp, err := customerApiClient.Get("/api/v0/nodepools/"+npID+"/status", customerAccountID)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(statusResp.StatusCode).To(Equal(http.StatusOK))

				var statusBody struct {
					Status struct {
						Phase string `json:"phase"`
					} `json:"status"`
				}
				g.Expect(json.Unmarshal(statusResp.Body, &statusBody)).To(Succeed())

				phase := statusBody.Status.Phase
				if os.Getenv("E2E_STATUS_POLL_LOG") != "" {
					_, _ = fmt.Fprintf(os.Stderr, "[%s] nodepool %s: phase=%s\n",
						time.Now().Format(time.RFC3339), npName, phase)
				}
				GinkgoWriter.Printf("  nodepool %s: phase=%s\n", npName, phase)
				g.Expect(phase).To(Equal("Ready"), "nodepool %s should be Ready", npName)
			}
			g.Expect(foundNodePool).To(BeTrue(), "no nodepools found for cluster %s", id)
		}).WithTimeout(20*time.Minute).WithPolling(30*time.Second).Should(Succeed(),
			"all nodepools should be ready")

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

	It("should be able to delete the extra nodepool", Label("nodepool-delete", "cleanup"), func() {
		if nodepoolID == "" {
			Skip("no nodepool was created — nothing to delete")
		}
		GinkgoWriter.Printf("Deleting nodepool %s\n", nodepoolID)

		cmd := exec.Command(ROSACTL_BIN, "nodepool", "delete", nodepoolID,
			"--region", region,
		)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "rosactl nodepool delete failed:\n%s", string(output))
		GinkgoWriter.Printf("Nodepool %s deletion initiated\n", nodepoolID)
	})

	It("should be able to delete the hcp cluster via CLI", Label("hcp-delete", "cluster-delete", "cleanup"), func() {
		defer recordTiming("hcp-cluster-delete")()
		if clusterID == "" {
			clusterID = os.Getenv("HCP_INSTANCE_ID")
			if clusterID == "" {
				Skip("clusterID not set - run full Ordered suite or set HCP_INSTANCE_ID")
			}
		}
		GinkgoWriter.Printf("Deleting HCP cluster %q (id: %s) via rosactl\n", clusterName, clusterID)

		// Use cluster ID directly to avoid a name-lookup round-trip; --yes skips the confirmation prompt
		cmd := exec.Command(ROSACTL_BIN, "cluster", "delete", clusterID,
			"--region", region,
			"--yes",
		)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		Expect(err).ToNot(HaveOccurred(), "rosactl cluster delete failed:\n%s", string(output))
		GinkgoWriter.Printf("rosactl cluster delete output:\n%s\n", string(output))
		GinkgoWriter.Printf("HCP cluster deletion initiated: %s\n", clusterName)
	})

	// Poll via the platform API until the cluster is no longer present
	It("should confirm the hcp cluster is deleted", Label("hcp-delete", "cluster-delete", "cluster-query", "cleanup"), func() {
		defer recordTiming("hcp-cluster-delete-wait")()
		if clusterID == "" {
			clusterID = os.Getenv("HCP_INSTANCE_ID")
			if clusterID == "" {
				Skip("clusterID not set - run full Ordered suite or set HCP_INSTANCE_ID")
			}
		}
		GinkgoWriter.Printf("Waiting for HCP cluster %q (id: %s) to be fully deleted\n", clusterName, clusterID)
		Eventually(func(g Gomega) {
			response, err := customerApiClient.Get("/api/v0/clusters/"+clusterID, customerAccountID)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.StatusCode).To(Or(Equal(http.StatusNotFound), Equal(http.StatusGone)))
		}).WithTimeout(10*time.Minute).WithPolling(30*time.Second).Should(Succeed(), "cluster should be deleted")
		GinkgoWriter.Printf("HCP cluster confirmed deleted: %s\n", clusterName)
	})

	It("should be able to delete the cluster-oidc", Label("oidc-delete", "cleanup"), func() {
		defer recordTiming("hcp-oidc-delete")()
		if os.Getenv("ROSA_REGIONAL_TEARDOWN_FIRE_AND_FORGET") == "true" {
			GinkgoWriter.Printf("Fire-and-forget mode: launching async deletes for infra stacks\n")
			fireAndForgetInfraDelete(ROSACTL_BIN, clusterName, region, []string{"cluster-oidc"})
			// cleanupCompleted = true
			return
		}
		GinkgoWriter.Printf("Deleting the cluster-oidc: %s\n", clusterName)
		cmd := exec.Command(ROSACTL_BIN, "cluster-oidc", "delete", clusterName, "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			Fail(fmt.Sprintf("Failed to delete the cluster-oidc: %v\nOutput:\n%s", err, string(output)))
		}
		GinkgoWriter.Printf("Cluster-OIDC deleted successfully: %s\n", clusterName)
	})

	It("should be able to delete the cluster-vpc", Label("vpc-delete", "cleanup"), func() {
		defer recordTiming("hcp-vpc-delete")()
		if os.Getenv("ROSA_REGIONAL_TEARDOWN_FIRE_AND_FORGET") == "true" {
			GinkgoWriter.Printf("Fire-and-forget mode: launching async deletes for infra stacks\n")
			fireAndForgetInfraDelete(ROSACTL_BIN, clusterName, region, []string{"cluster-vpc"})
			// cleanupCompleted = true
			return
		}
		GinkgoWriter.Printf("Deleting cluster-vpc: %s\n", clusterName)
		cmd := exec.Command(ROSACTL_BIN, "cluster-vpc", "delete", clusterName, "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			Fail(fmt.Sprintf("Failed to delete the cluster-vpc: %v\nOutput:\n%s", err, string(output)))
		}
		GinkgoWriter.Printf("cluster-vpc deleted successfully: %s\n", clusterName)
	})

	It("should be able to delete the cluster-iam", Label("iam-delete", "cleanup"), func() {
		defer recordTiming("hcp-iam-delete")()
		if os.Getenv("ROSA_REGIONAL_TEARDOWN_FIRE_AND_FORGET") == "true" {
			GinkgoWriter.Printf("Fire-and-forget mode: launching async deletes for infra stacks\n")
			fireAndForgetInfraDelete(ROSACTL_BIN, clusterName, region, []string{"cluster-iam"})
			cleanupCompleted = true
			return
		}
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
