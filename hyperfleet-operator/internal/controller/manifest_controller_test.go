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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo"
)

var _ = Describe("Manifest Controller", func() {
	Context("When reconciling a Manifest (ZOA deploy)", func() {
		const (
			manifestName = "test-monitoring"
			testNS       = "mc01"
		)

		ctx := context.Background()

		BeforeEach(func() {
			ensureNamespace(ctx, testNS)
		})

		AfterEach(func() {
			resource := &hyperfleetv1alpha1.Manifest{}
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, resource)
			if err == nil {
				controllerutil.RemoveFinalizer(resource, manifestFinalizer)
				_ = k8sClient.Update(ctx, resource)
				_ = k8sClient.Delete(ctx, resource)
			}
			// Clean up the second manifest used in the collision test.
			other := &hyperfleetv1alpha1.Manifest{}
			if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "test-monitoring-b"}, other); err == nil {
				controllerutil.RemoveFinalizer(other, manifestFinalizer)
				_ = k8sClient.Update(ctx, other)
				_ = k8sClient.Delete(ctx, other)
			}
		})

		It("should add a finalizer on first reconcile", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			reconciler := &ManifestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: &fakeDynamo{},
			}

			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())
			// Finalizer write emits a watch event that re-enqueues; no explicit requeue needed.
			Expect(result.RequeueAfter).To(BeZero())

			var updated hyperfleetv1alpha1.Manifest
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(&updated, manifestFinalizer)).To(BeTrue())
		})

		It("should write ApplyDesires for each resource", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ManifestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: fd,
			}

			// First reconcile: adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			// Second reconcile: writes desires.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			// 4 resources → 4 ApplyDesires (SA, Role, RoleBinding, Job).
			Expect(fd.applyCount).To(Equal(4))

			sa := fd.findApply("serviceaccounts", "zoa-runner")
			Expect(sa).NotTo(BeNil())
			Expect(sa.Spec.TargetItem.Version).To(Equal("v1"))

			role := fd.findApply("roles", "zoa-runner")
			Expect(role).NotTo(BeNil())
			Expect(role.Spec.TargetItem.Version).To(Equal("v1"))
			Expect(role.Spec.TargetItem.Group).To(Equal("rbac.authorization.k8s.io"))

			job := fd.findApply("jobs", "collect-logs-abc123")
			Expect(job).NotTo(BeNil())
			Expect(job.Spec.TargetItem.Namespace).To(Equal("zoa-actions"))

			// Verify KubeContent is the raw JSON from spec.
			for _, a := range fd.applies {
				Expect(a.Spec.ServerSideApply).NotTo(BeNil())
				Expect(a.Spec.ServerSideApply.KubeContent).NotTo(BeNil())
				Expect(a.Spec.ServerSideApply.KubeContent.Raw).NotTo(BeEmpty())
			}
		})

		It("should set status after writing desires", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ManifestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: fd,
			}

			// First reconcile: finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			// Second reconcile: desires + status.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.Manifest
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.ManifestPhaseSyncing))
			Expect(updated.Status.AppliedResources).To(Equal(int32(4)))
			cond := meta.FindStatusCondition(updated.Status.Conditions, ConditionSynced)
			Expect(cond).NotTo(BeNil())
			Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			Expect(cond.Reason).To(Equal("AwaitingSync"))
		})

		It("should scope document IDs to the CR identity", func() {
			// Two different Manifest CRs deploying the same resource
			// must produce different document IDs to avoid DynamoDB overwrites.
			hfmA := newTestManifest(manifestName)
			hfmB := newTestManifest("test-monitoring-b")
			Expect(k8sClient.Create(ctx, hfmA)).To(Succeed())
			Expect(k8sClient.Create(ctx, hfmB)).To(Succeed())

			fdA := &fakeDynamo{}
			fdB := &fakeDynamo{}

			reconcilerA := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fdA,
			}
			reconcilerB := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fdB,
			}

			// Reconcile both (finalizer + desires).
			_, _ = reconcilerA.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			_, _ = reconcilerA.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			_, _ = reconcilerB.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "test-monitoring-b"},
			})
			_, _ = reconcilerB.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "test-monitoring-b"},
			})

			Expect(fdA.applyCount).To(Equal(4))
			Expect(fdB.applyCount).To(Equal(4))

			// The document IDs for the same resource (SA "zoa-runner") must differ between CRs.
			saA := fdA.findApply("serviceaccounts", "zoa-runner")
			saB := fdB.findApply("serviceaccounts", "zoa-runner")
			Expect(saA).NotTo(BeNil())
			Expect(saB).NotTo(BeNil())
			docIDA := saA.DocumentID
			docIDB := saB.DocumentID
			Expect(docIDA).NotTo(Equal(docIDB), "document IDs should differ between CRs")

			expectedA := dynamo.NewDocumentID("hyperfleet-manifest/"+testNS+"/"+manifestName, "", "v1", "serviceaccounts", "zoa-actions", "zoa-runner")
			expectedB := dynamo.NewDocumentID("hyperfleet-manifest/"+testNS+"/test-monitoring-b", "", "v1", "serviceaccounts", "zoa-actions", "zoa-runner")
			Expect(docIDA).To(Equal(expectedA))
			Expect(docIDB).To(Equal(expectedB))
		})

		It("should flip desires to Type=Delete in-place and requeue when waiting for confirmation", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
			}

			// Add finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})

			// Delete the CR.
			var toDelete hyperfleetv1alpha1.Manifest
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &toDelete)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &toDelete)).To(Succeed())

			// Deletion reconcile: flips all 4 desires to Type=Delete in-place;
			// no status yet → requeues. No pre-deletion of ApplyDesire specs.
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			// No ApplyDesire specs deleted yet — they are flipped in-place, not removed.
			Expect(fd.deletedSpecs).To(BeEmpty(), "should not delete specs before deletion is confirmed")

			// All 4 desires must have been upserted as Type=Delete with the same
			// documentIDs as the original SSA desires (same taskKey, no "-delete" suffix).
			deleteApplies := filterDeleteDesires(fd.applies)
			Expect(deleteApplies).To(HaveLen(4), "all 4 resources should be flipped to Type=Delete")
			resources := make([]string, len(deleteApplies))
			for i, a := range deleteApplies {
				resources[i] = a.Spec.TargetItem.Resource
			}
			Expect(resources).To(ConsistOf("serviceaccounts", "roles", "rolebindings", "jobs"))

			// Document IDs must match those written during create (same taskKey).
			taskKey := "hyperfleet-manifest/" + testNS + "/" + manifestName
			expectedSADocID := dynamo.NewDocumentID(taskKey, "", "v1", "serviceaccounts", "zoa-actions", "zoa-runner")
			found := false
			for _, a := range deleteApplies {
				if a.DocumentID == expectedSADocID {
					found = true
					break
				}
			}
			Expect(found).To(BeTrue(), "delete desire for SA should have same documentID as SSA desire")

			Expect(result.RequeueAfter).NotTo(BeZero(), "should requeue while waiting for confirmation")
		})

		It("should remove finalizer after all desires confirmed deleted, then clean up specs", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
			}

			// Add finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})

			// Delete the CR.
			var toDelete hyperfleetv1alpha1.Manifest
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &toDelete)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &toDelete)).To(Succeed())

			// First deletion pass: flips desires, no status → requeues.
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())
			Expect(fd.deletedSpecs).To(BeEmpty(), "no specs deleted while waiting")

			// Simulate all desires confirmed deleted (Successful=True).
			fd.applyStatus = &dynamo.ApplyDesireStatus{
				Conditions: []metav1.Condition{{
					Type:   dynamo.DesireConditionSuccessful,
					Status: metav1.ConditionTrue,
					Reason: "NoErrors",
				}},
			}

			// Second deletion pass: all confirmed → cleans up specs and removes finalizer.
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify CR is gone (finalizer removed).
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &hyperfleetv1alpha1.Manifest{})
			Expect(err).To(HaveOccurred())

			// Verify ApplyDesire specs were cleaned up (4) and ReadDesire spec
			// for the watched Job (1) — only after confirmation, not before.
			applyCleanups, readCleanups := fd.countSpecCleanups()
			Expect(applyCleanups).To(Equal(4), "should clean up all 4 ApplyDesire specs after deletion confirmed")
			Expect(readCleanups).To(Equal(1), "should clean up ReadDesire spec for watched Job")
		})

		It("should error when Content is missing apiVersion", func() {
			resource := &hyperfleetv1alpha1.Manifest{
				ObjectMeta: metav1.ObjectMeta{Name: manifestName, Namespace: testNS},
				Spec: hyperfleetv1alpha1.ManifestSpec{
					ManagementCluster: "mc01",
					Resources: []hyperfleetv1alpha1.ResourceTemplate{{
						Resource: "configmaps",
						Content:  runtime.RawExtension{Raw: []byte(`{"kind":"ConfigMap","metadata":{"name":"test"}}`)},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
			}

			// First reconcile: adds finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			// Second reconcile: should fail on extractResourceMeta.
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("content missing apiVersion"))
			Expect(fd.applyCount).To(Equal(0))
		})

		It("should error when Content is missing metadata.name", func() {
			resource := &hyperfleetv1alpha1.Manifest{
				ObjectMeta: metav1.ObjectMeta{Name: manifestName, Namespace: testNS},
				Spec: hyperfleetv1alpha1.ManifestSpec{
					ManagementCluster: "mc01",
					Resources: []hyperfleetv1alpha1.ResourceTemplate{{
						Resource: "configmaps",
						Content:  runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{}}`)},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
			}

			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("content missing metadata.name"))
			Expect(fd.applyCount).To(Equal(0))
		})

		It("should write ReadDesires for watched resources", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
			}

			// Finalizer + desires.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Only the Job has watch: true → 1 ReadDesire.
			Expect(fd.readCount).To(Equal(1))
			Expect(fd.reads[0].Spec.TargetItem.Resource).To(Equal("jobs"))
			Expect(fd.reads[0].Spec.TargetItem.Name).To(Equal("collect-logs-abc123"))
			Expect(fd.reads[0].Spec.TargetItem.Namespace).To(Equal("zoa-actions"))
			Expect(fd.reads[0].Spec.TargetItem.Group).To(Equal("batch"))
			Expect(fd.reads[0].Spec.TargetItem.Version).To(Equal("v1"))

			// Should requeue for status polling.
			Expect(result.RequeueAfter).NotTo(BeZero(), "should requeue when watched resources exist")
		})

		It("should populate resourceStatuses from ReadDesire feedback", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			kubeContent := []byte(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"collect-logs-abc123","namespace":"zoa-actions"},"status":{"succeeded":1,"startTime":"2026-06-25T10:00:00Z","completionTime":"2026-06-25T10:00:05Z"}}`)
			fd := &fakeDynamo{
				readStatus: &dynamo.ReadDesireStatus{
					KubeContent: &runtime.RawExtension{Raw: kubeContent},
				},
			}
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
			}

			// Finalizer + desires.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.Manifest
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &updated)).To(Succeed())
			Expect(updated.Status.ResourceStatuses).To(HaveLen(1))
			Expect(updated.Status.ResourceStatuses[0].Resource).To(Equal("jobs"))
			Expect(updated.Status.ResourceStatuses[0].Name).To(Equal("collect-logs-abc123"))
			Expect(updated.Status.ResourceStatuses[0].Namespace).To(Equal("zoa-actions"))
			Expect(updated.Status.ResourceStatuses[0].Status.Raw).To(MatchJSON(`{"succeeded":1,"startTime":"2026-06-25T10:00:00Z","completionTime":"2026-06-25T10:00:05Z"}`))
		})

		It("should not write ReadDesires for unwatched resources", func() {
			resource := newTestManifestUnwatched(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
			}

			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			result, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(fd.readCount).To(Equal(0))
			Expect(result.RequeueAfter).To(Equal(15*time.Second), "should requeue to poll Synced status")
		})

		It("should handle not-found gracefully", func() {
			reconciler := &ManifestReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
				Dynamo: &fakeDynamo{},
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "does-not-exist"},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should populate EventRouter for watched resources", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			er := NewEventRouter()
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
				StatusEvents: make(chan event.GenericEvent, 256),
				EventRouter:  er,
			}

			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			readDocID := dynamo.NewDocumentID(
				"hyperfleet-manifest/"+testNS+"/"+manifestName+"-read",
				"batch", "v1", "jobs", "zoa-actions", "collect-logs-abc123",
			)
			target, ok := er.Lookup(readDocID)
			Expect(ok).To(BeTrue(), "EventRouter should contain entry for watched resource")
			Expect(target.Key.Namespace).To(Equal(testNS))
			Expect(target.Key.Name).To(Equal(manifestName))
		})

		It("should set Deleting phase on deletion", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
			}

			// Add finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})

			// Delete the CR.
			var toDelete hyperfleetv1alpha1.Manifest
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &toDelete)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &toDelete)).To(Succeed())

			// Reconcile deletion (no confirmation — requeues).
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.Manifest
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.ManifestPhaseDeleting))
		})

		It("should clean up orphaned ApplyDesire and ReadDesire specs when resources are removed", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{
				readStatus: &dynamo.ReadDesireStatus{
					KubeContent: &runtime.RawExtension{Raw: []byte(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"collect-logs-abc123","namespace":"zoa-actions"},"status":{"succeeded":1}}`)},
				},
			}
			er := NewEventRouter()
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
				StatusEvents: make(chan event.GenericEvent, 256),
				EventRouter:  er,
			}

			// Add finalizer.
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			// Write desires + populate ResourceStatuses (generation 1).
			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			// Verify initial state: 4 ApplyDesires, 1 ReadDesire, 1 ResourceStatus.
			Expect(fd.applyCount).To(Equal(4))
			Expect(fd.readCount).To(Equal(1))
			var initial hyperfleetv1alpha1.Manifest
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &initial)).To(Succeed())
			Expect(initial.Status.ResourceStatuses).To(HaveLen(1))
			Expect(initial.Status.ResourceStatuses[0].Resource).To(Equal("jobs"))
			Expect(initial.Status.ResourceStatuses[0].Group).To(Equal("batch"))
			Expect(initial.Status.ResourceStatuses[0].Version).To(Equal("v1"))

			// Verify the watched resource's ReadDesire is registered in EventRouter.
			readDocID := dynamo.NewDocumentID(
				"hyperfleet-manifest/"+testNS+"/"+manifestName+"-read",
				"batch", "v1", "jobs", "zoa-actions", "collect-logs-abc123",
			)
			_, ok := er.Lookup(readDocID)
			Expect(ok).To(BeTrue())

			// Now update the Manifest to remove the watched Job resource,
			// keeping only the SA and Role.
			initial.Spec.Resources = []hyperfleetv1alpha1.ResourceTemplate{
				{
					Resource: "serviceaccounts",
					Content:  runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"zoa-runner","namespace":"zoa-actions"}}`)},
				},
				{
					Resource: "roles",
					Content:  runtime.RawExtension{Raw: []byte(`{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"Role","metadata":{"name":"zoa-runner","namespace":"zoa-actions"},"rules":[{"apiGroups":[""],"resources":["pods/log"],"verbs":["get"]}]}`)},
				},
			}
			Expect(k8sClient.Update(ctx, &initial)).To(Succeed())

			// Reset counters for the next reconcile.
			fd.mu.Lock()
			fd.deletedSpecs = nil
			fd.mu.Unlock()

			// Reconcile with the updated spec (generation 2).
			_, err = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			// The Job was in ResourceStatuses (watched), so orphan cleanup should
			// delete both the ApplyDesire and ReadDesire specs.
			applyCleanups, readCleanups := fd.countSpecCleanups()
			Expect(applyCleanups).To(BeNumerically(">=", 1), "should clean up orphaned ApplyDesire for removed Job")
			Expect(readCleanups).To(BeNumerically(">=", 1), "should clean up orphaned ReadDesire for removed Job")

			// EventRouter should be deregistered for the orphaned ReadDesire.
			_, ok = er.Lookup(readDocID)
			Expect(ok).To(BeFalse(), "EventRouter should deregister orphaned ReadDesire")
		})

		It("should clean up EventRouter on deletion", func() {
			resource := newTestManifest(manifestName)
			Expect(k8sClient.Create(ctx, resource)).To(Succeed())

			fd := &fakeDynamo{}
			er := NewEventRouter()
			reconciler := &ManifestReconciler{
				Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: fd,
				StatusEvents: make(chan event.GenericEvent, 256),
				EventRouter:  er,
			}

			// Finalizer + desires (populates EventRouter).
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			_, _ = reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})

			readDocID := dynamo.NewDocumentID(
				"hyperfleet-manifest/"+testNS+"/"+manifestName+"-read",
				"batch", "v1", "jobs", "zoa-actions", "collect-logs-abc123",
			)
			_, ok := er.Lookup(readDocID)
			Expect(ok).To(BeTrue(), "EventRouter should be populated before deletion")

			// Delete the CR.
			var toDelete hyperfleetv1alpha1.Manifest
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: manifestName}, &toDelete)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &toDelete)).To(Succeed())

			// Simulate all ApplyDesires (Type=Delete) confirmed (Successful=True) so finalizer is removed.
			fd.applyStatus = &dynamo.ApplyDesireStatus{
				Conditions: []metav1.Condition{{
					Type:   dynamo.DesireConditionSuccessful,
					Status: metav1.ConditionTrue,
					Reason: "NoErrors",
				}},
			}

			_, err := reconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: manifestName},
			})
			Expect(err).NotTo(HaveOccurred())

			_, ok = er.Lookup(readDocID)
			Expect(ok).To(BeFalse(), "EventRouter should be cleaned up after deletion")
		})
	})
})

