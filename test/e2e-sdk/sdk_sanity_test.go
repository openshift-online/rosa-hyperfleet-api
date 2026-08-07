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
//	BASE_URL                  — platform API base URL
//	ROSACTL_BIN               — path to the rosactl binary
//	CUSTOMER_AWS_PROFILE      — AWS profile for customer-account operations
//
// Optional:
//
//	AWS_REGION                — defaults to us-east-1
//	E2E_ACCOUNT_ID            — RC account ID (derived from STS if absent)
//	E2E_CUSTOMER_ACCOUNT_ID   — customer account ID (derived from STS if absent)
//	HCP_CLUSTER_NAME          — fixed cluster name (generated if absent)
//	HCP_ROSA_ISSUER_URL       — OIDC issuer URL override when not in cluster response
//	HYPERFLEET_VERSION        — release image passed to release.image; server resolves version when empty
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

	hyperfleet "github.com/openshift-online/rosa-hyperfleet-api/clientset"
	hfrest "github.com/openshift-online/rosa-hyperfleet-api/clientset/rest"
	"github.com/openshift-online/rosa-hyperfleet-api/clientset/wrappers"
	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	awstest "github.com/openshift-online/rosa-hyperfleet-api/test/helpers/aws"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	clusterReadyTimeout  = 35 * time.Minute
	clusterReadyPoll     = 30 * time.Second
	clusterDeleteTimeout = 20 * time.Minute
	clusterDeletePoll    = 30 * time.Second

	nodepoolReadyTimeout  = 30 * time.Minute
	nodepoolReadyPoll     = 30 * time.Second
	nodepoolDeleteTimeout = 20 * time.Minute
	nodepoolDeletePoll    = 30 * time.Second

	defaultRegion       = "us-east-1"
	defaultInstanceType = "m5.xlarge"
)

// iamStackOutputs holds the IAM role ARNs and instance profile read from the
// rosa-<cluster>-iam CloudFormation stack.
type iamStackOutputs struct {
	Roles           hypershiftv1beta1.AWSRolesRef
	InstanceProfile string
}

