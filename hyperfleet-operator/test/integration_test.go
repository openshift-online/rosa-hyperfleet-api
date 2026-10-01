package integration

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	dynamo "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo"
)

var _ = Describe("Cross-component interaction", func() {
	AfterEach(func() {
		purgeResources()
		purgeDynamoTables()
		dynamoCli.ResetCache()
	})

	It("should keep manifest and cluster desires separate", func() {
		By("creating a Cluster CR")
		cluster := newTestCluster("e2e-sep-test")
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		By("creating a Manifest CR on the same MC")
		hfm := newTestManifest("e2e-monitoring")
		Expect(k8sClient.Create(ctx, hfm)).To(Succeed())

		// On the MC the cluster's objects live in "cluster-<uid>".
		mcNS := hyperfleetv1alpha1.ManagementClusterNamespace(cluster.UID)

		By("waiting for both to write desires")
		specsTable := mc + "-specs-applydesires"
		Eventually(func(g Gomega) {
			items := scanTable(specsTable)
			hasClusterNS := false
			hasManifestSA := false
			for _, item := range items {
				resource := attrString(item, "spec", "targetItem", "resource")
				name := attrString(item, "spec", "targetItem", "name")
				if resource == "namespaces" && name == mcNS {
					hasClusterNS = true
				}
				if resource == "serviceaccounts" && name == "e2e-runner" {
					hasManifestSA = true
				}
			}
			g.Expect(hasClusterNS).To(BeTrue(), "cluster namespace desire missing")
			g.Expect(hasManifestSA).To(BeTrue(), "manifest SA desire missing")
		}).Should(Succeed())

		By("verifying document IDs don't collide")
		clusterDocID := dynamo.NewDocumentID("hyperfleet-operator", "", "v1", "namespaces", "", mcNS)
		manifestDocID := dynamo.NewDocumentID(
			"hyperfleet-manifest/mc01/e2e-monitoring",
			"", "v1", "serviceaccounts", "e2e-actions", "e2e-runner",
		)
		Expect(clusterDocID).NotTo(Equal(manifestDocID))

		items := scanTable(specsTable)
		docIDs := map[string]bool{}
		for _, item := range items {
			docIDs[attrString(item, "documentID")] = true
		}
		Expect(docIDs).To(HaveKey(clusterDocID))
		Expect(docIDs).To(HaveKey(manifestDocID))
	})
})
