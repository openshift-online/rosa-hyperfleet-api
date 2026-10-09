/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// SDK E2E Tests - HCP Cluster and NodePool lifecycle via the Go clientset
//
// Required environment variables:
//
//	E2E_BASE_URL              — platform API base URL
//	ROSACTL_BIN               — path to the rosactl binary
//	CUSTOMER_AWS_PROFILE      — AWS profile for customer-account operations
//
// Optional:
//
//	AWS_REGION                — defaults to us-east-1
//	E2E_ACCOUNT_ID            — RC account ID (derived from STS if absent)
//	E2E_CUSTOMER_ACCOUNT_ID   — customer account ID (derived from STS if absent)
//	HCP_CLUSTER_NAME          — fixed cluster name (generated if absent)
//	OCP_IMAGE               — release image passed to release.image; defaults to defaultReleaseImage
//	HYPERFLEET_INSTANCE_TYPE  — node instance type (defaults to m5.xlarge)
//	E2E_SKIP_CLEANUP          — set to skip DeferCleanup safety-net teardown
package e2e_sdk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	hyperfleet "github.com/openshift-online/rosa-hyperfleet-api/clientset"
	"github.com/openshift-online/rosa-hyperfleet-api/clientset/platform"
	hfrest "github.com/openshift-online/rosa-hyperfleet-api/clientset/rest"
	awstest "github.com/openshift-online/rosa-hyperfleet-api/test/helpers/aws"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	clusterReadyTimeout  = 35 * time.Minute
	clusterReadyPoll     = 30 * time.Second
	clusterDeleteTimeout = 20 * time.Minute
	clusterDeletePoll    = 30 * time.Second

	dnsReservationReadyTimeout  = 5 * time.Minute
	dnsReservationReadyPoll     = 5 * time.Second
	dnsReservationDeleteTimeout = 5 * time.Minute
	dnsReservationDeletePoll    = 5 * time.Second

	oidcConfigReadyTimeout  = 35 * time.Minute
	oidcConfigReadyPoll     = 30 * time.Second
	oidcConfigDeleteTimeout = 5 * time.Minute
	oidcConfigDeletePoll    = 5 * time.Second

	nodepoolReadyTimeout  = 30 * time.Minute
	nodepoolReadyPoll     = 30 * time.Second
	nodepoolDeleteTimeout = 20 * time.Minute
	nodepoolDeletePoll    = 30 * time.Second

	defaultRegion       = "us-east-1"
	defaultInstanceType = "m5.xlarge"
	defaultReleaseImage = "quay.io/openshift-release-dev/ocp-release:5.0.0-rc.5-multi"
)

// iamStackOutputs holds the IAM role ARNs and instance profile read from the
// rosa-<cluster>-iam CloudFormation stack.
type iamStackOutputs struct {
	Roles           hypershiftv1beta1.AWSRolesRef
	InstanceProfile string
}

