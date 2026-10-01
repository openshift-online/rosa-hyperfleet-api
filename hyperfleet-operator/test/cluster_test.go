package integration

import (
	"encoding/json"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	dynamo "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo"
	hd "github.com/openshift-online/rosa-hyperfleet-kube-applier/hyperfleet-dynamo/dynamodb"
)

var _ = Describe("Cluster lifecycle", func() {
	const (
		clusterName  = "e2e-test-01"
		testNS       = "account-111222333444"
		nodePoolName = clusterName + ".workers"
	)

	AfterEach(func() {
		purgeResources()
		purgeDynamoTables()
		dynamoCli.ResetCache()
	})

	It("should write correct ApplyDesires to DynamoDB when a Cluster is created", func() {
		By("creating a Cluster CR")
		cluster := newTestCluster(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		By("waiting for Placement to be created and Bound")
		Eventually(func(g Gomega) {
			var p hyperfleetv1alpha1.Placement
			nn := types.NamespacedName{
				Namespace: testNS,
				Name:      clusterName + ".placement",
			}
			g.Expect(k8sClient.Get(ctx, nn, &p)).To(Succeed())
			g.Expect(p.Status.Phase).To(Equal(hyperfleetv1alpha1.PlacementPhaseBound))
			g.Expect(p.Spec.ManagementCluster).To(Equal("mc01"))
		}).Should(Succeed())

		By("waiting for ApplyDesires to appear in DynamoDB")
		specsTable := mc + "-specs-applydesires"
		Eventually(func(g Gomega) {
			items := scanTable(specsTable)
			g.Expect(len(items)).To(BeNumerically(">=", 6), "expected at least 6 ApplyDesires, got %d", len(items))
		}).Should(Succeed())

		By("verifying the 6 expected resources are present")
		items := scanTable(specsTable)
		resourceNames := map[string]bool{}
		for _, item := range items {
			name := attrString(item, "spec", "targetItem", "name")
			resource := attrString(item, "spec", "targetItem", "resource")
			resourceNames[resource+"/"+name] = true
		}

		// On the MC every cluster object lives in "cluster-<uid>".
		mcNS := hyperfleetv1alpha1.ManagementClusterNamespace(cluster.UID)
		expectedResources := []string{
			"namespaces/" + mcNS,
			"configmaps/cluster-config",
			"externalsecrets/pull-secret",
			"certificates/api-serving-cert",
			"hostedclusters/" + clusterName,
			"secrets/ssh-key",
		}
		for _, expected := range expectedResources {
			Expect(resourceNames).To(HaveKey(expected), "missing resource: %s", expected)
		}

		By("verifying HostedCluster content in DynamoDB")
		var hcContent map[string]any
		for _, item := range items {
			resource := attrString(item, "spec", "targetItem", "resource")
			if resource == "hostedclusters" {
				raw := attrString(item, "spec_kubeContent")
				Expect(raw).NotTo(BeEmpty(), "kubeContent should not be empty")
				Expect(json.Unmarshal([]byte(raw), &hcContent)).To(Succeed())
				break
			}
		}
		Expect(hcContent).NotTo(BeNil(), "HostedCluster not found in DynamoDB")

		spec := hcContent["spec"].(map[string]any)
		Expect(spec["issuerURL"]).To(Equal("https://oidc.e2e.example.com/e2e-test-01"))
		Expect(spec["infraID"]).To(Equal(string(cluster.UID)))

		dns := spec["dns"].(map[string]any)
		Expect(dns["baseDomain"]).To(MatchRegexp(`^[0-9a-f]{4}\.0\.e2e\.example\.com$`))

		By("verifying ReadDesire for HostedCluster status feedback")
		readTable := mc + "-specs-readdesires"
		Eventually(func(g Gomega) {
			readItems := scanTable(readTable)
			g.Expect(readItems).ToNot(BeEmpty())
			resource := attrString(readItems[0], "spec", "targetItem", "resource")
			g.Expect(resource).To(Equal("hostedclusters"))
		}).Should(Succeed())

		By("verifying document IDs are deterministic")
		nsDocID := dynamo.NewDocumentID("hyperfleet-operator", "", "v1", "namespaces", "", mcNS)
		found := false
		for _, item := range items {
			if docID, ok := item["documentID"]; ok {
				if sv, ok := docID.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value == nsDocID {
					found = true
					break
				}
			}
		}
		Expect(found).To(BeTrue(), "namespace desire should have deterministic document ID %s", nsDocID)
	})

	It("should write an oidc-signing-key ExternalSecret ApplyDesire when the Cluster has OidcConfigID set", func() {
		By("creating an unmanaged OidcConfig CR referenced by the Cluster")
		Expect(k8sClient.Create(ctx, newTestOidcConfig())).To(Succeed())

		By("creating a Cluster CR with OidcConfigID set")
		cluster := newTestClusterWithOidcConfig(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		By("waiting for Placement to be created and Bound")
		Eventually(func(g Gomega) {
			var p hyperfleetv1alpha1.Placement
			nn := types.NamespacedName{
				Namespace: testNS,
				Name:      clusterName + ".placement",
			}
			g.Expect(k8sClient.Get(ctx, nn, &p)).To(Succeed())
			g.Expect(p.Status.Phase).To(Equal(hyperfleetv1alpha1.PlacementPhaseBound))
		}).Should(Succeed())

		By("waiting for the oidc-signing-key ExternalSecret ApplyDesire to appear in DynamoDB")
		specsTable := mc + "-specs-applydesires"
		Eventually(func(g Gomega) {
			items := scanTable(specsTable)
			g.Expect(len(items)).To(BeNumerically(">=", 7), "expected at least 7 ApplyDesires, got %d", len(items))
			found := false
			for _, item := range items {
				resource := attrString(item, "spec", "targetItem", "resource")
				name := attrString(item, "spec", "targetItem", "name")
				if resource == "externalsecrets" && name == "oidc-signing-key" {
					found = true
					break
				}
			}
			g.Expect(found).To(BeTrue(), "expected an externalsecrets/oidc-signing-key ApplyDesire")
		}).Should(Succeed())

		By("verifying HostedCluster references the oidc-signing-key Secret")
		items := scanTable(specsTable)
		var hcContent map[string]any
		for _, item := range items {
			if attrString(item, "spec", "targetItem", "resource") == "hostedclusters" {
				raw := attrString(item, "spec_kubeContent")
				Expect(raw).NotTo(BeEmpty())
				Expect(json.Unmarshal([]byte(raw), &hcContent)).To(Succeed())
				break
			}
		}
		Expect(hcContent).NotTo(BeNil(), "HostedCluster not found in DynamoDB")
		spec := hcContent["spec"].(map[string]any)
		sask := spec["serviceAccountSigningKey"].(map[string]any)
		Expect(sask["name"]).To(Equal("oidc-signing-key"))
	})

	It("should propagate HostedCluster status from ReadDesire to Cluster CR", func() {
		By("creating a Cluster CR")
		cluster := newTestCluster(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		By("waiting for ReadDesire to appear in DynamoDB")
		readTable := mc + "-specs-readdesires"
		var readDocID string
		Eventually(func(g Gomega) {
			items := scanTable(readTable)
			g.Expect(items).ToNot(BeEmpty())
			readDocID = attrString(items[0], "documentID")
			g.Expect(readDocID).NotTo(BeEmpty())
		}).Should(Succeed())

		By("simulating kube-applier-aws writing HostedCluster status to status-readdesires")
		hcStatus := map[string]any{
			"status": map[string]any{
				"conditions": []map[string]any{
					{
						"type":               "Available",
						"status":             "True",
						"reason":             "HostedClusterAsExpected",
						"message":            "The hosted cluster is available",
						"lastTransitionTime": "2026-06-24T00:00:00Z",
					},
				},
				"controlPlaneEndpoint": map[string]any{
					"host": "api.e2e-test.example.com",
				},
				"version": map[string]any{
					"history": []map[string]any{
						{"version": "4.17.3"},
					},
				},
			},
		}
		hcJSON, err := json.Marshal(hcStatus)
		Expect(err).NotTo(HaveOccurred())

		statusTable := mc + "-status-readdesires"
		_, err = dynamoDBCli.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(statusTable),
			Item: map[string]dynamodbtypes.AttributeValue{
				"documentID":         &dynamodbtypes.AttributeValueMemberS{Value: readDocID},
				"updateTime":         &dynamodbtypes.AttributeValueMemberS{Value: time.Now().UTC().Format(time.RFC3339)},
				"shard":              &dynamodbtypes.AttributeValueMemberS{Value: hd.ComputeShardDefault(readDocID)},
				"status_kubeContent": &dynamodbtypes.AttributeValueMemberS{Value: string(hcJSON)},
			},
		})
		Expect(err).NotTo(HaveOccurred())

		// Dispatch manually as belt-and-suspenders; the GSI poller will also
		// fire within a few seconds now that shard+updateTime are written.
		eventRouter.Dispatch(readDocID)

		By("verifying Cluster CR status is updated with HostedCluster data")
		Eventually(func(g Gomega) {
			var c hyperfleetv1alpha1.Cluster
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &c)).To(Succeed())
			g.Expect(c.Status.ControlPlaneEndpoint.Host).To(Equal("api.e2e-test.example.com"))
			g.Expect(c.Status.Version).To(Equal("4.17.3"))
			g.Expect(c.Status.Phase).To(Equal(hyperfleetv1alpha1.ClusterPhaseReady))
		}).Should(Succeed())
	})

	It("should cascade delete NodePools, write delete desires, and remove Placement when Cluster is deleted", func() {
		By("creating a Cluster CR")
		cluster := newTestCluster(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		By("waiting for PlacementRef to be set")
		Eventually(func(g Gomega) {
			var c hyperfleetv1alpha1.Cluster
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &c)).To(Succeed())
			g.Expect(c.Status.PlacementRef).NotTo(BeNil())
		}).Should(Succeed())

		By("creating a NodePool CR")
		np := newTestNodePool(cluster)
		Expect(k8sClient.Create(ctx, np)).To(Succeed())

		By("waiting for NodePool ApplyDesire to confirm both CRs are reconciled")
		specsApply := mc + "-specs-applydesires"
		Eventually(func(g Gomega) {
			items := scanTable(specsApply)
			for _, item := range items {
				if attrString(item, "spec", "targetItem", "resource") == "nodepools" {
					return
				}
			}
			g.Expect(false).To(BeTrue(), "nodepool desire not found yet")
		}).Should(Succeed())

		By("deleting the Cluster CR")
		var toDelete hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &toDelete)).To(Succeed())
		Expect(k8sClient.Delete(ctx, &toDelete)).To(Succeed())

		By("verifying NodePool CR is deleted")
		Eventually(func() error {
			nn := types.NamespacedName{Namespace: testNS, Name: nodePoolName}
			return k8sClient.Get(ctx, nn, &hyperfleetv1alpha1.NodePool{})
		}).ShouldNot(Succeed())

		By("verifying delete desire status entries exist in the applydesires status table")
		statusApply := mc + "-status-applydesires"
		Eventually(func(g Gomega) {
			// Status entries with Type=Delete prove the desires were created and confirmed.
			statusItems := scanTable(statusApply)
			g.Expect(len(statusItems)).To(BeNumerically(">=", 2), "expected status entries for processed delete desires")
		}).Should(Succeed())

		By("verifying ApplyDesire specs are cleaned up from DynamoDB")
		Eventually(func(g Gomega) {
			items := scanTable(specsApply)
			g.Expect(items).To(BeEmpty(), "all ApplyDesire specs should be cleaned up on deletion")
		}).Should(Succeed())

		By("verifying Placement CR is deleted")
		Eventually(func() error {
			nn := types.NamespacedName{
				Namespace: testNS,
				Name:      clusterName + ".placement",
			}
			return k8sClient.Get(ctx, nn, &hyperfleetv1alpha1.Placement{})
		}).ShouldNot(Succeed())

		By("verifying Cluster CR is fully gone")
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &hyperfleetv1alpha1.Cluster{})
		}).ShouldNot(Succeed())
	})

	It("should automatically delete an expired cluster through the full lifecycle", func() {
		By("creating an expired Cluster CR")
		cluster := newExpiredTestCluster("e2e-expired-01")
		cluster.Name = clusterName
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		By("verifying the cluster is fully deleted (expiration triggers deletion, kube-applier confirms)")
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &hyperfleetv1alpha1.Cluster{})
		}).ShouldNot(Succeed())

		By("verifying Placement CR is also cleaned up")
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{
				Namespace: testNS,
				Name:      clusterName + ".placement",
			}, &hyperfleetv1alpha1.Placement{})
		}).ShouldNot(Succeed())

		By("verifying ApplyDesire specs are cleaned up from DynamoDB")
		specsApply := mc + "-specs-applydesires"
		Eventually(func(g Gomega) {
			items := scanTable(specsApply)
			g.Expect(items).To(BeEmpty(), "all ApplyDesire specs should be cleaned up after expiration-driven deletion")
		}).Should(Succeed())
	})

	It("should write NodePool ApplyDesire when NodePool CR is created", func() {
		By("creating a Cluster CR with PlacementRef")
		cluster := newTestCluster(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		Eventually(func(g Gomega) {
			var c hyperfleetv1alpha1.Cluster
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &c)).To(Succeed())
			g.Expect(c.Status.PlacementRef).NotTo(BeNil())
		}).Should(Succeed())

		By("creating a NodePool CR")
		np := newTestNodePool(cluster)
		Expect(k8sClient.Create(ctx, np)).To(Succeed())

		By("waiting for NodePool ApplyDesire in DynamoDB")
		specsTable := mc + "-specs-applydesires"
		Eventually(func(g Gomega) {
			items := scanTable(specsTable)
			for _, item := range items {
				resource := attrString(item, "spec", "targetItem", "resource")
				if resource == "nodepools" {
					// On the MC only the child part of "<cluster>.workers" is used.
					g.Expect(attrString(item, "spec", "targetItem", "name")).To(Equal("workers"))
					g.Expect(attrString(item, "spec", "targetItem", "namespace")).To(Equal(hyperfleetv1alpha1.ManagementClusterNamespace(cluster.UID)))
					return
				}
			}
			g.Expect(false).To(BeTrue(), "nodepool desire not found")
		}).Should(Succeed())
	})

	It("should write NodePool delete ApplyDesire when only the NodePool is deleted", func() {
		By("creating a Cluster CR")
		cluster := newTestCluster(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		By("waiting for PlacementRef")
		Eventually(func(g Gomega) {
			var c hyperfleetv1alpha1.Cluster
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &c)).To(Succeed())
			g.Expect(c.Status.PlacementRef).NotTo(BeNil())
		}).Should(Succeed())

		By("creating a NodePool CR")
		np := newTestNodePool(cluster)
		Expect(k8sClient.Create(ctx, np)).To(Succeed())

		By("waiting for NodePool ApplyDesire in DynamoDB")
		specsApply := mc + "-specs-applydesires"
		Eventually(func(g Gomega) {
			items := scanTable(specsApply)
			for _, item := range items {
				if attrString(item, "spec", "targetItem", "resource") == "nodepools" {
					return
				}
			}
			g.Expect(false).To(BeTrue(), "nodepool desire not found")
		}).Should(Succeed())

		By("deleting only the NodePool CR")
		Eventually(func() error {
			var npToDelete hyperfleetv1alpha1.NodePool
			nn := types.NamespacedName{Namespace: testNS, Name: nodePoolName}
			if err := k8sClient.Get(ctx, nn, &npToDelete); err != nil {
				return err
			}
			return k8sClient.Delete(ctx, &npToDelete)
		}).Should(Succeed())

		By("verifying NodePool ApplyDesire is cleaned up from DynamoDB")
		Eventually(func(g Gomega) {
			items := scanTable(specsApply)
			for _, item := range items {
				g.Expect(attrString(item, "spec", "targetItem", "resource")).NotTo(Equal("nodepools"),
					"nodepool ApplyDesire should be cleaned up on deletion")
			}
		}).Should(Succeed())

		By("verifying NodePool delete ApplyDesire was processed and status recorded in DynamoDB")
		statusApply := mc + "-status-applydesires"
		Eventually(func(g Gomega) {
			// Status entry proves the delete desire was created and confirmed.
			statusItems := scanTable(statusApply)
			found := false
			for _, item := range statusItems {
				if docID, ok := item["documentID"]; ok {
					if sv, ok := docID.(*dynamodbtypes.AttributeValueMemberS); ok && sv.Value != "" {
						found = true
						break
					}
				}
			}
			g.Expect(found).To(BeTrue(), "expected status entry for processed nodepool delete ApplyDesire")
		}).Should(Succeed())

		By("verifying NodePool CR is fully gone")
		Eventually(func() error {
			nn := types.NamespacedName{Namespace: testNS, Name: nodePoolName}
			return k8sClient.Get(ctx, nn, &hyperfleetv1alpha1.NodePool{})
		}).ShouldNot(Succeed())

		By("verifying Cluster and Placement are still alive")
		var c hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &c)).To(Succeed())
		var p hyperfleetv1alpha1.Placement
		nn := types.NamespacedName{
			Namespace: testNS,
			Name:      clusterName + ".placement",
		}
		Expect(k8sClient.Get(ctx, nn, &p)).To(Succeed())
	})

	It("should keep two clusters' same-named pools apart, and give a recreated cluster nothing of the old one", func() {
		By("creating two clusters in one account, each with a workers pool")
		a := newTestCluster("e2e-a")
		b := newTestCluster("e2e-b")
		Expect(k8sClient.Create(ctx, a)).To(Succeed())
		Expect(k8sClient.Create(ctx, b)).To(Succeed())
		for _, c := range []*hyperfleetv1alpha1.Cluster{a, b} {
			Eventually(func(g Gomega) {
				var latest hyperfleetv1alpha1.Cluster
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: c.Name}, &latest)).To(Succeed())
				g.Expect(latest.Status.PlacementRef).NotTo(BeNil())
			}).Should(Succeed())
			Expect(k8sClient.Create(ctx, newTestNodePool(c))).To(Succeed())
		}

		poolDesires := func() []string {
			var out []string
			for _, item := range scanTable(mc + "-specs-applydesires") {
				if attrString(item, "spec", "targetItem", "resource") == "nodepools" {
					out = append(out, attrString(item, "spec", "targetItem", "namespace")+"/"+attrString(item, "spec", "targetItem", "name"))
				}
			}
			return out
		}
		nsA := hyperfleetv1alpha1.ManagementClusterNamespace(a.UID)
		nsB := hyperfleetv1alpha1.ManagementClusterNamespace(b.UID)
		Eventually(poolDesires).Should(ConsistOf(nsA+"/workers", nsB+"/workers"))

		By("deleting cluster a")
		// By key: the operator has since updated it, and deletes are CAS-checked.
		Expect(k8sClient.Delete(ctx, &hyperfleetv1alpha1.Cluster{ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: "e2e-a"}})).To(Succeed())
		Eventually(func() error {
			return k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "e2e-a"}, &hyperfleetv1alpha1.Cluster{})
		}).ShouldNot(Succeed())

		By("verifying b and its pool are untouched")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "e2e-b"}, &hyperfleetv1alpha1.Cluster{})).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "e2e-b.workers"}, &hyperfleetv1alpha1.NodePool{})).To(Succeed())
		Eventually(poolDesires).Should(ConsistOf(nsB + "/workers"))

		By("recreating cluster a under the same name")
		again := newTestCluster("e2e-a")
		Expect(k8sClient.Create(ctx, again)).To(Succeed())
		Expect(again.UID).NotTo(Equal(a.UID), "a recreated cluster gets a new uid")

		By("verifying it inherits nothing: no pools, a fresh placement, its own DNS claim")
		Eventually(func(g Gomega) {
			var p hyperfleetv1alpha1.Placement
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "e2e-a.placement"}, &p)).To(Succeed())
			g.Expect(p.Labels[hyperfleetv1alpha1.ClusterUIDLabel]).To(Equal(string(again.UID)))
			var latest hyperfleetv1alpha1.Cluster
			g.Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "e2e-a"}, &latest)).To(Succeed())
			g.Expect(latest.Status.BaseDomain).NotTo(BeEmpty())
		}).Should(Succeed())
		var pools hyperfleetv1alpha1.NodePoolList
		Expect(k8sClient.List(ctx, &pools, client.MatchingLabels{hyperfleetv1alpha1.ClusterUIDLabel: string(again.UID)})).To(Succeed())
		Expect(pools.Items).To(BeEmpty())
		var oldClaims hyperfleetv1alpha1.IndexList
		Expect(k8sClient.List(ctx, &oldClaims, client.MatchingLabels{hyperfleetv1alpha1.OwnerUIDLabel: string(a.UID)})).To(Succeed())
		Expect(oldClaims.Items).To(BeEmpty(), "the deleted cluster's DNS claim should be released")
	})
})
