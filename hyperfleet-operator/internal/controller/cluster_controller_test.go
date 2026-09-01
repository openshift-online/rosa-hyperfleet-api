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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/render"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
)

var _ = Describe("Cluster Controller", func() {
	Context("When reconciling a new Cluster", func() {
		const (
			clusterName = "test-cluster-01"
			testNS      = "cluster-test-cluster-id"
		)

		ctx := context.Background()

		BeforeEach(func() {
			ensureNamespace(ctx, testNS)
		})

		AfterEach(func() {
			resource := &hyperfleetv1alpha1.Cluster{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, resource)
			if err == nil {
				controllerutil.RemoveFinalizer(resource, clusterFinalizer)
				_ = k8sClient.Update(ctx, resource)
				_ = k8sClient.Delete(ctx, resource)
			}
			placement := &hyperfleetv1alpha1.Placement{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName + "-placement"}, placement); err == nil {
				_ = k8sClient.Delete(ctx, placement)
			}
		})

		It("should add a finalizer on first reconcile", func() {
			resource := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := &ClusterReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Dynamo:         &fakeDynamo{},
				RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
			}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())
			// Finalizer write emits a watch event that re-enqueues; no explicit requeue needed.
			Expect(result.RequeueAfter).To(BeZero())

			var updated hyperfleetv1alpha1.Cluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(&updated, clusterFinalizer)).To(BeTrue())
		})

		It("should set WaitingForPlacement when no Placement exists", func() {
			resource := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := &ClusterReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Dynamo:         &fakeDynamo{},
				RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
			}

			// First reconcile adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			// Second reconcile checks for Placement.
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())

			var updated hyperfleetv1alpha1.Cluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.ClusterPhaseWaitingForPlacement))
		})

		It("should create DynamoDB desires when Placement is Bound", func() {
			resource := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			// Create a Bound Placement.
			placement := &hyperfleetv1alpha1.Placement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName + "-placement",
					Namespace: testNS,
				},
				Spec: hyperfleetv1alpha1.PlacementSpec{
					ClusterName:       clusterName,
					ManagementCluster: "mc01",
				},
			}
			Expect(k8sClient.Create(ctx, placement)).To(Succeed())
			placement.Status.Phase = hyperfleetv1alpha1.PlacementPhaseBound
			Expect(k8sClient.Status().Update(ctx, placement)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ClusterReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Dynamo:         fd,
				RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
			}

			// First reconcile: adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			// Second reconcile: creates desires.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())

			// 7 cluster manifests → 7 ApplyDesires + 1 ReadDesire.
			Expect(fd.applyCount).To(Equal(7))
			Expect(fd.readCount).To(Equal(1))
		})

		It("should switch all 7 desires to Type=Delete in-place, wait for confirmation, then remove finalizer", func() {
			resource := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			// Create a Placement so the deletion path has something to clean up.
			placement := &hyperfleetv1alpha1.Placement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName + "-placement",
					Namespace: testNS,
				},
				Spec: hyperfleetv1alpha1.PlacementSpec{
					ClusterName:       clusterName,
					ManagementCluster: "mc01",
				},
			}
			Expect(k8sClient.Create(ctx, placement)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ClusterReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Dynamo:         fd,
				RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
			}

			// First reconcile: adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})

			// Set placementRef so the deletion path writes delete desires.
			var updated hyperfleetv1alpha1.Cluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &updated)).To(Succeed())
			updated.Status.PlacementRef = &hyperfleetv1alpha1.PlacementReference{
				Name:              clusterName + "-placement",
				ManagementCluster: "mc01",
			}
			Expect(k8sClient.Status().Update(ctx, &updated)).To(Succeed())

			// Delete the CR — sets DeletionTimestamp.
			Expect(k8sClient.Delete(ctx, &updated)).To(Succeed())

			// First deletion reconcile: switches all 7 desires to Type=Delete
			// in-place; no status yet → requeues.
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())
			deleteApplies := filterDeleteDesires(fd.applies)
			Expect(deleteApplies).To(HaveLen(7), "all 7 resources should be switched to Type=Delete")
			Expect(result.RequeueAfter).NotTo(BeZero(), "should requeue while waiting for deletion confirmation")

			// Placement should still exist (finalizer not removed yet).
			var p hyperfleetv1alpha1.Placement
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName + "-placement"}, &p)).To(Succeed())

			// Simulate kube-applier acknowledging the deletes but resources
			// still terminating (Successful=False, WaitingForDeletion).
			fd.applyStatus = &dynamo.ApplyDesireStatus{
				Conditions: []metav1.Condition{{
					Type:   dynamo.DesireConditionSuccessful,
					Status: metav1.ConditionFalse,
					Reason: "WaitingForDeletion",
				}},
			}

			// Second deletion reconcile: status exists but Successful!=True → requeues.
			result, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())
			deleteApplies = filterDeleteDesires(fd.applies)
			Expect(deleteApplies).To(HaveLen(14), "7 desires re-upserted on second pass")
			Expect(result.RequeueAfter).NotTo(BeZero(), "should requeue while resources still terminating")

			// Simulate all resources fully deleted (Successful=True).
			fd.applyStatus = &dynamo.ApplyDesireStatus{
				Conditions: []metav1.Condition{{
					Type:   dynamo.DesireConditionSuccessful,
					Status: metav1.ConditionTrue,
					Reason: "NoErrors",
				}},
			}

			// Third deletion reconcile: all 7 confirmed deleted → cleans up
			// desire specs and ReadDesire, deletes Placement, removes finalizer.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())
			deleteApplies = filterDeleteDesires(fd.applies)
			Expect(deleteApplies).To(HaveLen(21), "7 desires re-upserted on third pass")

			// Verify the Placement was deleted.
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName + "-placement"}, &p)
			Expect(err).To(HaveOccurred())

			// Verify desire specs were cleaned up once at the end (not on every pass).
			// 7 ApplyDesire cleanups + 1 ReadDesire cleanup.
			applyCleanups, readCleanups := fd.countSpecCleanups()
			Expect(applyCleanups).To(Equal(7), "should clean up all 7 ApplyDesire specs once deletion confirmed")
			Expect(readCleanups).To(Equal(1), "should clean up ReadDesire spec")
		})

		It("should propagate HC status feedback and set Phase=Ready", func() {
			resource := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			placement := &hyperfleetv1alpha1.Placement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName + "-placement",
					Namespace: testNS,
				},
				Spec: hyperfleetv1alpha1.PlacementSpec{
					ClusterName:       clusterName,
					ManagementCluster: "mc01",
				},
			}
			Expect(k8sClient.Create(ctx, placement)).To(Succeed())
			placement.Status.Phase = hyperfleetv1alpha1.PlacementPhaseBound
			Expect(k8sClient.Status().Update(ctx, placement)).To(Succeed())

			fd := &fakeDynamo{
				applyStatus: &dynamo.ApplyDesireStatus{
					Conditions: []metav1.Condition{{
						Type: dynamo.DesireConditionSuccessful, Status: metav1.ConditionTrue, Reason: "NoErrors",
					}},
				},
				readStatus: &dynamo.ReadDesireStatus{
					KubeContent: &runtime.RawExtension{Raw: []byte(`{
						"status": {
							"conditions": [
								{"type": "Available", "status": "True", "reason": "HostedClusterAsExpected", "lastTransitionTime": "2026-06-25T10:00:00Z"},
								{"type": "Degraded", "status": "False", "reason": "AsExpected", "lastTransitionTime": "2026-06-25T10:00:00Z"}
							],
							"version": {
								"history": [{"version": "4.17.0"}]
							},
							"controlPlaneEndpoint": {
								"host": "api.my-cluster.example.com",
								"port": 6443
							}
						}
					}`)},
				},
			}
			reconciler := &ClusterReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Dynamo:         fd,
				RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
			}

			// First reconcile: adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			// Second reconcile: creates desires + sets Provisioning.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())
			// Third reconcile: phase is already Provisioning so setPhase is
			// skipped and Ready from updateStatusFromDynamo persists.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.Cluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &updated)).To(Succeed())

			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.ClusterPhaseReady))
			Expect(updated.Status.Version).To(Equal("4.17.0"))
			Expect(updated.Status.ControlPlaneEndpoint.Host).To(Equal("api.my-cluster.example.com"))
			Expect(updated.Status.ControlPlaneEndpoint.Port).To(Equal(int32(6443)))

			availCond := meta.FindStatusCondition(updated.Status.Conditions, "Available")
			Expect(availCond).NotTo(BeNil())
			Expect(availCond.Status).To(Equal(metav1.ConditionTrue))

			degradedCond := meta.FindStatusCondition(updated.Status.Conditions, "Degraded")
			Expect(degradedCond).NotTo(BeNil())
			Expect(degradedCond.Status).To(Equal(metav1.ConditionFalse))
		})

		It("should not set Phase=Ready when cluster is Degraded", func() {
			resource := newTestCluster(clusterName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			placement := &hyperfleetv1alpha1.Placement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName + "-placement",
					Namespace: testNS,
				},
				Spec: hyperfleetv1alpha1.PlacementSpec{
					ClusterName:       clusterName,
					ManagementCluster: "mc01",
				},
			}
			Expect(k8sClient.Create(ctx, placement)).To(Succeed())
			placement.Status.Phase = hyperfleetv1alpha1.PlacementPhaseBound
			Expect(k8sClient.Status().Update(ctx, placement)).To(Succeed())

			fd := &fakeDynamo{
				applyStatus: &dynamo.ApplyDesireStatus{
					Conditions: []metav1.Condition{{
						Type: dynamo.DesireConditionSuccessful, Status: metav1.ConditionTrue, Reason: "NoErrors",
					}},
				},
				readStatus: &dynamo.ReadDesireStatus{
					KubeContent: &runtime.RawExtension{Raw: []byte(`{
						"status": {
							"conditions": [
								{"type": "Available", "status": "True", "reason": "HostedClusterAsExpected", "lastTransitionTime": "2026-06-25T10:00:00Z"},
								{"type": "Degraded", "status": "True", "reason": "ComponentFailing", "lastTransitionTime": "2026-06-25T10:00:00Z"}
							]
						}
					}`)},
				},
			}
			reconciler := &ClusterReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Dynamo:         fd,
				RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
			}

			// Finalizer + desires + third reconcile (same as Ready test).
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.Cluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).NotTo(Equal(hyperfleetv1alpha1.ClusterPhaseReady))
		})

		It("should delete an expired cluster", func() {
			resource := newTestCluster(clusterName)
			expiry := metav1.NewTime(time.Now().Add(-1 * time.Minute))
			resource.Spec.ExpirationTimestamp = &expiry
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := &ClusterReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Dynamo:         &fakeDynamo{},
				RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
			}

			// First reconcile adds finalizer.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Second reconcile detects expiration and deletes.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Cluster should have a DeletionTimestamp set.
			var updated hyperfleetv1alpha1.Cluster
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, &updated)).To(Succeed())
			Expect(updated.DeletionTimestamp.IsZero()).To(BeFalse())
		})

		It("should requeue at expiration time when it is sooner than statusRefreshDelay", func() {
			resource := newTestCluster(clusterName)
			expiry := metav1.NewTime(time.Now().Add(30 * time.Second))
			resource.Spec.ExpirationTimestamp = &expiry
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			placement := &hyperfleetv1alpha1.Placement{
				ObjectMeta: metav1.ObjectMeta{
					Name:      clusterName + "-placement",
					Namespace: testNS,
				},
				Spec: hyperfleetv1alpha1.PlacementSpec{
					ClusterName:       clusterName,
					ManagementCluster: "mc01",
				},
			}
			Expect(k8sClient.Create(ctx, placement)).To(Succeed())
			placement.Status.Phase = hyperfleetv1alpha1.PlacementPhaseBound
			Expect(k8sClient.Status().Update(ctx, placement)).To(Succeed())

			reconciler := &ClusterReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Dynamo:         &fakeDynamo{},
				RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
			}

			// First reconcile: adds finalizer.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())
			// Second reconcile: creates desires + returns requeue.
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(BeNumerically(">", 0))
			Expect(result.RequeueAfter).To(BeNumerically("<", statusRefreshDelay))
		})

		It("should handle not-found gracefully", func() {
			reconciler := &ClusterReconciler{
				Client:         k8sClient,
				Scheme:         k8sClient.Scheme(),
				Dynamo:         &fakeDynamo{},
				RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "does-not-exist"},
			})
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

