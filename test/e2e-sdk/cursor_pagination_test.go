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

package e2e_sdk_test

import (
	"context"
	"fmt"
	"os"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	hyperfleet "github.com/openshift-online/rosa-hyperfleet-api/clientset"
	"github.com/openshift-online/rosa-hyperfleet-api/clientset/platform"
	hfrest "github.com/openshift-online/rosa-hyperfleet-api/clientset/rest"
)

var _ = Describe("SDK E2E: cursor-based pagination", Ordered, func() {
	var (
		ctx      context.Context
		cs       *hyperfleet.Clientset
		clusterA *v1alpha1.Cluster
		clusterB *v1alpha1.Cluster
		clusterC *v1alpha1.Cluster // created between page 1 and page 2
	)

	BeforeAll(func() {
		ctx = context.Background()

		baseURL := os.Getenv("E2E_BASE_URL")
		if baseURL == "" {
			Skip("E2E_BASE_URL is not set")
		}
		customerProfile := os.Getenv("CUSTOMER_AWS_PROFILE")
		if customerProfile == "" {
			Skip("CUSTOMER_AWS_PROFILE is not set")
		}

		region := os.Getenv("AWS_REGION")
		if region == "" {
			region = defaultRegion
		}

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx,
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

		suffix := fmt.Sprintf("%d", time.Now().Unix())
		clusters := cs.HyperfleetV1alpha1().Clusters()

		By("verifying the account has no pre-existing clusters")
		existing, listErr := clusters.List(ctx, platform.ListOptions{Limit: 100})
		Expect(listErr).ToNot(HaveOccurred(), "listing existing clusters")
		Expect(existing.Items).To(BeEmpty(),
			"account must be empty before the pagination test; found %d cluster(s) — ensure the lifecycle test has fully cleaned up before running this suite",
			len(existing.Items))

		By("creating two clusters before pagination begins")
		clusterA, err = clusters.Create(ctx, minimalCluster("pag-a-"+suffix), platform.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "creating cluster A")
		GinkgoWriter.Println("Cluster A created")

		clusterB, err = clusters.Create(ctx, minimalCluster("pag-b-"+suffix), platform.CreateOptions{})
		Expect(err).ToNot(HaveOccurred(), "creating cluster B")
		GinkgoWriter.Println("Cluster B created")

		DeferCleanup(func() {
			cleanCtx := context.Background()
			for _, c := range []*v1alpha1.Cluster{clusterA, clusterB, clusterC} {
				if c == nil {
					continue
				}
				if err := clusters.Delete(cleanCtx, string(c.UID), platform.DeleteOptions{}); err != nil {
					GinkgoWriter.Printf("WARNING: failed to delete cluster: %v\n", err)
				}
			}
		})
	})

	It("paginates through clusters using the continue token and cuts off late-arriving writes", func() {
		clusters := cs.HyperfleetV1alpha1().Clusters()
		suffix := fmt.Sprintf("%d", time.Now().Unix())

		By("listing page 1 with limit=1")
		page1, err := clusters.List(ctx, platform.ListOptions{Limit: 1})
		Expect(err).ToNot(HaveOccurred(), "listing page 1")
		Expect(page1.Items).To(HaveLen(1), "page 1 should contain exactly 1 cluster")
		Expect(page1.Continue).ToNot(BeEmpty(), "page 1 should carry a continue token")
		GinkgoWriter.Printf("Page 1: %d item(s)\n", len(page1.Items))

		By("creating a third cluster after the cursor was issued")
		var createErr error
		clusterC, createErr = clusters.Create(ctx, minimalCluster("pag-c-"+suffix), platform.CreateOptions{})
		Expect(createErr).ToNot(HaveOccurred(), "creating cluster C between pages")
		GinkgoWriter.Println("Cluster C created after cursor")

		By("listing page 2 using the continue token from page 1")
		page2, err := clusters.List(ctx, platform.ListOptions{
			Limit:    1,
			Continue: page1.Continue,
		})
		Expect(err).ToNot(HaveOccurred(), "listing page 2")
		Expect(page2.Items).To(HaveLen(1), "page 2 should contain exactly 1 cluster")
		GinkgoWriter.Printf("Page 2: %d item(s)\n", len(page2.Items))

		By("verifying both original clusters appear across the two pages")
		seen := map[string]bool{}
		for _, c := range page1.Items {
			seen[string(c.UID)] = true
		}
		for _, c := range page2.Items {
			seen[string(c.UID)] = true
		}
		Expect(seen).To(HaveKey(string(clusterA.UID)), "cluster A should appear in paginated results")
		Expect(seen).To(HaveKey(string(clusterB.UID)), "cluster B should appear in paginated results")

		By("verifying the two pages returned different clusters")
		Expect(page1.Items[0].UID).ToNot(Equal(page2.Items[0].UID),
			"page 1 and page 2 should return different clusters")

		By("verifying the continue token is empty after page 2 — snapshot excludes cluster C")
		// The cursor carries the snapshot watermark from page 1's query. Cluster C
		// was created after that snapshot, so txid_stamp(C) > watermark.
		// The SQL WHERE txid_stamp <= watermark excludes C from all subsequent pages,
		// making page 2 the last page of this traversal.
		Expect(page2.Continue).To(BeEmpty(),
			"page 2 should be the last page; C has txid_stamp > watermark and is excluded by snapshot isolation")
	})
})

// minimalCluster returns a Cluster with enough spec to pass server-side
// validation. No AWS infrastructure is created — these clusters exist only to
// exercise pagination and are deleted by DeferCleanup.
func minimalCluster(name string) *v1alpha1.Cluster {
	return &v1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: v1alpha1.ClusterSpec{
			HostedCluster: v1alpha1.HostedClusterSpecPassthrough{
				Platform: hypershiftv1beta1.PlatformSpec{
					Type: hypershiftv1beta1.AWSPlatform,
				},
			},
		},
	}
}
