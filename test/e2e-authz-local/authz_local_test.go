package authzlocal_test

import (
	"fmt"
	"net/http"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	awstest "github.com/openshift-online/rosa-hyperfleet-api/test/helpers/aws"
)

const (
	privilegedAccountID    = "000000000000"
	supervisorARN          = "arn:aws:iam::000000000000:user/supervisor"
	nonPrivilegedAccountID = "111111111111"
	adminARN               = "arn:aws:iam::111111111111:role/TestAdminRole"
	nonAdminARN            = "arn:aws:iam::111111111111:role/AppRole"
	defaultTimeout         = 30 * time.Second
)

var _ = Describe("Local Authz E2E", Ordered, func() {
	var client *awstest.APIClient

	BeforeAll(func() {
		baseURL := os.Getenv("E2E_BASE_URL")
		if baseURL == "" {
			baseURL = "http://localhost:8000"
		}
		client = awstest.NewAPIClient(baseURL)

		Eventually(func() error {
			return client.CheckReady()
		}, defaultTimeout, 1*time.Second).Should(Succeed(), "Service should be ready")
	})

	Context("Supervisor (Privileged Account) Access", Ordered, func() {
		BeforeAll(func() {
			client.CallerARN = supervisorARN
		})

		It("should access the health endpoints", func() {
			resp, err := client.Get("/api/v0/live", privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			resp, err = client.Get("/api/v0/ready", privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should list accounts", func() {
			resp, err := client.Get("/api/v0/accounts", privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should get the privileged account", func() {
			resp, err := client.Get(
				fmt.Sprintf("/api/v0/accounts/%s", privilegedAccountID),
				privilegedAccountID,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			data, err := resp.JSON()
			Expect(err).NotTo(HaveOccurred())
			Expect(data["accountId"]).To(Equal(privilegedAccountID))
			Expect(data["privileged"]).To(BeTrue())
		})

		It("should access authz admin endpoints", func() {
			resp, err := client.Get("/api/v0/authz/admins", privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should not have policy store (privileged accounts bypass Cedar)", func() {
			resp, err := client.Get("/api/v0/authz/policies", privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
			Expect(string(resp.Body)).To(ContainSubstring("internal-error"))
		})
	})

	Context("Account Provisioning", Ordered, func() {
		It("should provision a non-privileged account with adminArn", func() {
			body := map[string]interface{}{
				"accountId":  nonPrivilegedAccountID,
				"privileged": false,
				"adminArn":   adminARN,
			}

			client.CallerARN = supervisorARN
			resp, err := client.Post("/api/v0/accounts", body, privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))

			data, err := resp.JSON()
			Expect(err).NotTo(HaveOccurred())
			Expect(data["accountId"]).To(Equal(nonPrivilegedAccountID))
			Expect(data["privileged"]).To(BeFalse())
		})

		It("should have bootstrapped the admin from adminArn", func() {
			client.CallerARN = adminARN
			resp, err := client.Get("/api/v0/authz/admins", nonPrivilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(string(resp.Body)).To(ContainSubstring(adminARN))
		})

		It("should reject provisioning without adminArn for non-privileged accounts", func() {
			body := map[string]interface{}{
				"accountId":  "222222222222",
				"privileged": false,
			}
			client.CallerARN = supervisorARN
			resp, err := client.Post("/api/v0/accounts", body, privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusBadRequest))
			Expect(string(resp.Body)).To(ContainSubstring("adminArn"))
		})
	})

	Context("Admin Management", Ordered, func() {
		It("should allow admin to add another admin", func() {
			client.CallerARN = adminARN
			err := client.CreateAdmin(nonPrivilegedAccountID, "arn:aws:iam::111111111111:role/SecondAdmin")
			Expect(err).NotTo(HaveOccurred())
		})

		It("should list all admins for the account", func() {
			client.CallerARN = adminARN
			resp, err := client.Get("/api/v0/authz/admins", nonPrivilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			body := string(resp.Body)
			Expect(body).To(ContainSubstring(adminARN))
			Expect(body).To(ContainSubstring("SecondAdmin"))
		})

		It("should deny non-admin from managing admins", func() {
			client.CallerARN = nonAdminARN
			resp, err := client.Post("/api/v0/authz/admins", map[string]interface{}{
				"principalArn": "arn:aws:iam::111111111111:role/Unauthorized",
			}, nonPrivilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		})
	})

	Context("Cedar Policy CRUD", Ordered, func() {
		var policyID string

		It("should create a Cedar policy template", func() {
			client.CallerARN = adminARN
			var err error
			policyID, err = client.CreatePolicy(
				nonPrivilegedAccountID,
				"clusters-read-only",
				"Read-only access to clusters",
				`permit(principal == ?principal, action in [ROSA::Action::"ListClusters", ROSA::Action::"DescribeCluster"], resource);`,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(policyID).NotTo(BeEmpty())
			GinkgoWriter.Printf("Created policy: %s\n", policyID)
		})

		It("should get the policy by ID", func() {
			client.CallerARN = adminARN
			resp, err := client.Get(
				fmt.Sprintf("/api/v0/authz/policies/%s", policyID),
				nonPrivilegedAccountID,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))

			data, err := resp.JSON()
			Expect(err).NotTo(HaveOccurred())
			Expect(data["name"]).To(Equal("clusters-read-only"))
			Expect(data["policyId"]).To(Equal(policyID))
		})

		It("should list policies", func() {
			client.CallerARN = adminARN
			resp, err := client.Get("/api/v0/authz/policies", nonPrivilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(string(resp.Body)).To(ContainSubstring(policyID))
		})

		It("should deny non-admin from creating policies", func() {
			client.CallerARN = nonAdminARN
			resp, err := client.Post("/api/v0/authz/policies", map[string]interface{}{
				"name":   "unauthorized",
				"policy": `permit(principal == ?principal, action == ROSA::Action::"ListClusters", resource);`,
			}, nonPrivilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		})
	})

	Context("Group and Attachment Management", Ordered, func() {
		var (
			policyID     string
			groupID      string
			attachmentID string
		)

		BeforeAll(func() {
			client.CallerARN = adminARN

			var err error
			policyID, err = client.CreatePolicy(
				nonPrivilegedAccountID,
				"nodepool-management",
				"Full nodepool management",
				`permit(principal == ?principal, action in [ROSA::Action::"ListNodePools", ROSA::Action::"CreateNodePool", ROSA::Action::"DeleteNodePool", ROSA::Action::"DescribeNodePool"], resource);`,
			)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should create a group", func() {
			var err error
			groupID, err = client.CreateGroup(nonPrivilegedAccountID, "developers", "Developer team")
			Expect(err).NotTo(HaveOccurred())
			Expect(groupID).NotTo(BeEmpty())
			GinkgoWriter.Printf("Created group: %s\n", groupID)
		})

		It("should attach policy to group", func() {
			var err error
			attachmentID, err = client.CreateAttachment(nonPrivilegedAccountID, policyID, "group", groupID)
			Expect(err).NotTo(HaveOccurred())
			Expect(attachmentID).NotTo(BeEmpty())
			GinkgoWriter.Printf("Created attachment: %s\n", attachmentID)
		})

		It("should add a member to the group", func() {
			err := client.AddGroupMembers(nonPrivilegedAccountID, groupID, []string{nonAdminARN})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should list group members", func() {
			resp, err := client.Get(
				fmt.Sprintf("/api/v0/authz/groups/%s/members", groupID),
				nonPrivilegedAccountID,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(string(resp.Body)).To(ContainSubstring(nonAdminARN))
		})

		It("should clean up attachment", func() {
			err := client.DeleteAttachment(nonPrivilegedAccountID, attachmentID)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Cedar Authorization Evaluation", Ordered, func() {
		var (
			policyID string
			groupID  string
		)

		BeforeAll(func() {
			client.CallerARN = adminARN

			var err error
			policyID, err = client.CreatePolicy(
				nonPrivilegedAccountID,
				"cluster-list-only",
				"Allow listing clusters only",
				`permit(principal == ?principal, action == ROSA::Action::"ListClusters", resource);`,
			)
			Expect(err).NotTo(HaveOccurred())

			groupID, err = client.CreateGroup(nonPrivilegedAccountID, "authz-test-group", "Authz evaluation test group")
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CreateAttachment(nonPrivilegedAccountID, policyID, "group", groupID)
			Expect(err).NotTo(HaveOccurred())

			err = client.AddGroupMembers(nonPrivilegedAccountID, groupID, []string{nonAdminARN})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should ALLOW ListClusters for authorized principal", func() {
			decision, err := client.CheckAuthorization(nonPrivilegedAccountID, awstest.CheckAuthorizationRequest{
				Principal: nonAdminARN,
				Action:    "ListClusters",
				Resource:  "*",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(Equal("ALLOW"))
		})

		It("should DENY CreateCluster for principal with only list access", func() {
			decision, err := client.CheckAuthorization(nonPrivilegedAccountID, awstest.CheckAuthorizationRequest{
				Principal: nonAdminARN,
				Action:    "CreateCluster",
				Resource:  "*",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(Equal("DENY"))
		})

		It("should DENY all actions for unknown principal", func() {
			unknownARN := "arn:aws:iam::111111111111:role/UnknownRole"
			decision, err := client.CheckAuthorization(nonPrivilegedAccountID, awstest.CheckAuthorizationRequest{
				Principal: unknownARN,
				Action:    "ListClusters",
				Resource:  "*",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(Equal("DENY"))
		})
	})

	Context("ARN Normalization", Ordered, func() {
		It("should normalize STS assumed-role ARN for admin check", func() {
			stsARN := fmt.Sprintf("arn:aws:sts::%s:assumed-role/TestAdminRole/session-12345", nonPrivilegedAccountID)
			client.CallerARN = stsARN

			resp, err := client.Get("/api/v0/authz/admins", nonPrivilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK),
				"STS assumed-role ARN should be normalized to IAM role for admin check")
		})

		It("should normalize STS ARN for Cedar evaluation", func() {
			client.CallerARN = adminARN

			// First create a policy and group for the IAM form of an ARN
			policyID, err := client.CreatePolicy(
				nonPrivilegedAccountID,
				"arn-normalization-test",
				"Test ARN normalization in Cedar",
				`permit(principal == ?principal, action == ROSA::Action::"ListNodePools", resource);`,
			)
			Expect(err).NotTo(HaveOccurred())

			groupID, err := client.CreateGroup(nonPrivilegedAccountID, "arn-norm-group", "ARN normalization test")
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CreateAttachment(nonPrivilegedAccountID, policyID, "group", groupID)
			Expect(err).NotTo(HaveOccurred())

			iamARN := "arn:aws:iam::111111111111:role/NormTestRole"
			err = client.AddGroupMembers(nonPrivilegedAccountID, groupID, []string{iamARN})
			Expect(err).NotTo(HaveOccurred())

			// Now check authorization using the STS form — should be normalized to IAM
			stsARN := "arn:aws:sts::111111111111:assumed-role/NormTestRole/session-abc"
			decision, err := client.CheckAuthorization(nonPrivilegedAccountID, awstest.CheckAuthorizationRequest{
				Principal: stsARN,
				Action:    "ListNodePools",
				Resource:  "*",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(Equal("ALLOW"),
				"STS assumed-role ARN should be normalized to IAM role for Cedar evaluation")
		})
	})

	Context("Admin Removal and Cedar Transition", Ordered, func() {
		// Tests the flow: bootstrap admin sets up Cedar policies, then is removed.
		// A second admin should still manage admins. A Cedar-authorized non-admin
		// should still access resources but NOT manage admins (RequireAdmin is separate from Cedar).
		const (
			secondAdminARN  = "arn:aws:iam::111111111111:role/SecondAdmin"
			cedarRoleARN    = "arn:aws:iam::111111111111:role/CedarOnlyRole"
		)

		var groupID string

		BeforeAll(func() {
			client.CallerARN = adminARN

			// Create a Cedar policy granting cluster access to cedarRoleARN
			policyID, err := client.CreatePolicy(
				nonPrivilegedAccountID,
				"cedar-transition-test",
				"Test Cedar access after admin removal",
				`permit(principal == ?principal, action in [ROSA::Action::"ListClusters", ROSA::Action::"DescribeCluster"], resource);`,
			)
			Expect(err).NotTo(HaveOccurred())

			groupID, err = client.CreateGroup(nonPrivilegedAccountID, "cedar-transition-group", "Cedar transition test")
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CreateAttachment(nonPrivilegedAccountID, policyID, "group", groupID)
			Expect(err).NotTo(HaveOccurred())

			err = client.AddGroupMembers(nonPrivilegedAccountID, groupID, []string{cedarRoleARN})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should verify Cedar-authorized role has resource access before admin removal", func() {
			decision, err := client.CheckAuthorization(nonPrivilegedAccountID, awstest.CheckAuthorizationRequest{
				Principal: cedarRoleARN,
				Action:    "ListClusters",
				Resource:  "*",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(Equal("ALLOW"))
		})

		It("should remove the bootstrap admin", func() {
			client.CallerARN = adminARN
			resp, err := client.Delete(
				fmt.Sprintf("/api/v0/authz/admins/%s", adminARN),
				nonPrivilegedAccountID,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(SatisfyAny(Equal(http.StatusOK), Equal(http.StatusNoContent)))
		})

		It("should deny removed admin from managing admins", func() {
			client.CallerARN = adminARN
			resp, err := client.Post("/api/v0/authz/admins", map[string]interface{}{
				"principalArn": "arn:aws:iam::111111111111:role/ShouldFail",
			}, nonPrivilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden))
		})

		It("should still allow second admin to manage admins", func() {
			client.CallerARN = secondAdminARN
			resp, err := client.Get("/api/v0/authz/admins", nonPrivilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})

		It("should still allow Cedar-authorized role to access resources after admin removal", func() {
			decision, err := client.CheckAuthorization(nonPrivilegedAccountID, awstest.CheckAuthorizationRequest{
				Principal: cedarRoleARN,
				Action:    "ListClusters",
				Resource:  "*",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(Equal("ALLOW"))
		})

		It("should deny Cedar-authorized role from managing admins (RequireAdmin is separate from Cedar)", func() {
			client.CallerARN = cedarRoleARN
			resp, err := client.Post("/api/v0/authz/admins", map[string]interface{}{
				"principalArn": "arn:aws:iam::111111111111:role/ShouldAlsoFail",
			}, nonPrivilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden),
				"Cedar policies cannot grant admin management — RequireAdmin checks the admins table, not Cedar")
		})
	})

	Context("Account Re-provisioning", Ordered, func() {
		// Delete a customer account completely, re-create it with a new adminArn,
		// and verify the full flow works again from scratch.
		const (
			reproAccountID = "333333333333"
			reproAdminARN  = "arn:aws:iam::333333333333:role/OriginalAdmin"
			reproNewAdmin  = "arn:aws:iam::333333333333:role/FreshAdmin"
			reproAppRole   = "arn:aws:iam::333333333333:role/AppRole"
		)

		It("should provision the account the first time", func() {
			client.CallerARN = supervisorARN
			resp, err := client.Post("/api/v0/accounts", map[string]interface{}{
				"accountId":  reproAccountID,
				"privileged": false,
				"adminArn":   reproAdminARN,
			}, privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))
		})

		It("should allow the bootstrapped admin to create a policy", func() {
			client.CallerARN = reproAdminARN
			_, err := client.CreatePolicy(
				reproAccountID,
				"repro-cluster-access",
				"Cluster access for re-provisioning test",
				`permit(principal == ?principal, action == ROSA::Action::"ListClusters", resource);`,
			)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should delete the account completely", func() {
			client.CallerARN = supervisorARN
			resp, err := client.Delete(
				fmt.Sprintf("/api/v0/accounts/%s", reproAccountID),
				privilegedAccountID,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(SatisfyAny(Equal(http.StatusOK), Equal(http.StatusNoContent)))
		})

		It("should confirm the account no longer exists", func() {
			client.CallerARN = supervisorARN
			resp, err := client.Get(
				fmt.Sprintf("/api/v0/accounts/%s", reproAccountID),
				privilegedAccountID,
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
		})

		It("should re-provision the account with a new adminArn", func() {
			client.CallerARN = supervisorARN
			resp, err := client.Post("/api/v0/accounts", map[string]interface{}{
				"accountId":  reproAccountID,
				"privileged": false,
				"adminArn":   reproNewAdmin,
			}, privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusCreated))
		})

		It("should deny the old admin after re-provisioning", func() {
			client.CallerARN = reproAdminARN
			resp, err := client.Get("/api/v0/authz/admins", reproAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusForbidden),
				"old admin from previous provisioning should not have access")
		})

		It("should allow the new admin to manage the account", func() {
			client.CallerARN = reproNewAdmin
			resp, err := client.Get("/api/v0/authz/admins", reproAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			Expect(string(resp.Body)).To(ContainSubstring(reproNewAdmin))
		})

		It("should allow the new admin to create policies", func() {
			client.CallerARN = reproNewAdmin
			_, err := client.CreatePolicy(
				reproAccountID,
				"fresh-cluster-access",
				"Fresh cluster access after re-provisioning",
				`permit(principal == ?principal, action == ROSA::Action::"ListClusters", resource);`,
			)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow the new admin to create groups and attach policies", func() {
			client.CallerARN = reproNewAdmin

			policyID, err := client.CreatePolicy(
				reproAccountID,
				"fresh-nodepool-access",
				"NodePool access after re-provisioning",
				`permit(principal == ?principal, action == ROSA::Action::"ListNodePools", resource);`,
			)
			Expect(err).NotTo(HaveOccurred())

			groupID, err := client.CreateGroup(reproAccountID, "fresh-group", "Re-provisioned group")
			Expect(err).NotTo(HaveOccurred())

			_, err = client.CreateAttachment(reproAccountID, policyID, "group", groupID)
			Expect(err).NotTo(HaveOccurred())

			err = client.AddGroupMembers(reproAccountID, groupID, []string{reproAppRole})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should enforce Cedar policies on the re-provisioned account", func() {
			decision, err := client.CheckAuthorization(reproAccountID, awstest.CheckAuthorizationRequest{
				Principal: reproAppRole,
				Action:    "ListNodePools",
				Resource:  "*",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(Equal("ALLOW"))

			decision, err = client.CheckAuthorization(reproAccountID, awstest.CheckAuthorizationRequest{
				Principal: reproAppRole,
				Action:    "DeleteCluster",
				Resource:  "*",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(Equal("DENY"))
		})
	})

	Context("Privileged Account Bypass", func() {
		It("should bypass Cedar for privileged account", func() {
			client.CallerARN = fmt.Sprintf("arn:aws:iam::%s:user/privileged-user", privilegedAccountID)

			resp, err := client.Get("/api/v0/authz/admins", privilegedAccountID)
			Expect(err).NotTo(HaveOccurred())
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
		})
	})
})