var _ = Describe("SDK E2E: cluster and nodepool lifecycle", Ordered, func() {
	var (
		ctx                context.Context
		baseURL            string
		rosactlBin         string
		customerProfile    string
		rcProfile          string
		region             string
		accountID          string
		customerAccountID  string
		version            string
		instanceType       string
		clusterName        string
		clusterUID         string
		dnsReservationName string
		dnsReservationUID  string
		oidcConfigName     string
		oidcConfigUID      string
		oidcIssuerURL      string
		nodepoolUID        string
		nodepoolName       string

		// Populated after stack creation from CloudFormation outputs.
		vpcID    string
		subnetID string
		iamOut   iamStackOutputs

		awsCfg    aws.Config
		cs        *hyperfleet.Clientset
		apiClient *awstest.APIClient

		vpcCreated            bool
		iamCreated            bool
		dnsReservationCreated bool
		oidcConfigCreated     bool
		oidcProviderCreated   bool
		clusterCreated        bool
		nodepoolCreated       bool
		extraNodepoolUID      string
		extraNodepoolName     string
		extraNodepoolCreated  bool
	)

	BeforeAll(func() {
		ctx = context.Background()

		baseURL = os.Getenv("E2E_BASE_URL")
		if baseURL == "" {
			Skip("E2E_BASE_URL is not set")
		}
		rosactlBin = os.Getenv("ROSACTL_BIN")
		if rosactlBin == "" {
			Skip("ROSACTL_BIN is not set")
		}
		customerProfile = os.Getenv("CUSTOMER_AWS_PROFILE")
		if customerProfile == "" {
			Skip("CUSTOMER_AWS_PROFILE is not set")
		}

		rcProfile = os.Getenv("RC_AWS_PROFILE")
		if rcProfile == "" {
			Skip("RC_AWS_PROFILE is not set")
		}

		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = defaultRegion
			GinkgoWriter.Printf("No AWS_REGION set, defaulting to %s\n", region)
		}

		version = os.Getenv("OCP_IMAGE")
		if version == "" {
			version = defaultReleaseImage
		}

		instanceType = os.Getenv("HYPERFLEET_INSTANCE_TYPE")
		if instanceType == "" {
			instanceType = defaultInstanceType
		}

		accountID = os.Getenv("E2E_ACCOUNT_ID")
		if accountID == "" {
			cmd := exec.Command("aws", "sts", "get-caller-identity", "--query",
				"Account", "--output", "text", "--region", region)
			cmd.Env = append(os.Environ(), "AWS_PROFILE="+rcProfile)
			out, err := cmd.CombinedOutput()
			Expect(err).ToNot(HaveOccurred(), "getting RC account ID: %s", string(out))
			accountID = strings.TrimSpace(string(out))
		}
		GinkgoWriter.Printf("RC account ID: %s\n", accountID)

		customerAccountID = os.Getenv("E2E_CUSTOMER_ACCOUNT_ID")
		if customerAccountID == "" {
			cmd := exec.Command("aws", "sts", "get-caller-identity", "--query",
				"Account", "--output", "text", "--region", region)
			cmd.Env = append(os.Environ(), "AWS_PROFILE="+customerProfile)
			out, err := cmd.CombinedOutput()
			Expect(err).ToNot(HaveOccurred(), "getting customer account ID: %s", string(out))
			customerAccountID = strings.TrimSpace(string(out))
		}
		GinkgoWriter.Printf("Customer account ID: %s\n", customerAccountID)

		if os.Getenv("HCP_CLUSTER_NAME") != "" {
			clusterName = os.Getenv("HCP_CLUSTER_NAME")
		} else {
			clusterName = fmt.Sprintf("sdk-e2e-%d", time.Now().Unix())
		}
		GinkgoWriter.Printf("Cluster name: %s\n", clusterName)

		var err error
		awsCfg, err = awsconfig.LoadDefaultConfig(ctx,
			awsconfig.WithSharedConfigProfile(customerProfile),
			awsconfig.WithRegion(region),
		)
		Expect(err).ToNot(HaveOccurred(), "loading customer AWS config")

		identity, err := sts.NewFromConfig(awsCfg).GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		Expect(err).ToNot(HaveOccurred(), "getting customer caller identity")

		cs, err = hyperfleet.NewForConfig(&hfrest.Config{
			Host:      baseURL,
			AccountID: *identity.Account,
			CallerARN: *identity.Arn,
			AWSConfig: awsCfg,
		})
		Expect(err).ToNot(HaveOccurred(), "building SDK clientset")

		apiClient = awstest.NewAPIClient(baseURL)
		apiClient.AWSProfile = rcProfile
		By("registering customer account")
		body := map[string]interface{}{
			"accountId":  customerAccountID,
			"privileged": true,
		}
		resp, err := apiClient.Post("/api/v0/accounts", body, accountID)
		Expect(err).ToNot(HaveOccurred())
		switch resp.StatusCode {
		case http.StatusCreated:
			GinkgoWriter.Printf("Customer account %s registered\n", customerAccountID)
		case http.StatusConflict:
			var body map[string]interface{}
			Expect(json.Unmarshal(resp.Body, &body)).To(Succeed())
			msg, _ := body["message"].(string)
			Expect(msg).To(ContainSubstring("ACCOUNTS-MGMT-CREATE-004"),
				"unexpected 409 body: %s", string(resp.Body))
			GinkgoWriter.Printf("Customer account %s already registered\n", customerAccountID)
		default:
			Fail(fmt.Sprintf("account registration: status %d body: %s", resp.StatusCode, string(resp.Body)))
		}

		// Safety-net: runs after the Ordered container finishes using the same
		// delete helpers as the It block, so teardown behaviour is identical
		// whether the test passed or failed mid-way.
		DeferCleanup(func() {
			if os.Getenv("E2E_SKIP_CLEANUP") != "" {
				GinkgoWriter.Printf("DeferCleanup: E2E_SKIP_CLEANUP set, skipping\n")
				return
			}
			GinkgoWriter.Printf("DeferCleanup: cleaning up remaining resources\n")
			cleanupCtx := context.Background()
			customerEnv := append(os.Environ(), "AWS_PROFILE="+customerProfile)

			if extraNodepoolCreated && extraNodepoolName != "" {
				GinkgoWriter.Printf("DeferCleanup: deleting extra nodepool %s\n", extraNodepoolName)
				if err := deleteNodepool(cleanupCtx, cs, extraNodepoolName); err != nil {
					GinkgoWriter.Printf("DeferCleanup WARNING: %v\n", err)
				} else {
					extraNodepoolCreated = false
				}
			}
			if nodepoolCreated && nodepoolName != "" {
				GinkgoWriter.Printf("DeferCleanup: initiating nodepool %s deletion\n", nodepoolName)
				if err := cs.HyperfleetV1alpha1().NodePools().Delete(cleanupCtx, nodepoolName, platform.DeleteOptions{}); err != nil {
					GinkgoWriter.Printf("DeferCleanup WARNING: nodepool delete: %v\n", err)
				} else {
					nodepoolCreated = false
				}
			}
			if clusterCreated && clusterName != "" {
				GinkgoWriter.Printf("DeferCleanup: deleting cluster %s (uid=%s)\n", clusterName, clusterUID)
				if err := deleteCluster(cleanupCtx, cs, clusterName); err != nil {
					GinkgoWriter.Printf("DeferCleanup WARNING: %v\n", err)
				} else {
					clusterCreated = false
					dnsReservationCreated = false // Cluster finalization deletes its claimed reservation.
				}
			}
			if dnsReservationCreated && dnsReservationName != "" {
				GinkgoWriter.Printf("DeferCleanup: deleting DNSReservation %s\n", dnsReservationName)
				if err := deleteDNSReservation(cleanupCtx, cs, dnsReservationName); err != nil {
					GinkgoWriter.Printf("DeferCleanup WARNING: DNSReservation delete: %v\n", err)
				} else {
					dnsReservationCreated = false
				}
			}
			if oidcConfigCreated && oidcConfigName != "" {
				GinkgoWriter.Printf("DeferCleanup: deleting OidcConfig %s\n", oidcConfigName)
				if err := deleteOidcConfig(cleanupCtx, cs, oidcConfigName); err != nil {
					GinkgoWriter.Printf("DeferCleanup WARNING: OidcConfig delete: %v\n", err)
				} else {
					oidcConfigCreated = false
				}
			}
			for _, sub := range infraStacksToDelete(oidcProviderCreated, vpcCreated, iamCreated) {
				GinkgoWriter.Printf("DeferCleanup: %s delete %s\n", sub, clusterName)
				cmd := exec.Command(rosactlBin, sub, "delete", clusterName, "--region", region)
				cmd.Env = customerEnv
				cmd.Stdout = GinkgoWriter
				cmd.Stderr = GinkgoWriter
				if err := cmd.Run(); err != nil {
					GinkgoWriter.Printf("DeferCleanup WARNING: %s delete: %v\n", sub, err)
				}
			}
			GinkgoWriter.Printf("DeferCleanup complete\n")
		})
	})

	It("manages the full cluster and nodepool lifecycle", func() {
		customerEnv := append(os.Environ(), "AWS_PROFILE="+customerProfile)

		By("creating VPC")
		cmd := exec.Command(rosactlBin, "cluster-vpc", "create", clusterName,
			"--region", region, "--availability-zones", region+"a")
		cmd.Env = customerEnv
		cmd.Stdout = GinkgoWriter
		cmd.Stderr = GinkgoWriter
		Expect(cmd.Run()).To(Succeed(), "rosactl cluster-vpc create")
		vpcCreated = true

		By("reading VPC outputs from CloudFormation")
		var err error
		vpcID, subnetID, err = vpcOutputsFromStack(clusterName, region, customerEnv)
		Expect(err).ToNot(HaveOccurred(), "reading VPC CloudFormation stack outputs")
		GinkgoWriter.Printf("VPC ID: %s  Subnet ID: %s\n", vpcID, subnetID)

		By("creating IAM roles")
		cmd = exec.Command(rosactlBin, "cluster-iam", "create", clusterName, "--region", region)
		cmd.Env = customerEnv
		cmd.Stdout = GinkgoWriter
		cmd.Stderr = GinkgoWriter
		Expect(cmd.Run()).To(Succeed(), "rosactl cluster-iam create")
		iamCreated = true

		By("reading IAM outputs from CloudFormation")
		iamOut, err = iamOutputsFromStack(clusterName, region, customerEnv)
		Expect(err).ToNot(HaveOccurred(), "reading IAM CloudFormation stack outputs")
		GinkgoWriter.Printf("InstanceProfile: %s\n", iamOut.InstanceProfile)

		By("creating a DNS reservation via SDK")
		dnsReservationName = clusterName + "-dns"
		dnsReservations := cs.HyperfleetV1alpha1().DNSReservations()
		reservation, err := dnsReservations.Create(ctx, &v1alpha1.DNSReservation{
			ObjectMeta: metav1.ObjectMeta{Name: dnsReservationName},
		}, platform.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "SDK DNSReservation create")
		dnsReservationCreated = true
		dnsReservationUID = string(reservation.UID)
		Expect(dnsReservationUID).ToNot(BeEmpty(), "created DNSReservation should have a UID")
		GinkgoWriter.Printf("DNSReservation %s created (uid=%s)\n", dnsReservationName, dnsReservationUID)

		By("waiting for DNS reservation allocation")
		Expect(dnsReservations.WaitUntil(ctx, dnsReservationName,
			func(r *v1alpha1.DNSReservation) bool {
				if r == nil {
					return false
				}
				GinkgoWriter.Printf("[%s] DNSReservation %s: phase=%s baseDomain=%s\n",
					time.Now().Format(time.RFC3339), dnsReservationName, r.Status.Phase, r.Status.BaseDomain)
				return r.Status.Phase == v1alpha1.DNSReservationPhaseReady && r.Status.BaseDomain != ""
			},
			dnsReservationReadyPoll, dnsReservationReadyTimeout,
		)).To(Succeed(), "DNSReservation should reach Ready phase")

		By("creating a managed OIDC config via SDK")
		oidcConfigName = clusterName + "-oidc"
		oidcConfigs := cs.HyperfleetV1alpha1().OidcConfigs()
		managedOidcConfig, err := oidcConfigs.Create(ctx, &v1alpha1.OidcConfig{
			ObjectMeta: metav1.ObjectMeta{Name: oidcConfigName},
			Spec:       v1alpha1.OidcConfigSpec{Type: "managed"},
		}, platform.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "SDK OidcConfig create")
		oidcConfigCreated = true
		oidcConfigUID = string(managedOidcConfig.UID)
		oidcIssuerURL = managedOidcConfig.Spec.IssuerUrl
		Expect(oidcConfigUID).ToNot(BeEmpty(), "created OidcConfig should have a UID")
		Expect(oidcIssuerURL).ToNot(BeEmpty(), "managed OidcConfig should return its generated issuer URL")
		GinkgoWriter.Printf("OidcConfig %s created (uid=%s issuer=%s)\n", oidcConfigName, oidcConfigUID, oidcIssuerURL)

		By("creating the customer IAM OIDC provider for the managed issuer")
		cmd = exec.Command(rosactlBin, "cluster-oidc", "create", clusterName,
			"--region", region, "--oidc-issuer-url", oidcIssuerURL)
		cmd.Env = customerEnv
		cmd.Stdout = GinkgoWriter
		cmd.Stderr = GinkgoWriter
		Expect(cmd.Run()).To(Succeed(), "rosactl cluster-oidc create")
		oidcProviderCreated = true

		By("verifying IAM roles trust the OIDC provider")
		oidcOut, err := cfStackOutputs("rosa-"+clusterName+"-oidc", region, customerEnv)
		Expect(err).ToNot(HaveOccurred(), "reading OIDC CloudFormation stack outputs")
		var oidcProviderArn string
		for _, o := range oidcOut {
			if o.OutputKey == "OIDCProviderArn" {
				oidcProviderArn = o.OutputValue
				break
			}
		}
		Expect(oidcProviderArn).ToNot(BeEmpty(), "OIDCProviderArn not found in rosa-%s-oidc outputs", clusterName)
		Expect(verifyRolesTrustOIDCProvider(iamOut.Roles, oidcProviderArn, region, customerEnv)).To(Succeed())
		GinkgoWriter.Printf("All IAM roles trust OIDC provider %s\n", oidcProviderArn)

		By("creating cluster via SDK")
		subnetRef := subnetID
		subnetOut, err := ec2.NewFromConfig(awsCfg).DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
			SubnetIds: []string{subnetID},
		})
		Expect(err).ToNot(HaveOccurred(), "describing subnet %s", subnetID)
		Expect(subnetOut.Subnets).ToNot(BeEmpty(), "subnet %s not found", subnetID)
		zone := *subnetOut.Subnets[0].AvailabilityZone

		cluster, err := cs.HyperfleetV1alpha1().Clusters().Create(ctx, &v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName},
			Spec: v1alpha1.ClusterSpec{
				DNSReservationID: dnsReservationUID,
				OidcConfigID:     oidcConfigUID,
				HostedCluster: v1alpha1.HostedClusterSpecPassthrough{
					Release: hypershiftv1beta1.Release{Image: version},
					Platform: v1alpha1.PlatformSpec{
						Type: hypershiftv1beta1.AWSPlatform,
						AWS: &hypershiftv1beta1.AWSPlatformSpec{
							Region:   region,
							RolesRef: iamOut.Roles,
							CloudProviderConfig: &hypershiftv1beta1.AWSCloudProviderConfig{
								VPC:    vpcID,
								Zone:   zone,
								Subnet: &hypershiftv1beta1.AWSResourceReference{ID: &subnetRef},
							},
						},
					},
				},
			},
		}, platform.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "SDK cluster create")
		clusterUID = string(cluster.UID)
		clusterCreated = true
		Expect(clusterUID).ToNot(BeEmpty(), "created cluster should have a UID")
		GinkgoWriter.Printf("Cluster %s created (uid=%s)\n", clusterName, clusterUID)

		By("waiting for cluster Ready")
		clusters := cs.HyperfleetV1alpha1().Clusters()
		Expect(clusters.WaitUntil(ctx, clusterName,
			func(c *v1alpha1.Cluster) bool {
				if c == nil {
					return false
				}
				GinkgoWriter.Printf("[%s] cluster %s: phase=%s\n",
					time.Now().Format(time.RFC3339), clusterName, c.Status.Phase)
				return c.Status.Phase == v1alpha1.ClusterPhaseReady
			},
			oidcConfigReadyPoll, oidcConfigReadyTimeout,
		)).To(Succeed(), "cluster should reach Ready phase")
		GinkgoWriter.Printf("Cluster %s is Ready\n", clusterName)

		By("waiting for the managed OIDC config to become Ready")
		Expect(oidcConfigs.WaitUntil(ctx, oidcConfigName,
			func(config *v1alpha1.OidcConfig) bool {
				if config == nil {
					return false
				}
				GinkgoWriter.Printf("[%s] OidcConfig %s: phase=%s\n",
					time.Now().Format(time.RFC3339), oidcConfigName, config.Status.Phase)
				return config.Status.Phase == v1alpha1.OidcConfigPhaseReady
			},
			clusterReadyPoll, clusterReadyTimeout,
		)).To(Succeed(), "OidcConfig should reach Ready phase")

		By("creating nodepools via SDK")
		nodepools := cs.HyperfleetV1alpha1().NodePools()

		nodepoolName = clusterName + ".e2e-np"
		initialReplicas := int32(2)
		npSubnetRef := subnetID
		np, err := nodepools.Create(ctx, &v1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: nodepoolName},
			Spec: v1alpha1.NodePoolSpec{
				NodePool: v1alpha1.NodePoolSpecPassthrough{
					ClusterName: clusterName,
					Replicas:    &initialReplicas,
					Platform: v1alpha1.NodePoolPlatform{
						Type: hypershiftv1beta1.AWSPlatform,
						AWS: &hypershiftv1beta1.AWSNodePoolPlatform{
							InstanceType:    instanceType,
							InstanceProfile: iamOut.InstanceProfile,
							Subnet:          hypershiftv1beta1.AWSResourceReference{ID: &npSubnetRef},
						},
					},
					Release: hypershiftv1beta1.Release{Image: version},
				},
			},
		}, platform.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "SDK nodepool create")
		nodepoolUID = string(np.UID)
		nodepoolCreated = true
		Expect(nodepoolUID).ToNot(BeEmpty(), "created nodepool should have a UID")
		Expect(np.Namespace).To(Equal("account-"+customerAccountID),
			"nodepool.metadata.namespace should be the authenticated customer's account namespace")
		GinkgoWriter.Printf("NodePool %s created (uid=%s)\n", nodepoolName, nodepoolUID)

		extraNodepoolName = clusterName + ".e2e-np-extra"
		extraReplicas := int32(1)
		extraNpSubnetRef := subnetID
		extraNp, err := nodepools.Create(ctx, &v1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: extraNodepoolName},
			Spec: v1alpha1.NodePoolSpec{
				NodePool: v1alpha1.NodePoolSpecPassthrough{
					ClusterName: clusterName,
					Replicas:    &extraReplicas,
					Platform: v1alpha1.NodePoolPlatform{
						Type: hypershiftv1beta1.AWSPlatform,
						AWS: &hypershiftv1beta1.AWSNodePoolPlatform{
							InstanceType:    instanceType,
							InstanceProfile: iamOut.InstanceProfile,
							Subnet:          hypershiftv1beta1.AWSResourceReference{ID: &extraNpSubnetRef},
						},
					},
					Release: hypershiftv1beta1.Release{Image: version},
				},
			},
		}, platform.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "SDK extra nodepool create")
		extraNodepoolUID = string(extraNp.UID)
		extraNodepoolCreated = true
		Expect(extraNodepoolUID).ToNot(BeEmpty(), "created extra nodepool should have a UID")
		GinkgoWriter.Printf("Extra NodePool %s created (uid=%s)\n", extraNodepoolName, extraNodepoolUID)

		By("waiting for nodepools Ready")
		Expect(nodepools.WaitUntil(ctx, nodepoolName,
			func(n *v1alpha1.NodePool) bool {
				if n == nil {
					return false
				}
				GinkgoWriter.Printf("[%s] nodepool %s: phase=%s\n",
					time.Now().Format(time.RFC3339), nodepoolName, n.Status.Phase)
				return n.Status.Phase == v1alpha1.NodePoolPhaseReady
			},
			nodepoolReadyPoll, nodepoolReadyTimeout,
		)).To(Succeed(), "nodepool should reach Ready phase")
		GinkgoWriter.Printf("NodePool %s is Ready\n", nodepoolName)

		Expect(nodepools.WaitUntil(ctx, extraNodepoolName,
			func(n *v1alpha1.NodePool) bool {
				if n == nil {
					return false
				}
				GinkgoWriter.Printf("[%s] extra nodepool %s: phase=%s\n",
					time.Now().Format(time.RFC3339), extraNodepoolName, n.Status.Phase)
				return n.Status.Phase == v1alpha1.NodePoolPhaseReady
			},
			nodepoolReadyPoll, nodepoolReadyTimeout,
		)).To(Succeed(), "extra nodepool should reach Ready phase")
		GinkgoWriter.Printf("Extra NodePool %s is Ready\n", extraNodepoolName)

		By("patching nodepool replicas")
		current, err := nodepools.Get(ctx, nodepoolName, platform.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "getting nodepool for patch")
		newReplicas := int32(3)
		current.Spec.NodePool.Replicas = &newReplicas
		updated, err := nodepools.Update(ctx, current, platform.UpdateOptions{})
		Expect(err).ToNot(HaveOccurred(), "updating nodepool replicas")
		Expect(*updated.Spec.NodePool.Replicas).To(Equal(newReplicas))
		GinkgoWriter.Printf("NodePool replicas updated to %d\n", newReplicas)

		By("deleting extra nodepool")
		Expect(deleteNodepool(ctx, cs, extraNodepoolName)).To(Succeed())
		extraNodepoolCreated = false

		By("initiating nodepool deletion")
		Expect(nodepools.Delete(ctx, nodepoolName, platform.DeleteOptions{})).To(Succeed())
		nodepoolCreated = false
		GinkgoWriter.Printf("NodePool %s deletion initiated\n", nodepoolName)

		By("deleting cluster")
		Expect(deleteCluster(ctx, cs, clusterName)).To(Succeed())
		clusterCreated = false
		dnsReservationCreated = false // Cluster finalization deletes its claimed reservation.

		By("deleting the OidcConfig through the API")
		Expect(deleteOidcConfig(ctx, cs, oidcConfigName)).To(Succeed())
		oidcConfigCreated = false

		By("tearing down the customer OIDC provider, VPC, and IAM")
		for _, sub := range infraStacksToDelete(oidcProviderCreated, vpcCreated, iamCreated) {
			cmd = exec.Command(rosactlBin, sub, "delete", clusterName, "--region", region)
			cmd.Env = customerEnv
			cmd.Stdout = GinkgoWriter
			cmd.Stderr = GinkgoWriter
			Expect(cmd.Run()).To(Succeed(), "rosactl %s delete %s", sub, clusterName)
		}
		vpcCreated = false
		iamCreated = false
		oidcProviderCreated = false
	})
})