var _ = Describe("DNS Reservation", func() {
	const (
		dnsClusterName = "dns-test-cluster"
		dnsTestNS      = "cluster-dns-test-id"
	)

	ctx := context.Background()

	newReconciler := func() *ClusterReconciler {
		return &ClusterReconciler{
			Client:         k8sClient,
			Scheme:         k8sClient.Scheme(),
			Dynamo:         &fakeDynamo{},
			RegionalConfig: render.RegionalConfig{BaseDomain: "example.com", AWSRegion: "us-east-1"},
		}
	}

	BeforeEach(func() {
		ensureNamespace(ctx, dnsTestNS)
	})

	AfterEach(func() {
		cluster := &hyperfleetv1alpha1.Cluster{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName}, cluster); err == nil {
			controllerutil.RemoveFinalizer(cluster, clusterFinalizer)
			_ = k8sClient.Update(ctx, cluster)
			_ = k8sClient.Delete(ctx, cluster)
		}

		// Clean up any DNSReservation resources created during the test.
		var dnsList hyperfleetv1alpha1.DNSReservationList
		if err := k8sClient.List(ctx, &dnsList); err == nil {
			for i := range dnsList.Items {
				_ = k8sClient.Delete(ctx, &dnsList.Items[i])
			}
		}

		placement := &hyperfleetv1alpha1.Placement{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName + "-placement"}, placement); err == nil {
			_ = k8sClient.Delete(ctx, placement)
		}
	})

	It("should reserve a DNS base domain and persist it in cluster status", func() {
		cluster := newTestClusterInNS(dnsClusterName, dnsTestNS)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		reconciler := newReconciler()

		baseDomain, err := reconciler.reserveDNS(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(baseDomain).To(MatchRegexp(`^[0-9a-f]{4}\.0\.example\.com$`))

		// Verify DNSReservation was created with correct spec.
		var dnsList hyperfleetv1alpha1.DNSReservationList
		Expect(k8sClient.List(ctx, &dnsList, client.MatchingLabels{
			"hyperfleet.io/cluster-namespace": dnsTestNS,
		})).To(Succeed())
		Expect(dnsList.Items).To(HaveLen(1))
		dnsRes := dnsList.Items[0]
		Expect(dnsRes.Spec.Shard).To(Equal("0"))
		Expect(dnsRes.Spec.Prefix).To(MatchRegexp(`^[0-9a-f]{4}$`))
		Expect(dnsRes.Spec.BaseDomain).To(Equal(baseDomain))
		Expect(dnsRes.Labels["hyperfleet.io/cluster-namespace"]).To(Equal(dnsTestNS))
		Expect(dnsRes.Labels["hyperfleet.io/account-id"]).To(Equal(cluster.Spec.AccountID))

		// Verify cluster status was updated.
		var updated hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName}, &updated)).To(Succeed())
		Expect(updated.Status.BaseDomain).To(Equal(baseDomain))
	})

	It("should return the existing base domain when the reservation already belongs to this cluster", func() {
		cluster := newTestClusterInNS(dnsClusterName, dnsTestNS)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		reconciler := newReconciler()

		// First reservation.
		bd1, err := reconciler.reserveDNS(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Calling again should find the existing reservation via the recovery path.
		var fresh hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName}, &fresh)).To(Succeed())
		fresh.Status.BaseDomain = ""
		Expect(k8sClient.Status().Update(ctx, &fresh)).To(Succeed())

		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName}, &fresh)).To(Succeed())
		bd2, err := reconciler.reserveDNS(ctx, &fresh)
		Expect(err).NotTo(HaveOccurred())
		Expect(bd2).To(Equal(bd1))

		// Verify only one DNSReservation exists for this cluster.
		var dnsList hyperfleetv1alpha1.DNSReservationList
		Expect(k8sClient.List(ctx, &dnsList)).To(Succeed())
		ownedCount := 0
		for _, d := range dnsList.Items {
			if d.Labels["hyperfleet.io/cluster-namespace"] == dnsTestNS {
				ownedCount++
			}
		}
		Expect(ownedCount).To(Equal(1))
	})

	It("should delete the DNS reservation during cluster cleanup", func() {
		cluster := newTestClusterInNS(dnsClusterName, dnsTestNS)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		reconciler := newReconciler()

		// Reserve DNS.
		baseDomain, err := reconciler.reserveDNS(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Verify the reservation exists.
		var dnsList hyperfleetv1alpha1.DNSReservationList
		Expect(k8sClient.List(ctx, &dnsList, client.MatchingLabels{
			"hyperfleet.io/cluster-namespace": dnsTestNS,
		})).To(Succeed())
		Expect(dnsList.Items).To(HaveLen(1))

		// Re-fetch the cluster (status was updated).
		var updated hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName}, &updated)).To(Succeed())
		Expect(updated.Status.BaseDomain).To(Equal(baseDomain))

		// Add finalizer so cleanupAndRemoveFinalizer has something to remove.
		controllerutil.AddFinalizer(&updated, clusterFinalizer)
		Expect(k8sClient.Update(ctx, &updated)).To(Succeed())

		// Re-fetch after finalizer update.
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName}, &updated)).To(Succeed())

		// Run cleanup.
		_, err = reconciler.cleanupAndRemoveFinalizer(ctx, &updated)
		Expect(err).NotTo(HaveOccurred())

		// Verify the DNS reservation was deleted.
		Expect(k8sClient.List(ctx, &dnsList, client.MatchingLabels{
			"hyperfleet.io/cluster-namespace": dnsTestNS,
		})).To(Succeed())
		Expect(dnsList.Items).To(BeEmpty(), "DNS reservation should be deleted")

		// Verify finalizer was removed.
		var final hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName}, &final)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(&final, clusterFinalizer)).To(BeFalse())
	})

	It("should retry and succeed when a prefix collides with another cluster's reservation", func() {
		cluster := newTestClusterInNS(dnsClusterName, dnsTestNS)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		reconciler := newReconciler()

		// Reserve DNS for our cluster first.
		baseDomain, err := reconciler.reserveDNS(ctx, cluster)
		Expect(err).NotTo(HaveOccurred())

		// Extract the prefix that was assigned.
		var dnsList hyperfleetv1alpha1.DNSReservationList
		Expect(k8sClient.List(ctx, &dnsList, client.MatchingLabels{
			"hyperfleet.io/cluster-namespace": dnsTestNS,
		})).To(Succeed())
		Expect(dnsList.Items).To(HaveLen(1))
		Expect(dnsList.Items[0].Spec.BaseDomain).To(Equal(baseDomain))

		// Create a second cluster in a different namespace.
		const otherNS = "cluster-other-dns-test"
		ensureNamespace(ctx, otherNS)
		otherCluster := newTestClusterInNS("other-dns-cluster", otherNS)
		Expect(k8sClient.Create(ctx, otherCluster)).To(Succeed())

		// Reserve DNS for the second cluster — it must succeed with a different reservation.
		otherBaseDomain, err := reconciler.reserveDNS(ctx, otherCluster)
		Expect(err).NotTo(HaveOccurred())
		Expect(otherBaseDomain).To(MatchRegexp(`^[0-9a-f]{4}\.0\.example\.com$`))

		// Verify the second cluster got its own reservation.
		var otherDNS hyperfleetv1alpha1.DNSReservationList
		Expect(k8sClient.List(ctx, &otherDNS, client.MatchingLabels{
			"hyperfleet.io/cluster-namespace": otherNS,
		})).To(Succeed())
		Expect(otherDNS.Items).To(HaveLen(1))
		Expect(otherDNS.Items[0].Spec.BaseDomain).To(Equal(otherBaseDomain))

		// Clean up the second cluster's resources.
		_ = k8sClient.Delete(ctx, otherCluster)
		for i := range otherDNS.Items {
			_ = k8sClient.Delete(ctx, &otherDNS.Items[i])
		}
	})

	It("should skip DNS cleanup when no reservation exists", func() {
		cluster := newTestClusterInNS(dnsClusterName, dnsTestNS)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())

		// Add finalizer without reserving DNS.
		controllerutil.AddFinalizer(cluster, clusterFinalizer)
		Expect(k8sClient.Update(ctx, cluster)).To(Succeed())

		var updated hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName}, &updated)).To(Succeed())
		Expect(updated.Status.BaseDomain).To(BeEmpty())

		reconciler := newReconciler()
		_, err := reconciler.cleanupAndRemoveFinalizer(ctx, &updated)
		Expect(err).NotTo(HaveOccurred())

		// Finalizer should still be removed.
		var final hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: dnsTestNS, Name: dnsClusterName}, &final)).To(Succeed())
		Expect(controllerutil.ContainsFinalizer(&final, clusterFinalizer)).To(BeFalse())
	})
})

