package integration

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/silence"
)

var _ = Describe("Cluster silence lifecycle", func() {
	const (
		clusterName = "silence-int-01"
		testNS      = "cluster-e2e-cluster-id"
	)

	AfterEach(func() {
		purgeResources()
		purgeDynamoTables()
		dynamoCli.ResetCache()
	})

	It("creates an installing silence for provisioning clusters and removes it when ready", func() {
		identity := silence.ClusterIdentity{Namespace: testNS, Name: clusterName}

		By("creating a Cluster CR")
		cluster := newTestCluster(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		By("setting cluster phase to Provisioning")
		Expect(updateClusterPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseProvisioning)).To(Succeed())

		By("waiting for an installing silence in Alertmanager")
		Eventually(func(g Gomega) {
			silences, err := silenceClient.List(ctx, identity)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(silences).To(HaveLen(1))
			g.Expect(silence.MatchesReason(silences[0], silence.ReasonInstalling)).To(BeTrue())
			g.Expect(silences[0].Status.State).To(Equal("active"))
		}).Should(Succeed())

		By("setting cluster phase to Ready")
		Expect(updateClusterPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseReady)).To(Succeed())

		By("waiting for silences to be expired")
		Eventually(func(g Gomega) {
			silences, err := silenceClient.List(ctx, identity)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(silences).To(BeEmpty())
		}).Should(Succeed())
	})

	It("creates a deleting silence when the cluster enters the deleting phase", func() {
		identity := silence.ClusterIdentity{Namespace: testNS, Name: clusterName}

		By("creating a provisioning cluster with an installing silence")
		cluster := newTestCluster(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		Expect(updateClusterPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseProvisioning)).To(Succeed())

		Eventually(func(g Gomega) {
			silences, err := silenceClient.List(ctx, identity)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(silences).To(HaveLen(1))
			g.Expect(silence.MatchesReason(silences[0], silence.ReasonInstalling)).To(BeTrue())
		}).Should(Succeed())

		By("setting cluster phase to Deleting")
		Expect(updateClusterPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseDeleting)).To(Succeed())

		By("waiting for a deleting silence in Alertmanager")
		Eventually(func(g Gomega) {
			silences, err := silenceClient.List(ctx, identity)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(silences).To(HaveLen(1))
			g.Expect(silence.MatchesReason(silences[0], silence.ReasonDeleting)).To(BeTrue())
		}).Should(Succeed())
	})
})

func updateClusterPhase(name, namespace string, phase hyperfleetv1alpha1.ClusterPhase) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var cluster hyperfleetv1alpha1.Cluster
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, &cluster); err != nil {
			return err
		}
		cluster.Status.Phase = phase
		return k8sClient.Status().Update(ctx, &cluster)
	})
}
