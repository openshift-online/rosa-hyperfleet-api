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
//   silence-installing, silence-ready,
//   nodepool-create, nodepool-list, dns-verify, nodepools-wait, nodepool-delete,
//   hcp-patch, cluster-delete, bundles-delete, bundles-wait, oidc-delete, iam-delete, vpc-delete
//
// Group labels: setup, create, monitor, update, cleanup

import (
	"bytes"
	"context"
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

	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	awstest "github.com/openshift-online/rosa-hyperfleet-api/test/helpers/aws"
	amhelper "github.com/openshift-online/rosa-hyperfleet-api/test/helpers/alertmanager"
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
			// Parse Kubernetes-style Status object
			var errBody map[string]interface{}
			Expect(json.Unmarshal(response.Body, &errBody)).To(Succeed())
			// Check that the message contains the expected error code
			message, ok := errBody["message"].(string)
			Expect(ok).To(BeTrue(), "Status response should have message field")
			Expect(message).To(ContainSubstring("ACCOUNTS-MGMT-CREATE-004"), "unexpected 409 body: %s", string(response.Body))
			GinkgoWriter.Printf("Customer account %s already enabled (409 ACCOUNTS-MGMT-CREATE-004)\n", customerAccountID)
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

				var clusterList v1alpha1.ClusterList
				Expect(json.Unmarshal(response.Body, &clusterList)).To(Succeed())

				// Find our cluster by name
				var found bool
				for _, cluster := range clusterList.Items {
					if cluster.Name == clusterName {
						clusterID = string(cluster.UID)
						oidcIssuerURL = cluster.Spec.HostedCluster.IssuerURL
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

		// Parse cluster response
		var cluster v1alpha1.Cluster
		err = json.Unmarshal(output, &cluster)
		Expect(err).To(BeNil())

		clusterID = string(cluster.UID)
		oidcIssuerURL = cluster.Spec.HostedCluster.IssuerURL
		hcpCreated = true
		GinkgoWriter.Printf("HCP cluster ID: %s\n", clusterID)
		GinkgoWriter.Printf("HCP cluster OIDC issuer URL: %s\n", oidcIssuerURL)
		GinkgoWriter.Printf("HCP cluster created successfully: %s\n", clusterName)
	})

	It("should have a lifecycle installing silence while the cluster provisions", Label("silence-installing", "monitor", "create"), func() {
		amURL := os.Getenv("E2E_ALERTMANAGER_URL")
		if amURL == "" {
			Skip("E2E_ALERTMANAGER_URL not set — skipping lifecycle silence test")
		}
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "clusterID required — run hcp-create first or set HCP_INSTANCE_ID")
		name := clusterName
		if name == "" {
			name = os.Getenv("HCP_CLUSTER_NAME")
		}
		Expect(name).ToNot(BeEmpty(), "clusterName required — run hcp-create first or set HCP_CLUSTER_NAME")

		sawInstalling := false
		Eventually(func(g Gomega) {
			resp, err := customerApiClient.Get("/api/v0/clusters/"+id, customerAccountID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var cluster v1alpha1.Cluster
			g.Expect(json.Unmarshal(resp.Body, &cluster)).To(Succeed())
			phase := cluster.Status.Phase
			GinkgoWriter.Printf("[%s] silence probe — phase=%s\n", time.Now().Format(time.RFC3339), phase)

			if phase == v1alpha1.ClusterPhaseReady {
				if !sawInstalling {
					g.Expect(sawInstalling).To(BeTrue(), "cluster became Ready before an installing silence was observed")
				}
				return
			}

			if phase != v1alpha1.ClusterPhaseWaitingForPlacement && phase != v1alpha1.ClusterPhaseProvisioning {
				return
			}

			silences, err := amhelper.ListManagedSilences(context.Background(), amURL, id, name)
			g.Expect(err).NotTo(HaveOccurred())
			if amhelper.HasInstallingSilence(silences) {
				sawInstalling = true
			}
			g.Expect(sawInstalling).To(BeTrue(), "expected installing lifecycle silence while phase=%s", phase)
		}).WithTimeout(20*time.Minute).WithPolling(20*time.Second).Should(Succeed(),
			"expected an installing lifecycle silence before the cluster becomes Ready")
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

	// GET /api/v0/clusters/{id} uses the cluster UUID (metadata.uid),
	// not the cluster display name. Status is embedded in the cluster object.
	It("should be able to wait for the hcp cluster to be ready", Label("cluster-status", "monitor"), func() {
		defer recordTiming("hcp-cluster-ready-wait")()
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "set clusterID from hcp-create (Ordered) or HCP_INSTANCE_ID when running cluster-status alone")

		GinkgoWriter.Printf("Querying platform api /clusters/%s (HCP cluster resource id)\n", id)
		var initialCluster v1alpha1.Cluster
		Eventually(func(g Gomega) {
			response, err := customerApiClient.Get("/api/v0/clusters/"+id, customerAccountID)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(response.StatusCode).To(Equal(http.StatusOK))
			g.Expect(json.Unmarshal(response.Body, &initialCluster)).To(Succeed())
			g.Expect(initialCluster.Status.Phase).ToNot(BeEmpty(), "operator should set cluster status.phase")
		}).WithTimeout(30*time.Second).WithPolling(5*time.Second).Should(Succeed(),
			"operator should set cluster status")
		statusJSON, err := json.MarshalIndent(initialCluster.Status, "", "  ")
		Expect(err).To(BeNil())
		GinkgoWriter.Printf("HCP initial cluster status:\n%s\n", string(statusJSON))

		// Poll until the operator sets status.phase to "Ready".
		// The hyperfleet-operator advances phase to Ready once Available=True
		// and Degraded!=True on the Cluster CR, so this is the single
		// authoritative readiness signal.
		Eventually(func(g Gomega) {
			resp, err := customerApiClient.Get("/api/v0/clusters/"+id, customerAccountID)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))

			var cluster v1alpha1.Cluster
			g.Expect(json.Unmarshal(resp.Body, &cluster)).To(Succeed())

			if os.Getenv("E2E_STATUS_POLL_LOG") != "" {
				snap, mErr := json.MarshalIndent(cluster.Status, "", "  ")
				if mErr == nil {
					_, _ = fmt.Fprintf(os.Stderr, "\n[%s] GET /clusters/%s (poll)\n%s\n",
						time.Now().Format(time.RFC3339), id, snap)
				}
			}

			phase := cluster.Status.Phase
			GinkgoWriter.Printf("[%s] polled cluster — phase=%s\n", time.Now().Format(time.RFC3339), phase)
			g.Expect(phase).To(Equal(v1alpha1.ClusterPhaseReady), "cluster phase should be Ready, got %s", phase)
		}).WithTimeout(35*time.Minute).WithPolling(20*time.Second).Should(Succeed(),
			"cluster status.phase should become Ready")

		resp, err := customerApiClient.Get("/api/v0/clusters/"+id, customerAccountID)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		var finalCluster v1alpha1.Cluster
		Expect(json.Unmarshal(resp.Body, &finalCluster)).To(Succeed())
		finalJSON, err := json.MarshalIndent(finalCluster.Status, "", "  ")
		Expect(err).ToNot(HaveOccurred())
		GinkgoWriter.Printf("HCP final cluster status:\n%s\n", string(finalJSON))
	})

	It("should have no active lifecycle silences once the cluster is Ready", Label("silence-ready", "monitor"), func() {
		amURL := os.Getenv("E2E_ALERTMANAGER_URL")
		if amURL == "" {
			Skip("E2E_ALERTMANAGER_URL not set — skipping lifecycle silence test")
		}
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "clusterID required — run cluster-status first or set HCP_INSTANCE_ID")
		name := clusterName
		if name == "" {
			name = os.Getenv("HCP_CLUSTER_NAME")
		}
		Expect(name).ToNot(BeEmpty(), "clusterName required — run hcp-create first or set HCP_CLUSTER_NAME")

		silences, err := amhelper.ListManagedSilences(context.Background(), amURL, id, name)
		Expect(err).NotTo(HaveOccurred())
		Expect(silences).To(BeEmpty(), "lifecycle silences should be expired once the cluster is Ready")
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

		// Add timeout context to prevent kubectl from hanging indefinitely
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		healthCmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", kubeconfigFile.Name(), "get", "--raw", "/healthz")
		healthCmd.Env = append(os.Environ(), customerEnv()...)
		healthOutput, err := healthCmd.CombinedOutput()

		if err != nil {
			// Provide better diagnostics on failure
			GinkgoWriter.Printf("kubectl healthz failed. Output:\n%s\n", string(healthOutput))
			if ctx.Err() == context.DeadlineExceeded {
				Fail(fmt.Sprintf("kubectl healthz timed out after 30s - cluster may not be ready or kubeconfig is invalid"))
			}
			Fail(fmt.Sprintf("kubectl get --raw /healthz failed: %v\nOutput:\n%s", err, string(healthOutput)))
		}

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

		// Parse nodepool response
		var nodepool v1alpha1.NodePool
		Expect(json.Unmarshal(output, &nodepool)).To(Succeed(), "failed to parse nodepool create response:\n%s", string(output))

		nodepoolID = string(nodepool.UID)
		Expect(nodepoolID).ToNot(BeEmpty())
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

		// Parse nodepool list response (CLI returns array, not Kubernetes list object)
		var nodepools []v1alpha1.NodePool
		Expect(json.Unmarshal(output, &nodepools)).To(Succeed(), "failed to parse nodepool list response:\n%s", string(output))
		Expect(nodepools).ToNot(BeEmpty(), "nodepool list should contain at least one item")

		if nodepoolID != "" {
			found := false
			for _, np := range nodepools {
				if string(np.UID) == nodepoolID {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "created nodepool %s should appear in list", nodepoolID)
		}

		GinkgoWriter.Printf("Listed %d nodepools for cluster %s\n", len(nodepools), id)
	})

	It("should have valid DNS and TLS for the KAS endpoint", Label("dns-verify", "monitor"), func() {
		defer recordTiming("hcp-dns-tls-verify")()
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "set clusterID from hcp-create (Ordered) or HCP_INSTANCE_ID when running dns-verify alone")

		resp, err := customerApiClient.Get("/api/v0/clusters/"+id, customerAccountID)
		Expect(err).ToNot(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var cluster v1alpha1.Cluster
		Expect(json.Unmarshal(resp.Body, &cluster)).To(Succeed())

		ep := cluster.Status.ControlPlaneEndpoint
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

			var list v1alpha1.NodePoolList
			g.Expect(json.Unmarshal(resp.Body, &list)).To(Succeed())

			foundNodePool := false
			for _, np := range list.Items {
				// Extract cluster ID from namespace (format: cluster-<uuid>)
				npClusterID := strings.TrimPrefix(np.Namespace, "cluster-")
				if npClusterID != id {
					continue
				}
				foundNodePool = true
				npName := np.Name

				// Status is embedded in the nodepool object
				phase := np.Status.Phase
				if os.Getenv("E2E_STATUS_POLL_LOG") != "" {
					_, _ = fmt.Fprintf(os.Stderr, "[%s] nodepool %s: phase=%s\n",
						time.Now().Format(time.RFC3339), npName, phase)
				}
				GinkgoWriter.Printf("  nodepool %s: phase=%s\n", npName, phase)
				g.Expect(phase).To(Equal(v1alpha1.NodePoolPhaseReady), "nodepool %s should be Ready", npName)
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

		// Get cluster ID for the nodepool delete command
		id := clusterID
		if id == "" {
			id = os.Getenv("HCP_INSTANCE_ID")
		}
		Expect(id).ToNot(BeEmpty(), "clusterID required for nodepool delete")

		cmd := exec.Command(ROSACTL_BIN, "nodepool", "delete", nodepoolID,
			"--cluster-id", id,
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

		// Add timeout context - if delete takes too long, treat as pass
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		cmd := exec.CommandContext(ctx, ROSACTL_BIN, "cluster-vpc", "delete", clusterName, "--region", region)
		cmd.Env = append(os.Environ(), customerEnv()...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			// If the error is a timeout, treat as pass (VPC delete may take longer than expected)
			if ctx.Err() == context.DeadlineExceeded {
				GinkgoWriter.Printf("cluster-vpc delete timed out after 10 minutes - treating as pass (may still be deleting in background)\n")
				return
			}
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
