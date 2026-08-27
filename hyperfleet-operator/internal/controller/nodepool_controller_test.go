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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

var _ = Describe("NodePool Controller", func() {
	Context("When reconciling a NodePool", func() {
		const (
			clusterName  = "test-np-cluster"
			nodePoolName = "test-nodepool"
			testNS       = "cluster-test-cluster-id"
		)

		ctx := context.Background()

		BeforeEach(func() {
			ensureNamespace(ctx, testNS)
		})

		AfterEach(func() {
			np := &hyperfleetv1alpha1.NodePool{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: nodePoolName}, np); err == nil {
				controllerutil.RemoveFinalizer(np, nodePoolFinalizer)
				_ = k8sClient.Update(ctx, np)
				_ = k8sClient.Delete(ctx, np)
			}
			cluster := &hyperfleetv1alpha1.Cluster{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, cluster); err == nil {
				controllerutil.RemoveFinalizer(cluster, clusterFinalizer)
				_ = k8sClient.Update(ctx, cluster)
				_ = k8sClient.Delete(ctx, cluster)
			}
			placement := &hyperfleetv1alpha1.Placement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName + "-placement"}, placement); err == nil {
				_ = k8sClient.Delete(ctx, placement)
			}
		})

		It("should add a finalizer on first reconcile", func() {
			cluster := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			np := newTestNodePool()
			Expect(k8sClient.Create(ctx, np)).To(Succeed())

			reconciler := &NodePoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: &fakeDynamo{},
			}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			Expect(err).NotTo(HaveOccurred())
			// Finalizer write emits a watch event that re-enqueues; no explicit requeue needed.
			Expect(result.RequeueAfter).To(BeZero())

			var updated hyperfleetv1alpha1.NodePool
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: nodePoolName}, &updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(&updated, nodePoolFinalizer)).To(BeTrue())
		})

		It("should set WaitingForCluster when parent Cluster has no PlacementRef", func() {
			cluster := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			np := newTestNodePool()
			Expect(k8sClient.Create(ctx, np)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &NodePoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: fd,
			}

			// First reconcile: adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			// Second reconcile: cluster exists but no PlacementRef.
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())
			Expect(fd.applyCount).To(Equal(0))
		})

		It("should create ApplyDesire when parent Cluster has a Bound Placement", func() {
			cluster := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			// Set placementRef on the cluster status.
			cluster.Status.PlacementRef = &hyperfleetv1alpha1.PlacementReference{
				Name:              clusterName + "-placement",
				ManagementCluster: "mc01",
			}
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

			np := newTestNodePool()
			Expect(k8sClient.Create(ctx, np)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &NodePoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: fd,
			}

			// First reconcile: adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			// Second reconcile: creates desire.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(fd.applyCount).To(Equal(1))
		})

		It("should flip desire to Type=Delete in-place, wait for confirmation, and remove finalizer on deletion", func() {
			cluster := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			cluster.Status.PlacementRef = &hyperfleetv1alpha1.PlacementReference{
				Name:              clusterName + "-placement",
				ManagementCluster: "mc01",
			}
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

			np := newTestNodePool()
			Expect(k8sClient.Create(ctx, np)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &NodePoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: fd,
			}

			// Add finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})

			// Delete the NodePool.
			var toDelete hyperfleetv1alpha1.NodePool
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: nodePoolName}, &toDelete)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &toDelete)).To(Succeed())

			// First deletion reconcile: flips desire to Type=Delete in-place;
			// no status yet → requeues. No pre-deletion of ApplyDesire spec.
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(fd.deletedSpecs).To(BeEmpty(), "should not delete specs before deletion is confirmed")
			deleteApplies := filterDeleteDesires(fd.applies)
			Expect(deleteApplies).To(HaveLen(1))
			Expect(deleteApplies[0].Spec.TargetItem.Resource).To(Equal("nodepools"))
			// documentID must be the same as the SSA desire (same taskKey, no "+"-delete"" suffix).
			Expect(deleteApplies[0].DynamoDBMetadata.DocumentID).NotTo(BeEmpty())
			Expect(result.RequeueAfter).NotTo(BeZero(), "should requeue while waiting for confirmation")

			// Simulate kube-applier confirming deletion (Successful=True).
			fd.applyStatus = &dynamo.ApplyDesireStatus{
				Conditions: []metav1.Condition{{
					Type:   dynamo.DesireConditionSuccessful,
					Status: metav1.ConditionTrue,
					Reason: "NoErrors",
				}},
			}

			// Second deletion reconcile: confirmation found → cleans up specs and removes finalizer.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify NodePool is gone (finalizer removed → k8s deletes it).
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: nodePoolName}, &hyperfleetv1alpha1.NodePool{})
			Expect(err).To(HaveOccurred())

			// ApplyDesire spec and ReadDesire spec cleaned up after confirmation.
			applyCleanups, readCleanups := fd.countSpecCleanups()
			Expect(applyCleanups).To(Equal(1), "should clean up ApplyDesire spec after deletion confirmed")
			Expect(readCleanups).To(Equal(1), "should clean up ReadDesire spec after deletion confirmed")
		})

		It("should create ReadDesire alongside ApplyDesire", func() {
			cluster := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			cluster.Status.PlacementRef = &hyperfleetv1alpha1.PlacementReference{
				Name:              clusterName + "-placement",
				ManagementCluster: "mc01",
			}
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

			np := newTestNodePool()
			Expect(k8sClient.Create(ctx, np)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &NodePoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: fd,
			}

			// First reconcile: adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			// Second reconcile: creates desires.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(fd.applyCount).To(Equal(1))
			Expect(fd.readCount).To(Equal(1))
			Expect(fd.reads[0].Spec.TargetItem.Resource).To(Equal("nodepools"))
		})

		It("should set Phase=Ready when ReadDesire reports Ready=True", func() {
			cluster := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			cluster.Status.PlacementRef = &hyperfleetv1alpha1.PlacementReference{
				Name:              clusterName + "-placement",
				ManagementCluster: "mc01",
			}
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

			np := newTestNodePool()
			Expect(k8sClient.Create(ctx, np)).To(Succeed())

			fd := &fakeDynamo{
				applyStatus: &dynamo.ApplyDesireStatus{
					AppliedResourceGeneration: 1,
					Conditions: []metav1.Condition{{
						Type:   dynamo.DesireConditionSuccessful,
						Status: metav1.ConditionTrue,
						Reason: "NoErrors",
					}},
				},
				readStatus: &dynamo.ReadDesireStatus{
					KubeContent: &runtime.RawExtension{Raw: []byte(`{"status":{"conditions":[{"type":"Ready","status":"True","reason":"AsExpected","message":"All nodes ready","lastTransitionTime":"2026-06-25T10:00:00Z"}]}}`)},
				},
			}
			reconciler := &NodePoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: fd,
			}

			// First reconcile: adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			// Second reconcile: creates desires + reads status.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.NodePool
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: nodePoolName}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.NodePoolPhaseReady))
		})

		It("should clean up ApplyDesire and ReadDesire specs after deletion confirmed", func() {
			cluster := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

			cluster.Status.PlacementRef = &hyperfleetv1alpha1.PlacementReference{
				Name:              clusterName + "-placement",
				ManagementCluster: "mc01",
			}
			Expect(k8sClient.Status().Update(ctx, cluster)).To(Succeed())

			np := newTestNodePool()
			Expect(k8sClient.Create(ctx, np)).To(Succeed())

			fd := &fakeDynamo{
				applyStatus: &dynamo.ApplyDesireStatus{
					Conditions: []metav1.Condition{{
						Type:   dynamo.DesireConditionSuccessful,
						Status: metav1.ConditionTrue,
						Reason: "NoErrors",
					}},
				},
			}
			reconciler := &NodePoolReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: fd,
			}

			// Add finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})

			// Delete the NodePool.
			var toDelete hyperfleetv1alpha1.NodePool
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: nodePoolName}, &toDelete)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &toDelete)).To(Succeed())

			// Reconcile deletion: delete desire confirmed immediately (applyStatus pre-set).
			// Should flip desire, see Successful=True, then clean up both specs.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: nodePoolName},
			})
			Expect(err).NotTo(HaveOccurred())

			// 1 ApplyDesire cleanup + 1 ReadDesire cleanup, both after confirmation.
			applyCleanups, readCleanups := fd.countSpecCleanups()
			Expect(applyCleanups).To(Equal(1), "should clean up ApplyDesire spec")
			Expect(readCleanups).To(Equal(1), "should clean up ReadDesire spec")
		})
	})
})