// newTestManifest models a ZOA trusted action: deploy a ServiceAccount, Role,
// RoleBinding, and a runner Job to an MC. The Job is watched so the platform-api
// can detect completion via status.resourceStatuses.
func newTestManifest(name string) *hyperfleetv1alpha1.Manifest {
	return &hyperfleetv1alpha1.Manifest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "mc01",
		},
		Spec: hyperfleetv1alpha1.ManifestSpec{
			ManagementCluster: "mc01",
			Resources: []hyperfleetv1alpha1.ResourceTemplate{
				{
					Resource: "serviceaccounts",
					Content:  runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ServiceAccount","metadata":{"name":"zoa-runner","namespace":"zoa-actions"}}`)},
				},
				{
					Resource: "roles",
					Content:  runtime.RawExtension{Raw: []byte(`{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"Role","metadata":{"name":"zoa-runner","namespace":"zoa-actions"},"rules":[{"apiGroups":[""],"resources":["pods/log"],"verbs":["get"]}]}`)},
				},
				{
					Resource: "rolebindings",
					Content:  runtime.RawExtension{Raw: []byte(`{"apiVersion":"rbac.authorization.k8s.io/v1","kind":"RoleBinding","metadata":{"name":"zoa-runner","namespace":"zoa-actions"},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"Role","name":"zoa-runner"},"subjects":[{"kind":"ServiceAccount","name":"zoa-runner","namespace":"zoa-actions"}]}`)},
				},
				{
					Resource: "jobs",
					Content:  runtime.RawExtension{Raw: []byte(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"collect-logs-abc123","namespace":"zoa-actions"},"spec":{"template":{"spec":{"serviceAccountName":"zoa-runner","containers":[{"name":"runner","image":"registry.example.com/zoa-runner:latest"}],"restartPolicy":"Never"}}}}`)},
					Watch:    true,
				},
			},
		},
	}
}

// newTestManifestUnwatched models a pure infrastructure deploy (no status feedback).
func newTestManifestUnwatched(name string) *hyperfleetv1alpha1.Manifest {
	return &hyperfleetv1alpha1.Manifest{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "mc01",
		},
		Spec: hyperfleetv1alpha1.ManifestSpec{
			ManagementCluster: "mc01",
			Resources: []hyperfleetv1alpha1.ResourceTemplate{
				{
					Resource: "namespaces",
					Content:  runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"monitoring"}}`)},
				},
				{
					Resource: "configmaps",
					Content:  runtime.RawExtension{Raw: []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"prometheus-config","namespace":"monitoring"},"data":{"scrape.yml":"global: {}"}}`)},
				},
			},
		},
	}
}