func newTestClusterInNS(name, ns string) *hyperfleetv1alpha1.Cluster {
	c := newTestCluster(name)
	c.Namespace = ns
	return c
}

func mustParseCIDR(s string) ipnet.IPNet {
	parsed, err := ipnet.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return *parsed
}

func newTestCluster(name string) *hyperfleetv1alpha1.Cluster {
	return &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "cluster-test-cluster-id",
		},
		Spec: hyperfleetv1alpha1.ClusterSpec{
			AccountID:  "123456789012",
			CreatorARN: "arn:aws:iam::123456789012:user/admin",
			HostedCluster: hyperfleetv1alpha1.HostedClusterSpecPassthrough{
				Release:    hypershiftv1beta1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.17.0-ec.2-x86_64"},
				IssuerURL:  "https://oidc.example.com/cluster-01",
				PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
				Networking: hypershiftv1beta1.ClusterNetworking{
					ClusterNetwork: []hypershiftv1beta1.ClusterNetworkEntry{{CIDR: mustParseCIDR("10.128.0.0/14")}},
					ServiceNetwork: []hypershiftv1beta1.ServiceNetworkEntry{{CIDR: mustParseCIDR("172.30.0.0/16")}},
					MachineNetwork: []hypershiftv1beta1.MachineNetworkEntry{{CIDR: mustParseCIDR("10.0.0.0/16")}},
				},
				Etcd: hypershiftv1beta1.EtcdSpec{
					ManagementType: hypershiftv1beta1.Managed,
					Managed: &hypershiftv1beta1.ManagedEtcdSpec{
						Storage: hypershiftv1beta1.ManagedEtcdStorageSpec{
							Type: hypershiftv1beta1.PersistentVolumeEtcdStorage,
						},
					},
				},
				Services: []hypershiftv1beta1.ServicePublishingStrategyMapping{
					{Service: hypershiftv1beta1.APIServer, ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{Type: hypershiftv1beta1.Route}},
					{Service: hypershiftv1beta1.OAuthServer, ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{Type: hypershiftv1beta1.Route}},
					{Service: hypershiftv1beta1.Konnectivity, ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{Type: hypershiftv1beta1.Route}},
					{Service: hypershiftv1beta1.Ignition, ServicePublishingStrategy: hypershiftv1beta1.ServicePublishingStrategy{Type: hypershiftv1beta1.Route}},
				},
				Platform: hypershiftv1beta1.PlatformSpec{
					Type: hypershiftv1beta1.AWSPlatform,
					AWS: &hypershiftv1beta1.AWSPlatformSpec{
						Region: "us-east-1",
						CloudProviderConfig: &hypershiftv1beta1.AWSCloudProviderConfig{
							VPC:  "vpc-abc123",
							Zone: "us-east-1a",
							Subnet: &hypershiftv1beta1.AWSResourceReference{
								ID: ptr.To("subnet-1,subnet-2"),
							},
						},
						RolesRef: hypershiftv1beta1.AWSRolesRef{
							ControlPlaneOperatorARN: "arn:aws:iam::123456789012:role/cpo",
							IngressARN:              "arn:aws:iam::123456789012:role/ingress",
							ImageRegistryARN:        "arn:aws:iam::123456789012:role/registry",
							KubeCloudControllerARN:  "arn:aws:iam::123456789012:role/kccm",
							NodePoolManagementARN:   "arn:aws:iam::123456789012:role/npm",
							NetworkARN:              "arn:aws:iam::123456789012:role/network",
							StorageARN:              "arn:aws:iam::123456789012:role/storage",
						},
					},
				},
			},
		},
	}
}

// filterDeleteDesires returns ApplyDesires that have Type=Delete.
func filterDeleteDesires(applies []*dynamo.ApplyDesire) []*dynamo.ApplyDesire {
	var out []*dynamo.ApplyDesire
	for _, a := range applies {
		if a.Spec.Type == dynamo.ApplyDesireTypeDelete {
			out = append(out, a)
		}
	}
	return out
}
