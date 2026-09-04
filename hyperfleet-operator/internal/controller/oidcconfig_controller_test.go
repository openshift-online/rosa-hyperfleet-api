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
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

type fakeOidcInfra struct {
	mu sync.Mutex

	thumbprint string

	storeErr             error
	existsErr            error
	readCrossAccountErr  error
	deleteKeyErr         error
	computeThumbprintErr error

	keyExists       bool
	crossAccountKey []byte

	storeCalled             int
	existsCalled            int
	readCalled              int
	deleteKeyCalled         int
	computeThumbprintCalled int

	// lastAccountID records the accountID passed to the most recent
	// StorePrivateKey/PrivateKeyExists/DeletePrivateKey call.
	lastAccountID string
}

func (f *fakeOidcInfra) StorePrivateKey(_ context.Context, accountID, _ string, _ []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.storeCalled++
	f.lastAccountID = accountID
	return f.storeErr
}

func (f *fakeOidcInfra) PrivateKeyExists(_ context.Context, accountID, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.existsCalled++
	f.lastAccountID = accountID
	return f.keyExists, f.existsErr
}

func (f *fakeOidcInfra) ReadCrossAccountSecret(_ context.Context, _, _ string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.readCalled++
	if f.readCrossAccountErr != nil {
		return nil, f.readCrossAccountErr
	}
	return f.crossAccountKey, nil
}

func (f *fakeOidcInfra) DeletePrivateKey(_ context.Context, accountID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteKeyCalled++
	f.lastAccountID = accountID
	return f.deleteKeyErr
}

func (f *fakeOidcInfra) ComputeThumbprint(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.computeThumbprintCalled++
	if f.computeThumbprintErr != nil {
		return "", f.computeThumbprintErr
	}
	return f.thumbprint, nil
}

