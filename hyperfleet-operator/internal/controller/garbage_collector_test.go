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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

var _ = Describe("Garbage Collector", func() {
	const (
		testNS      = "account-123456789012"
		clusterName = "gc-cluster"
	)
	ctx := context.Background()

	gc := func() *GarbageCollector {
		return &GarbageCollector{Client: k8sClient, Scheme: k8sClient.Scheme(), Owned: &hyperfleetv1alpha1.NodePool{}}
	}
	collect := func(np *hyperfleetv1alpha1.NodePool) {
		_, err := gc().Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(np)})
		Expect(err).NotTo(HaveOccurred())
	}
	// gone reports whether np was deleted (or is being deleted).
	gone := func(np *hyperfleetv1alpha1.NodePool) bool {
		var latest hyperfleetv1alpha1.NodePool
		err := k8sClient.Get(ctx, client.ObjectKeyFromObject(np), &latest)
		if apierrors.IsNotFound(err) {
			return true
		}
		Expect(err).NotTo(HaveOccurred())
		return !latest.DeletionTimestamp.IsZero()
	}

	BeforeEach(func() {
		ensureNamespace(ctx, testNS)
	})

	AfterEach(func() {
		np := &hyperfleetv1alpha1.NodePool{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName + ".workers"}, np); err == nil {
			np.Finalizers = nil
			_ = k8sClient.Update(ctx, np)
			_ = k8sClient.Delete(ctx, np)
		}
		cluster := &hyperfleetv1alpha1.Cluster{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: clusterName}, cluster); err == nil {
			cluster.Finalizers = nil
			_ = k8sClient.Update(ctx, cluster)
			_ = k8sClient.Delete(ctx, cluster)
		}
	})

	It("keeps an object whose owner is alive", func() {
		cluster := newTestCluster(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		np := newTestNodePool(cluster)
		Expect(k8sClient.Create(ctx, np)).To(Succeed())

		collect(np)
		Expect(gone(np)).To(BeFalse())
	})

	It("deletes an object whose owner is missing", func() {
		missing := newTestCluster(clusterName)
		missing.UID = "00000000-0000-0000-0000-00000000aaaa"
		np := newTestNodePool(missing)
		Expect(k8sClient.Create(ctx, np)).To(Succeed())

		collect(np)
		Expect(gone(np)).To(BeTrue())
	})

	It("deletes an object pointing at an old uid under a reused name", func() {
		cluster := newTestCluster(clusterName)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		earlier := cluster.DeepCopy()
		earlier.UID = "00000000-0000-0000-0000-00000000bbbb"
		np := newTestNodePool(earlier)
		Expect(k8sClient.Create(ctx, np)).To(Succeed())

		collect(np)
		Expect(gone(np)).To(BeTrue())
		var still hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &still)).To(Succeed(), "the new owner is untouched")
	})

	It("deletes a child inserted while its owner is being deleted", func() {
		cluster := newTestCluster(clusterName)
		cluster.Finalizers = []string{clusterFinalizer}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())

		// The insert lands after the owner started deleting, as if its
		// finalizer had already found no children.
		np := newTestNodePool(cluster)
		Expect(k8sClient.Create(ctx, np)).To(Succeed())

		collect(np)
		Expect(gone(np)).To(BeTrue())
	})

	It("ignores objects without a controller owner", func() {
		cluster := newTestCluster(clusterName)
		np := newTestNodePool(cluster)
		np.OwnerReferences = nil
		Expect(k8sClient.Create(ctx, np)).To(Succeed())

		collect(np)
		Expect(gone(np)).To(BeFalse())
	})

	It("lets a dying cluster's finalizer go only once nothing carries its uid", func() {
		cluster := newTestCluster(clusterName)
		controllerutil.AddFinalizer(cluster, clusterFinalizer)
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
		np := newTestNodePool(cluster)
		Expect(k8sClient.Create(ctx, np)).To(Succeed())
		Expect(k8sClient.Delete(ctx, cluster)).To(Succeed())

		var dying hyperfleetv1alpha1.Cluster
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &dying)).To(Succeed())
		reconciler := &ClusterReconciler{Client: k8sClient, Scheme: k8sClient.Scheme(), Dynamo: &fakeDynamo{}}

		result, err := reconciler.cleanupAndRemoveFinalizer(ctx, &dying)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).NotTo(BeZero(), "a NodePool still carries the uid")

		collect(np)
		Expect(gone(np)).To(BeTrue())

		_, err = reconciler.cleanupAndRemoveFinalizer(ctx, &dying)
		Expect(err).NotTo(HaveOccurred())
		err = k8sClient.Get(ctx, client.ObjectKeyFromObject(cluster), &dying)
		Expect(apierrors.IsNotFound(err)).To(BeTrue(), "the finalizer should be removed")
	})
})