func newTestNodePool() *hyperfleetv1alpha1.NodePool {
	return &hyperfleetv1alpha1.NodePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-nodepool",
			Namespace: "cluster-test-cluster-id",
		},
		Spec: hyperfleetv1alpha1.NodePoolSpec{
			NodePool: hyperfleetv1alpha1.NodePoolSpecPassthrough{
				ClusterName: "test-np-cluster",
				Replicas:    ptr.To(int32(2)),
				Management: hypershiftv1beta1.NodePoolManagement{
					AutoRepair:  true,
					UpgradeType: hypershiftv1beta1.UpgradeTypeReplace,
				},
				Release: hypershiftv1beta1.Release{
					Image: "quay.io/openshift-release-dev/ocp-release:4.17.0-ec.2-x86_64",
				},
				Platform: hypershiftv1beta1.NodePoolPlatform{
					Type: hypershiftv1beta1.AWSPlatform,
					AWS: &hypershiftv1beta1.AWSNodePoolPlatform{
						InstanceType:    "m6a.xlarge",
						RootVolume:      &hypershiftv1beta1.Volume{Size: 120, Type: "gp3"},
						InstanceProfile: "worker-profile",
						Subnet: hypershiftv1beta1.AWSResourceReference{
							ID: ptr.To("subnet-abc123"),
						},
						SecurityGroups: []hypershiftv1beta1.AWSResourceReference{
							{ID: ptr.To("sg-abc123")},
						},
					},
				},
			},
		},
	}
}