// vpcOutputsFromStack queries the rosa-<cluster>-vpc CloudFormation stack
// and returns the VPC ID and private subnet ID from its outputs.
func vpcOutputsFromStack(clusterName, region string, env []string) (vpcID, subnetID string, err error) {
	out, err := cfStackOutputs("rosa-"+clusterName+"-vpc", region, env)
	if err != nil {
		return "", "", err
	}
	for _, o := range out {
		switch o.OutputKey {
		case "VpcId":
			vpcID = o.OutputValue
		case "PrivateSubnetIds":
			// Comma-separated list; the cluster spec takes a single subnet.
			subnetID = strings.TrimSpace(strings.SplitN(o.OutputValue, ",", 2)[0])
		}
	}
	if vpcID == "" || subnetID == "" {
		return "", "", fmt.Errorf("VpcId or PrivateSubnetIds not found in rosa-%s-vpc outputs", clusterName)
	}
	return vpcID, subnetID, nil
}

// iamOutputsFromStack queries the rosa-<cluster>-iam CloudFormation stack
// and returns the HyperShift IAM role ARNs and worker instance profile.
func iamOutputsFromStack(clusterName, region string, env []string) (iamStackOutputs, error) {
	out, err := cfStackOutputs("rosa-"+clusterName+"-iam", region, env)
	if err != nil {
		return iamStackOutputs{}, err
	}

	var o iamStackOutputs
	for _, item := range out {
		switch item.OutputKey {
		case "IngressRoleArn":
			o.Roles.IngressARN = item.OutputValue
		case "ImageRegistryRoleArn":
			o.Roles.ImageRegistryARN = item.OutputValue
		case "CloudControllerManagerRoleArn":
			o.Roles.KubeCloudControllerARN = item.OutputValue
		case "EBSCSIRoleArn":
			o.Roles.StorageARN = item.OutputValue
		case "NetworkConfigRoleArn":
			o.Roles.NetworkARN = item.OutputValue
		case "NodePoolManagementRoleArn":
			o.Roles.NodePoolManagementARN = item.OutputValue
		case "ControlPlaneOperatorRoleArn":
			o.Roles.ControlPlaneOperatorARN = item.OutputValue
		case "WorkerInstanceProfileName":
			o.InstanceProfile = item.OutputValue
		}
	}
	return o, nil
}