var _ = Describe("OidcConfig Controller", func() {
	const testNS = "account-test-account"
	const clusterNS = "cluster-test-cluster-id"
	const testAccountID = "test-account"

	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, testNS)
		ensureNamespace(ctx, clusterNS)
	})

	AfterEach(func() {
		list := &hyperfleetv1alpha1.OidcConfigList{}
		if err := k8sClient.List(ctx, list, client.InNamespace(testNS)); err == nil {
			for i := range list.Items {
				controllerutil.RemoveFinalizer(&list.Items[i], oidcConfigFinalizer)
				_ = k8sClient.Update(ctx, &list.Items[i])
				_ = k8sClient.Delete(ctx, &list.Items[i])
			}
		}
		clusters := &hyperfleetv1alpha1.ClusterList{}
		Expect(k8sClient.List(ctx, clusters, client.InNamespace(clusterNS))).To(Succeed())
		for i := range clusters.Items {
			Expect(k8sClient.Delete(ctx, &clusters.Items[i])).To(Succeed())
		}
	})

	newReconciler := func(infra *fakeOidcInfra) *OidcConfigReconciler {
		return &OidcConfigReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			OIDC:   infra,
		}
	}

	// createReferencingCluster creates a minimal Cluster owned by testAccountID that references
	// oidcConfigID via spec.oidcConfigId, simulating the cluster-creation flow that unblocks a
	// managed OidcConfig's readiness checks (see isReferencedByCluster).
	createReferencingCluster := func(name, oidcConfigID string) {
		cluster := newTestCluster(name)
		cluster.Spec.OidcConfigID = oidcConfigID
		cluster.Labels = map[string]string{accountIDLabel: testAccountID}
		Expect(k8sClient.Create(ctx, cluster)).To(Succeed())
	}

	reconcileN := func(r *OidcConfigReconciler, ns, name string, n int) (ctrl.Result, error) {
		var result ctrl.Result
		var err error
		for i := 0; i < n; i++ {
			result, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: ns, Name: name},
			})
			if err != nil {
				return result, err
			}
		}
		return result, nil
	}

	Context("Managed OidcConfig", func() {
		It("should add finalizer and reach Ready once the issuer is TLS-reachable", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-01", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
					IssuerUrl: "https://oidc.example.com/managed-01",
					AccountID: testAccountID,
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{thumbprint: "abc123def456"}
			r := newReconciler(infra)

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-01"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal(ctrl.Result{}))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-01"}, &updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(&updated, oidcConfigFinalizer)).To(BeTrue())

			// A cluster must reference the config before the issuer is ever probed.
			createReferencingCluster("managed-01-cluster", "managed-01")

			result, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-01"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(thumbprintRefreshDelay))

			Expect(infra.computeThumbprintCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(0))
			Expect(infra.existsCalled).To(Equal(0))
			Expect(infra.readCalled).To(Equal(0))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-01"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
			Expect(updated.Status.Thumbprint).To(Equal("abc123def456"))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal("OIDCConfigured"))
		})

		It("should stay Pending with AwaitingCluster and never probe the issuer until a cluster references it", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-awaiting", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
					IssuerUrl: "https://oidc.example.com/managed-awaiting",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			// ComputeThumbprint must never be called before a cluster references the config.
			infra := &fakeOidcInfra{computeThumbprintErr: fmt.Errorf("should not be called")}
			r := newReconciler(infra)

			result, err := reconcileN(r, testNS, "managed-awaiting", 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(managedPendingRequeueInterval))
			Expect(infra.computeThumbprintCalled).To(Equal(0))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-awaiting"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhasePending))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("AwaitingCluster"))
			Expect(readyCond.Message).NotTo(ContainSubstring("403"))
		})

		It("should not treat a same-ID Cluster from another account as a reference", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-cross-account", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
					IssuerUrl: "https://oidc.example.com/managed-cross-account",
					AccountID: testAccountID,
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			// A cluster with the same oidcConfigId, but owned by a different account, must
			// never count as a reference.
			otherAccountCluster := newTestCluster("cross-account-cluster")
			otherAccountCluster.Spec.OidcConfigID = "managed-cross-account"
			otherAccountCluster.Labels = map[string]string{accountIDLabel: "other-account"}
			Expect(k8sClient.Create(ctx, otherAccountCluster)).To(Succeed())

			infra := &fakeOidcInfra{computeThumbprintErr: fmt.Errorf("should not be called")}
			r := newReconciler(infra)

			result, err := reconcileN(r, testNS, "managed-cross-account", 2)
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(managedPendingRequeueInterval))
			Expect(infra.computeThumbprintCalled).To(Equal(0))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-cross-account"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhasePending))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Reason).To(Equal("AwaitingCluster"))
		})

		It("should stay Pending (not Error) while the issuer isn't TLS-reachable yet", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-pending", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
					IssuerUrl: "https://oidc.example.com/managed-pending",
					AccountID: testAccountID,
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{computeThumbprintErr: fmt.Errorf("TLS dial oidc.example.com: connection refused")}
			r := newReconciler(infra)

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-pending"},
			})
			Expect(err).NotTo(HaveOccurred())

			createReferencingCluster("managed-pending-cluster", "managed-pending")

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-pending"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(managedPendingRequeueInterval))
			Expect(infra.computeThumbprintCalled).To(Equal(1))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-pending"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhasePending))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("IssuerNotReady"))
		})

		It("should reach Ready once the issuer becomes TLS-reachable after being Pending", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-recover", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
					IssuerUrl: "https://oidc.example.com/managed-recover",
					AccountID: testAccountID,
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				computeThumbprintErr: fmt.Errorf("not reachable yet"),
				thumbprint:           "recovered-thumb",
			}
			r := newReconciler(infra)
			createReferencingCluster("managed-recover-cluster", "managed-recover")

			// Reconcile 1: adds finalizer.
			// Reconcile 2: issuer not reachable yet, stays Pending.
			_, err := reconcileN(r, testNS, "managed-recover", 2)
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-recover"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhasePending))

			// The issuer becomes TLS-reachable.
			infra.computeThumbprintErr = nil
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-recover"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(thumbprintRefreshDelay))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-recover"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
			Expect(updated.Status.Thumbprint).To(Equal("recovered-thumb"))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal("OIDCConfigured"))
		})

		It("should stay Ready on subsequent reconciles while continuing to refresh the thumbprint, without touching key storage", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-idem", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
					IssuerUrl: "https://oidc.example.com/managed-idem",
					AccountID: testAccountID,
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{thumbprint: "thumb01"}
			r := newReconciler(infra)
			createReferencingCluster("managed-idem-cluster", "managed-idem")

			_, err := reconcileN(r, testNS, "managed-idem", 2)
			Expect(err).NotTo(HaveOccurred())

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-idem"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(infra.computeThumbprintCalled).To(Equal(2))
			Expect(infra.storeCalled).To(Equal(0))
			Expect(infra.existsCalled).To(Equal(0))
			Expect(infra.readCalled).To(Equal(0))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-idem"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
			Expect(updated.Status.Thumbprint).To(Equal("thumb01"))
		})
	})

	Context("Unmanaged OidcConfig", func() {
		It("should copy customer key and reach Ready", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-01", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
					AccountID:        testAccountID,
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint:      "customer-thumb",
				crossAccountKey: generateTestRSAKeyPEM(),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			// Reconcile 2: copies key, verifies issuer, sets Ready.
			_, err := reconcileN(r, testNS, "unmanaged-01", 2)
			Expect(err).NotTo(HaveOccurred())

			Expect(infra.readCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(1))
			Expect(infra.lastAccountID).To(Equal(testAccountID), "the signing key secret path must be account-scoped")

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-01"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
			Expect(updated.Status.Thumbprint).To(Equal("customer-thumb"))
		})

		It("should set Error phase for invalid private key", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-bad-key", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint:      "thumb",
				crossAccountKey: []byte("not-a-valid-pem"),
			}
			r := newReconciler(infra)

			_, err := reconcileN(r, testNS, "unmanaged-bad-key", 2)
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-bad-key"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseError))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("InvalidPrivateKey"))
		})

		It("should skip key copy when already stored", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-exists", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint: "thumb",
				keyExists:  true,
			}
			r := newReconciler(infra)

			_, err := reconcileN(r, testNS, "unmanaged-exists", 2)
			Expect(err).NotTo(HaveOccurred())

			Expect(infra.readCalled).To(Equal(0))
			Expect(infra.storeCalled).To(Equal(0))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-exists"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
		})

		It("should set Error phase and return an error when the issuer fails verification", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-verify-fail", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				crossAccountKey:      generateTestRSAKeyPEM(),
				computeThumbprintErr: fmt.Errorf("connection refused"),
			}
			r := newReconciler(infra)

			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-verify-fail"},
			})
			Expect(err).NotTo(HaveOccurred())

			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-verify-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("connection refused"))
			Expect(result.RequeueAfter).To(BeZero())

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-verify-fail"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseError))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("IssuerNotReady"))
		})

		It("should recover to Ready after a transient issuer verification failure", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-recover", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				crossAccountKey:      generateTestRSAKeyPEM(),
				thumbprint:           "recovered-thumb",
				computeThumbprintErr: fmt.Errorf("temporarily unreachable"),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-recover"},
			})
			// Reconcile 2: key copy succeeds, verification fails, phase goes to Error.
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-recover"},
			})
			Expect(err).To(HaveOccurred())

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-recover"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseError))

			infra.computeThumbprintErr = nil
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-recover"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(thumbprintRefreshDelay))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-recover"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
			Expect(updated.Status.Thumbprint).To(Equal("recovered-thumb"))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal("OIDCConfigured"))
		})

		It("should set Error condition when cross-account secret read fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-read-fail", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				readCrossAccountErr: fmt.Errorf("assume role denied"),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-read-fail"},
			})
			// Reconcile 2: key doesn't exist yet, cross-account read fails.
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-read-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("assume role denied"))
			Expect(infra.readCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(0))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-read-fail"}, &updated)).To(Succeed())
			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("CrossAccountReadFailed"))
		})

		It("should return error when checking private key existence fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-exists-fail", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				existsErr: fmt.Errorf("secrets manager unreachable"),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-exists-fail"},
			})
			// Reconcile 2: PrivateKeyExists fails.
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-exists-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("secrets manager unreachable"))
			Expect(infra.existsCalled).To(Equal(1))
			Expect(infra.readCalled).To(Equal(0))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-exists-fail"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).NotTo(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
		})
	})

	Context("Deletion", func() {
		It("should delete the private key for a managed config", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-del", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
					IssuerUrl: "https://oidc.example.com/managed-del",
					AccountID: testAccountID,
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{thumbprint: "thumb"}
			r := newReconciler(infra)
			createReferencingCluster("managed-del-cluster", "managed-del")

			// Reach Ready state.
			_, err := reconcileN(r, testNS, "managed-del", 2)
			Expect(err).NotTo(HaveOccurred())

			// Delete.
			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-del"}, &latest)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &latest)).To(Succeed())

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-del"},
			})
			Expect(err).NotTo(HaveOccurred())

			// Deletion is identical for managed and unmanaged configs
			Expect(infra.deleteKeyCalled).To(Equal(1))
		})

		It("should delete the private key for an unmanaged config", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-del", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint:      "thumb",
				crossAccountKey: generateTestRSAKeyPEM(),
			}
			r := newReconciler(infra)

			// Reach Ready state.
			_, err := reconcileN(r, testNS, "unmanaged-del", 2)
			Expect(err).NotTo(HaveOccurred())

			// Delete.
			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-del"}, &latest)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &latest)).To(Succeed())

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "unmanaged-del"},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(infra.deleteKeyCalled).To(Equal(1))
		})

		It("should keep the finalizer and return an error when Secrets Manager cleanup fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-del-sm-fail", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
					IssuerUrl: "https://oidc.example.com/managed-del-sm-fail",
					AccountID: testAccountID,
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{thumbprint: "thumb"}
			r := newReconciler(infra)
			createReferencingCluster("managed-del-sm-fail-cluster", "managed-del-sm-fail")

			_, err := reconcileN(r, testNS, "managed-del-sm-fail", 2)
			Expect(err).NotTo(HaveOccurred())

			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-del-sm-fail"}, &latest)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &latest)).To(Succeed())

			infra.deleteKeyErr = fmt.Errorf("secrets manager delete failed")

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-del-sm-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("secrets manager delete failed"))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-del-sm-fail"}, &latest)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(&latest, oidcConfigFinalizer)).To(BeTrue())
		})
	})

	Context("Error handling", func() {
		It("should handle not-found gracefully", func() {
			r := newReconciler(&fakeOidcInfra{})
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "does-not-exist"},
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should set Error phase for an unrecognized Spec.Type", func() {
			scheme := runtime.NewScheme()
			Expect(hyperfleetv1alpha1.AddToScheme(scheme)).To(Succeed())

			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-type", Namespace: testNS},
				Spec:       hyperfleetv1alpha1.OidcConfigSpec{Type: "bogus"},
			}
			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithStatusSubresource(&hyperfleetv1alpha1.OidcConfig{}).
				WithObjects(oc).
				Build()

			r := &OidcConfigReconciler{
				Client: fakeClient,
				Scheme: scheme,
				OIDC:   &fakeOidcInfra{},
			}

			_, err := reconcileN(r, testNS, "invalid-type", 2)
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(fakeClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "invalid-type"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseError))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("InvalidType"))
		})
	})
})

func generateTestRSAKeyPEM() []byte {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}