var _ = Describe("SDK E2E: cluster and nodepool lifecycle", Ordered, func() {
	var (
		ctx               context.Context
		baseURL           string
		rosactlBin        string
		customerProfile   string
		region            string
		accountID         string
		customerAccountID string
		version           string
		instanceType      string
		clusterName       string
		clusterID         string
		oidcIssuerURL     string
		nodepoolID        string

		// Populated after stack creation from CloudFormation outputs.
		vpcID    string
		subnetID string
		iamOut   iamStackOutputs

		awsCfg    aws.Config
		cs        *hyperfleet.Clientset
		apiClient *awstest.APIClient

		vpcCreated           bool
		iamCreated           bool
		oidcCreated          bool
		clusterCreated       bool
		nodepoolCreated      bool
		extraNodepoolID      string
		extraNodepoolCreated bool
	)

	BeforeAll(func() {
		ctx = context.Background()

		baseURL = os.Getenv("BASE_URL")
		if baseURL == "" {
			Skip("BASE_URL is not set")
		}
		rosactlBin = os.Getenv("ROSACTL_BIN")
		if rosactlBin == "" {
			Skip("ROSACTL_BIN is not set")
		}
		customerProfile = os.Getenv("CUSTOMER_AWS_PROFILE")
		if customerProfile == "" {
			Skip("CUSTOMER_AWS_PROFILE is not set")
		}

		region = os.Getenv("AWS_REGION")
		if region == "" {
			region = defaultRegion
			GinkgoWriter.Printf("No AWS_REGION set, defaulting to %s\n", region)
		}

		version = os.Getenv("HYPERFLEET_VERSION")

		instanceType = os.Getenv("HYPERFLEET_INSTANCE_TYPE")
		if instanceType == "" {
			instanceType = defaultInstanceType
		}

		accountID = os.Getenv("E2E_ACCOUNT_ID")
		if accountID == "" {
			cmd := exec.Command("aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text")
			out, err := cmd.CombinedOutput()
			Expect(err).ToNot(HaveOccurred(), "getting RC account ID: %s", string(out))
			accountID = strings.TrimSpace(string(out))
		}
		GinkgoWriter.Printf("RC account ID: %s\n", accountID)

		customerAccountID = os.Getenv("E2E_CUSTOMER_ACCOUNT_ID")
		if customerAccountID == "" {
			cmd := exec.Command("aws", "sts", "get-caller-identity", "--query", "Account", "--output", "text")
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

			if extraNodepoolCreated && extraNodepoolID != "" && clusterID != "" {
				GinkgoWriter.Printf("DeferCleanup: deleting extra nodepool %s\n", extraNodepoolID)
				if err := deleteNodepool(cleanupCtx, cs, clusterID, extraNodepoolID); err != nil {
					GinkgoWriter.Printf("DeferCleanup WARNING: %v\n", err)
				} else {
					extraNodepoolCreated = false
				}
			}
			if nodepoolCreated && nodepoolID != "" && clusterID != "" {
				GinkgoWriter.Printf("DeferCleanup: initiating nodepool %s deletion\n", nodepoolID)
				if err := cs.HyperfleetV1alpha1().NodePools(clusterID).Delete(cleanupCtx, nodepoolID, wrappers.DeleteOptions{}); err != nil {
					GinkgoWriter.Printf("DeferCleanup WARNING: nodepool delete: %v\n", err)
				} else {
					nodepoolCreated = false
				}
			}
			if clusterCreated && clusterID != "" {
				GinkgoWriter.Printf("DeferCleanup: deleting cluster %s (id=%s)\n", clusterName, clusterID)
				if err := deleteCluster(cleanupCtx, cs, customerAccountID, clusterID, clusterName); err != nil {
					GinkgoWriter.Printf("DeferCleanup WARNING: %v\n", err)
				} else {
					clusterCreated = false
				}
			}
			for _, sub := range infraStacksToDelete(oidcCreated, vpcCreated, iamCreated) {
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

		By("registering customer account")
		resp, err := apiClient.Post("/api/v0/accounts", map[string]interface{}{
			"accountId":  customerAccountID,
			"privileged": true,
		}, accountID)
		Expect(err).ToNot(HaveOccurred())
		switch resp.StatusCode {
		case http.StatusCreated:
			GinkgoWriter.Printf("Customer account %s registered\n", customerAccountID)
		case http.StatusConflict:
			var body map[string]interface{}
			Expect(json.Unmarshal(resp.Body, &body)).To(Succeed())
			Expect(body["code"]).To(Equal("account-exists"),
				"unexpected 409 body: %s", string(resp.Body))
			GinkgoWriter.Printf("Customer account %s already registered\n", customerAccountID)
		default:
			Fail(fmt.Sprintf("account registration: status %d body: %s", resp.StatusCode, string(resp.Body)))
		}

		By("creating cluster via SDK")
		subnetRef := subnetID
		subnetOut, err := ec2.NewFromConfig(awsCfg).DescribeSubnets(ctx, &ec2.DescribeSubnetsInput{
			SubnetIds: []string{subnetID},
		})
		Expect(err).ToNot(HaveOccurred(), "describing subnet %s", subnetID)
		Expect(subnetOut.Subnets).ToNot(BeEmpty(), "subnet %s not found", subnetID)
		zone := *subnetOut.Subnets[0].AvailabilityZone

		cluster, err := cs.HyperfleetV1alpha1().Clusters(customerAccountID).Create(ctx, &v1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: clusterName},
			Spec: v1alpha1.ClusterSpec{
				HostedCluster: v1alpha1.HostedClusterSpecPassthrough{
					Release: hypershiftv1beta1.Release{Image: version},
					Platform: hypershiftv1beta1.PlatformSpec{
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
		}, wrappers.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "SDK cluster create")
		clusterID = string(cluster.UID)
		clusterCreated = true
		GinkgoWriter.Printf("Cluster %s created (id=%s)\n", clusterName, clusterID)

		By("creating OIDC provider")
		oidcIssuerURL = cluster.Spec.HostedCluster.IssuerURL
		if oidcIssuerURL == "" {
			oidcIssuerURL = os.Getenv("HCP_ROSA_ISSUER_URL")
		}
		Expect(oidcIssuerURL).ToNot(BeEmpty(),
			"OIDC issuer URL not in cluster response; set HCP_ROSA_ISSUER_URL")
		cmd = exec.Command(rosactlBin, "cluster-oidc", "create", clusterName,
			"--region", region, "--oidc-issuer-url", oidcIssuerURL)
		cmd.Env = customerEnv
		cmd.Stdout = GinkgoWriter
		cmd.Stderr = GinkgoWriter
		Expect(cmd.Run()).To(Succeed(), "rosactl cluster-oidc create")
		oidcCreated = true

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
		Expect(verifyRolesTrustOIDCProvider(iamOut.Roles, oidcProviderArn, customerEnv)).To(Succeed())
		GinkgoWriter.Printf("All IAM roles trust OIDC provider %s\n", oidcProviderArn)

		By("waiting for cluster Ready")
		clusters := cs.HyperfleetV1alpha1().Clusters(customerAccountID)
		Expect(clusters.WaitUntil(ctx, clusterID,
			func(c *v1alpha1.Cluster) bool {
				if c == nil {
					return false
				}
				GinkgoWriter.Printf("[%s] cluster %s: phase=%s\n",
					time.Now().Format(time.RFC3339), clusterName, c.Status.Phase)
				return c.Status.Phase == v1alpha1.ClusterPhaseReady
			},
			clusterReadyPoll, clusterReadyTimeout,
		)).To(Succeed(), "cluster should reach Ready phase")
		GinkgoWriter.Printf("Cluster %s is Ready\n", clusterName)

		By("creating nodepool via SDK")
		npName := "e2e-np-" + clusterName
		initialReplicas := int32(2)
		npSubnetRef := subnetID
		np, err := cs.HyperfleetV1alpha1().NodePools(clusterID).Create(ctx, &v1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: npName},
			Spec: v1alpha1.NodePoolSpec{
				NodePool: v1alpha1.NodePoolSpecPassthrough{
					ClusterName: clusterName,
					Replicas:    &initialReplicas,
					Platform: hypershiftv1beta1.NodePoolPlatform{
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
		}, wrappers.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "SDK nodepool create")
		nodepoolID = string(np.UID)
		nodepoolCreated = true
		GinkgoWriter.Printf("NodePool %s created (id=%s)\n", npName, nodepoolID)

		By("waiting for nodepool Ready")
		nodepools := cs.HyperfleetV1alpha1().NodePools(clusterID)
		Expect(nodepools.WaitUntil(ctx, nodepoolID,
			func(n *v1alpha1.NodePool) bool {
				if n == nil {
					return false
				}
				GinkgoWriter.Printf("[%s] nodepool %s: phase=%s\n",
					time.Now().Format(time.RFC3339), npName, n.Status.Phase)
				return n.Status.Phase == v1alpha1.NodePoolPhaseReady
			},
			nodepoolReadyPoll, nodepoolReadyTimeout,
		)).To(Succeed(), "nodepool should reach Ready phase")
		GinkgoWriter.Printf("NodePool %s is Ready\n", npName)

		By("patching nodepool replicas")
		current, err := nodepools.Get(ctx, nodepoolID, wrappers.GetOptions{})
		Expect(err).ToNot(HaveOccurred(), "getting nodepool for patch")
		newReplicas := int32(3)
		current.Spec.NodePool.Replicas = &newReplicas
		updated, err := nodepools.Update(ctx, current, wrappers.UpdateOptions{})
		Expect(err).ToNot(HaveOccurred(), "updating nodepool replicas")
		Expect(*updated.Spec.NodePool.Replicas).To(Equal(newReplicas))
		GinkgoWriter.Printf("NodePool replicas updated to %d\n", newReplicas)

		By("creating extra nodepool for deletion test")
		extraNpName := "e2e-np-extra-" + clusterName
		extraReplicas := int32(1)
		extraNpSubnetRef := subnetID
		extraNp, err := cs.HyperfleetV1alpha1().NodePools(clusterID).Create(ctx, &v1alpha1.NodePool{
			ObjectMeta: metav1.ObjectMeta{Name: extraNpName},
			Spec: v1alpha1.NodePoolSpec{
				NodePool: v1alpha1.NodePoolSpecPassthrough{
					ClusterName: clusterName,
					Replicas:    &extraReplicas,
					Platform: hypershiftv1beta1.NodePoolPlatform{
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
		}, wrappers.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "SDK extra nodepool create")
		extraNodepoolID = string(extraNp.UID)
		extraNodepoolCreated = true
		GinkgoWriter.Printf("Extra NodePool %s created (id=%s)\n", extraNpName, extraNodepoolID)

		By("waiting for extra nodepool Ready")
		Expect(nodepools.WaitUntil(ctx, extraNodepoolID,
			func(n *v1alpha1.NodePool) bool {
				if n == nil {
					return false
				}
				GinkgoWriter.Printf("[%s] extra nodepool %s: phase=%s\n",
					time.Now().Format(time.RFC3339), extraNpName, n.Status.Phase)
				return n.Status.Phase == v1alpha1.NodePoolPhaseReady
			},
			nodepoolReadyPoll, nodepoolReadyTimeout,
		)).To(Succeed(), "extra nodepool should reach Ready phase")
		GinkgoWriter.Printf("Extra NodePool %s is Ready\n", extraNpName)

		By("deleting extra nodepool")
		Expect(deleteNodepool(ctx, cs, clusterID, extraNodepoolID)).To(Succeed())
		extraNodepoolCreated = false

		By("initiating nodepool deletion")
		Expect(cs.HyperfleetV1alpha1().NodePools(clusterID).Delete(ctx, nodepoolID, wrappers.DeleteOptions{})).To(Succeed())
		nodepoolCreated = false
		GinkgoWriter.Printf("NodePool %s deletion initiated\n", nodepoolID)

		By("deleting cluster")
		Expect(deleteCluster(ctx, cs, customerAccountID, clusterID, clusterName)).To(Succeed())
		clusterCreated = false

		By("tearing down OIDC, VPC, and IAM")
		for _, sub := range infraStacksToDelete(oidcCreated, vpcCreated, iamCreated) {
			cmd = exec.Command(rosactlBin, sub, "delete", clusterName, "--region", region)
			cmd.Env = customerEnv
			cmd.Stdout = GinkgoWriter
			cmd.Stderr = GinkgoWriter
			Expect(cmd.Run()).To(Succeed(), "rosactl %s delete %s", sub, clusterName)
		}
		vpcCreated = false
		iamCreated = false
		oidcCreated = false
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
func verifyRolesTrustOIDCProvider(roles hypershiftv1beta1.AWSRolesRef, oidcProviderArn string, env []string) error {
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

func deleteNodepool(ctx context.Context, cs *hyperfleet.Clientset, clusterID, nodepoolID string) error {
	nodepools := cs.HyperfleetV1alpha1().NodePools(clusterID)
	if err := nodepools.Delete(ctx, nodepoolID, wrappers.DeleteOptions{}); err != nil {
		return fmt.Errorf("nodepool delete: %w", err)
	}
	return nodepools.WaitUntil(ctx, nodepoolID,
		func(n *v1alpha1.NodePool) bool {
			if n == nil {
				GinkgoWriter.Printf("NodePool %s deleted\n", nodepoolID)
				return true
			}
			GinkgoWriter.Printf("[%s] nodepool %s: phase=%s, waiting for deletion\n",
				time.Now().Format(time.RFC3339), nodepoolID, n.Status.Phase)
			return false
		},
		nodepoolDeletePoll, nodepoolDeleteTimeout,
	)
}

func deleteCluster(ctx context.Context, cs *hyperfleet.Clientset, customerAccountID, clusterID, clusterName string) error {
	clusters := cs.HyperfleetV1alpha1().Clusters(customerAccountID)
	if err := clusters.Delete(ctx, clusterID, wrappers.DeleteOptions{}); err != nil {
		return fmt.Errorf("cluster delete: %w", err)
	}
	return clusters.WaitUntil(ctx, clusterID,
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
func infraStacksToDelete(oidc, vpc, iam bool) []string {
	var stacks []string
	if oidc {
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