// cfStackOutputs calls CloudFormation to retrieve the Outputs of a stack.
func cfStackOutputs(stackName, region string, env []string) ([]struct {
	OutputKey   string `json:"OutputKey"`
	OutputValue string `json:"OutputValue"`
}, error) {
	cmd := exec.Command("aws", "cloudformation", "describe-stacks",
		"--stack-name", stackName,
		"--region", region,
		"--query", "Stacks[0].Outputs",
		"--output", "json",
	)
	cmd.Env = env
	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cloudformation describe-stacks %s: %w", stackName, err)
	}
	var outputs []struct {
		OutputKey   string `json:"OutputKey"`
		OutputValue string `json:"OutputValue"`
	}
	if err := json.Unmarshal(raw, &outputs); err != nil {
		return nil, fmt.Errorf("parsing %s outputs: %w", stackName, err)
	}
	return outputs, nil
}

// verifyRolesTrustOIDCProvider checks that every operator role in roles has a
// trust-policy statement where:
//   - Principal.Federated == oidcProviderArn
//   - Action == sts:AssumeRoleWithWebIdentity
//   - Every StringEquals condition key is prefixed with the OIDC issuer domain
//     (the path component of the provider ARN after "oidc-provider/")
func verifyRolesTrustOIDCProvider(roles hypershiftv1beta1.AWSRolesRef, oidcProviderArn, region string, env []string) error {
	// Extract the issuer domain from the ARN:
	// arn:aws:iam::<account>:oidc-provider/<issuer-domain> → <issuer-domain>
	const prefix = ":oidc-provider/"
	idx := strings.Index(oidcProviderArn, prefix)
	if idx == -1 {
		return fmt.Errorf("unexpected OIDCProviderArn format: %s", oidcProviderArn)
	}
	issuerDomain := oidcProviderArn[idx+len(prefix):]

	roleArns := []string{
		roles.IngressARN,
		roles.ImageRegistryARN,
		roles.StorageARN,
		roles.NetworkARN,
		roles.KubeCloudControllerARN,
		roles.NodePoolManagementARN,
		roles.ControlPlaneOperatorARN,
	}

	type trustPolicy struct {
		Statement []struct {
			Effect    string `json:"Effect"`
			Principal struct {
				Federated string `json:"Federated"`
			} `json:"Principal"`
			Action    string                       `json:"Action"`
			Condition map[string]map[string]string `json:"Condition"`
		} `json:"Statement"`
	}

	for _, arn := range roleArns {
		roleName := arn[strings.LastIndex(arn, "/")+1:]
		cmd := exec.Command("aws", "iam", "get-role",
			"--role-name", roleName,
			"--query", "Role.AssumeRolePolicyDocument",
			"--output", "json",
			"--region", region,
		)
		cmd.Env = env
		raw, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("get-role %s: %w", roleName, err)
		}

		var policy trustPolicy
		if err := json.Unmarshal(raw, &policy); err != nil {
			return fmt.Errorf("parsing trust policy for %s: %w", roleName, err)
		}

		var matched bool
		for _, stmt := range policy.Statement {
			if stmt.Principal.Federated != oidcProviderArn {
				continue
			}
			if stmt.Action != "sts:AssumeRoleWithWebIdentity" {
				return fmt.Errorf("role %s: expected action sts:AssumeRoleWithWebIdentity, got %s", roleName, stmt.Action)
			}
			for key := range stmt.Condition["StringEquals"] {
				if !strings.HasPrefix(key, issuerDomain+":") {
					return fmt.Errorf("role %s: StringEquals key %q does not start with issuer domain %q", roleName, key, issuerDomain)
				}
			}
			matched = true
			break
		}
		if !matched {
			return fmt.Errorf("role %s: no trust statement found for OIDC provider %s", roleName, oidcProviderArn)
		}
		GinkgoWriter.Printf("  role %s: trust policy OK\n", roleName)
	}
	return nil
}

