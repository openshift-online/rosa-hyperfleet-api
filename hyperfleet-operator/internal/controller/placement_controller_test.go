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

package controller

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

var _ = Describe("Placement Controller", func() {
	Context("When reconciling a Cluster", func() {
		const (
			clusterName = "test-placement-cl"
			testNS      = "account-123456789012"
		)

		ctx := context.Background()

		const mcName = "mc01"

		BeforeEach(func() {
			ensureNamespace(ctx, testNS)
			mc := &hyperfleetv1alpha1.ManagementCluster{
				ObjectMeta: metav1.ObjectMeta{Name: mcName},
				Spec: hyperfleetv1alpha1.ManagementClusterSpec{
					Region:    "us-east-1",
					AccountID: "123456789012",
				},
			}
			err := k8sClient.Create(ctx, mc)
			if err != nil {
				Expect(err.Error()).To(ContainSubstring("already exists"))
			}
		})

		AfterEach(func() {
			cluster := &hyperfleetv1alpha1.Cluster{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, cluster); err == nil {
				controllerutil.RemoveFinalizer(cluster, clusterFinalizer)
				_ = k8sClient.Update(ctx, cluster)
				_ = k8sClient.Delete(ctx, cluster)
			}
			placement := &hyperfleetv1alpha1.Placement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName + ".placement"}, placement); err == nil {
				_ = k8sClient.Delete(ctx, placement)
			}
		})

		It("should create a Placement for a new Cluster", func() {
			resource := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := &PlacementReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())

			var placement hyperfleetv1alpha1.Placement
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName + ".placement"}, &placement)).To(Succeed())
			Expect(placement.Spec.ClusterName).To(Equal(clusterName))
			Expect(placement.Spec.ManagementCluster).To(Equal(mcName))
			Expect(placement.Status.Phase).To(Equal(hyperfleetv1alpha1.PlacementPhaseBound))

			// Owned by the cluster: a controller ownerReference and the uid label.
			Expect(metav1.IsControlledBy(&placement, resource)).To(BeTrue())
			Expect(placement.Labels[hyperfleetv1alpha1.ClusterUIDLabel]).To(Equal(string(resource.UID)))
		})

		It("should replace a Placement left by an earlier cluster with the same name", func() {
			resource := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			earlier := resource.DeepCopy()
			earlier.UID = "00000000-0000-0000-0000-000000000003"
			stale := createPlacement(ctx, earlier, "old-mc", true)

			reconciler := &PlacementReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}
			req := reconcile.Request{NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName}}

			// First pass deletes the stale Placement; the next creates ours.
			_, err := reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, req)
			Expect(err).NotTo(HaveOccurred())

			var placement hyperfleetv1alpha1.Placement
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName + ".placement"}, &placement)).To(Succeed())
			Expect(placement.UID).NotTo(Equal(stale.UID))
			Expect(metav1.IsControlledBy(&placement, resource)).To(BeTrue())
			Expect(placement.Spec.ManagementCluster).To(Equal(mcName))
		})

		It("should update Cluster status with placementRef", func() {
			resource := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := &PlacementReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())

			var cluster hyperfleetv1alpha1.Cluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &cluster)).To(Succeed())
			Expect(cluster.Status.PlacementRef).NotTo(BeNil())
			Expect(cluster.Status.PlacementRef.ManagementCluster).To(Equal(mcName))
		})
	})
})