func deleteNodepool(ctx context.Context, cs *hyperfleet.Clientset, nodepoolName string) error {
	nodepools := cs.HyperfleetV1alpha1().NodePools()
	if err := nodepools.Delete(ctx, nodepoolName, platform.DeleteOptions{}); err != nil {
		return fmt.Errorf("nodepool delete: %w", err)
	}
	return nodepools.WaitUntil(ctx, nodepoolName,
		func(n *v1alpha1.NodePool) bool {
			if n == nil {
				GinkgoWriter.Printf("NodePool %s deleted\n", nodepoolName)
				return true
			}
			GinkgoWriter.Printf("[%s] nodepool %s: phase=%s, waiting for deletion\n",
				time.Now().Format(time.RFC3339), nodepoolName, n.Status.Phase)
			return false
		},
		nodepoolDeletePoll, nodepoolDeleteTimeout,
	)
}

func deleteDNSReservation(ctx context.Context, cs *hyperfleet.Clientset, reservationName string) error {
	reservations := cs.HyperfleetV1alpha1().DNSReservations()
	if err := reservations.Delete(ctx, reservationName, platform.DeleteOptions{}); err != nil {
		return fmt.Errorf("DNSReservation delete: %w", err)
	}
	return reservations.WaitUntil(ctx, reservationName,
		func(reservation *v1alpha1.DNSReservation) bool {
			if reservation == nil {
				GinkgoWriter.Printf("DNSReservation %s deleted\n", reservationName)
				return true
			}
			GinkgoWriter.Printf("[%s] DNSReservation %s: phase=%s, waiting for deletion\n",
				time.Now().Format(time.RFC3339), reservationName, reservation.Status.Phase)
			return false
		},
		oidcConfigDeletePoll, oidcConfigDeleteTimeout,
	)
}

func deleteOidcConfig(ctx context.Context, cs *hyperfleet.Clientset, configName string) error {
	configs := cs.HyperfleetV1alpha1().OidcConfigs()
	if err := configs.Delete(ctx, configName, platform.DeleteOptions{}); err != nil {
		return fmt.Errorf("OidcConfig delete: %w", err)
	}
	return configs.WaitUntil(ctx, configName,
		func(config *v1alpha1.OidcConfig) bool {
			if config == nil {
				GinkgoWriter.Printf("OidcConfig %s deleted\n", configName)
				return true
			}
			GinkgoWriter.Printf("[%s] OidcConfig %s: phase=%s, waiting for deletion\n",
				time.Now().Format(time.RFC3339), configName, config.Status.Phase)
			return false
		},
		dnsReservationDeletePoll, dnsReservationDeleteTimeout,
	)
}

func deleteCluster(ctx context.Context, cs *hyperfleet.Clientset, clusterName string) error {
	clusters := cs.HyperfleetV1alpha1().Clusters()
	if err := clusters.Delete(ctx, clusterName, platform.DeleteOptions{}); err != nil {
		return fmt.Errorf("cluster delete: %w", err)
	}
	return clusters.WaitUntil(ctx, clusterName,
		func(c *v1alpha1.Cluster) bool {
			if c == nil {
				GinkgoWriter.Printf("Cluster %s deleted\n", clusterName)
				return true
			}
			GinkgoWriter.Printf("[%s] cluster %s: phase=%s, waiting for deletion\n",
				time.Now().Format(time.RFC3339), clusterName, c.Status.Phase)
			return false
		},
		clusterDeletePoll, clusterDeleteTimeout,
	)
}

// infraStacksToDelete returns the rosactl sub-commands for resources still needing cleanup,
// ordered so dependent resources are removed before their dependencies.
func infraStacksToDelete(oidcProvider, vpc, iam bool) []string {
	var stacks []string
	if oidcProvider {
		stacks = append(stacks, "cluster-oidc")
	}
	if vpc {
		stacks = append(stacks, "cluster-vpc")
	}
	if iam {
		stacks = append(stacks, "cluster-iam")
	}
	return stacks
}
