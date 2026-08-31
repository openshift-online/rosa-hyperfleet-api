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
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

// --- fake OIDC infra client ---

type fakeOidcInfra struct {
	mu sync.Mutex

	thumbprint string

	storeErr            error
	existsErr           error
	readCrossAccountErr error
	deleteKeyErr        error
	thumbprintErr       error

	keyExists       bool
	crossAccountKey []byte

	storeCalled      int
	existsCalled     int
	readCalled       int
	deleteKeyCalled  int
	thumbprintCalled int
}

func (f *fakeOidcInfra) StorePrivateKey(_ context.Context, _ string, _ []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.storeCalled++
	return f.storeErr
}

func (f *fakeOidcInfra) PrivateKeyExists(_ context.Context, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.existsCalled++
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

func (f *fakeOidcInfra) DeletePrivateKey(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteKeyCalled++
	return f.deleteKeyErr
}

func (f *fakeOidcInfra) ComputeThumbprint(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.thumbprintCalled++
	if f.thumbprintErr != nil {
		return "", f.thumbprintErr
	}
	return f.thumbprint, nil
}

// --- tests ---

var _ = Describe("OidcConfig Controller", func() {
	const testNS = "account-test-account"

	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, testNS)
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
	})

	newReconciler := func(infra *fakeOidcInfra) *OidcConfigReconciler {
		return &OidcConfigReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			OIDC:   infra,
		}
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

	validSpec := func() hyperfleetv1alpha1.OidcConfigSpec {
		return hyperfleetv1alpha1.OidcConfigSpec{
			IssuerUrl:        "https://customer-oidc.example.com",
			SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
			InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
		}
	}

	Context("Reconcile", func() {
		It("should copy customer key and reach Ready", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "copy-key", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint:      "customer-thumb",
				crossAccountKey: generateTestRSAKeyPEM(),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			// Reconcile 2: copies key, computes thumbprint, sets Ready.
			_, err := reconcileN(r, testNS, "copy-key", 2)
			Expect(err).NotTo(HaveOccurred())

			Expect(infra.readCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(1))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "copy-key"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
			Expect(updated.Status.Thumbprint).To(Equal("customer-thumb"))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal("OIDCConfigured"))
		})

		It("should set Error phase for invalid private key", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "bad-key", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint:      "thumb",
				crossAccountKey: []byte("not-a-valid-pem"),
			}
			r := newReconciler(infra)

			_, err := reconcileN(r, testNS, "bad-key", 2)
			Expect(err).NotTo(HaveOccurred())

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "bad-key"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseError))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("InvalidPrivateKey"))
		})

		It("should skip key copy when already stored", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "key-exists", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint: "thumb",
				keyExists:  true,
			}
			r := newReconciler(infra)

			_, err := reconcileN(r, testNS, "key-exists", 2)
			Expect(err).NotTo(HaveOccurred())

			Expect(infra.readCalled).To(Equal(0))
			Expect(infra.storeCalled).To(Equal(0))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "key-exists"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
		})

		It("should not re-copy the key on re-reconcile once Ready", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "idempotent", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint:      "thumb",
				crossAccountKey: generateTestRSAKeyPEM(),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			// Reconcile 2: copies key, computes thumbprint, sets Ready.
			_, err := reconcileN(r, testNS, "idempotent", 2)
			Expect(err).NotTo(HaveOccurred())

			// The controller doesn't track "already copied" itself — it
			// asks the infra client, so the fake reports the key as
			// existing from here on, simulating what the real client
			// would report on a re-reconcile of an already-Ready config.
			infra.keyExists = true

			// Reconcile 3: should not read/store again, just refresh the
			// thumbprint.
			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "idempotent"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(infra.readCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(1))
		})
	})

	Context("Deletion", func() {
		It("should delete the private key on delete", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "delete-me", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint:      "thumb",
				crossAccountKey: generateTestRSAKeyPEM(),
			}
			r := newReconciler(infra)

			// Reach Ready state.
			_, err := reconcileN(r, testNS, "delete-me", 2)
			Expect(err).NotTo(HaveOccurred())

			// Delete.
			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "delete-me"}, &latest)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &latest)).To(Succeed())

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "delete-me"},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(infra.deleteKeyCalled).To(Equal(1))
		})

		It("should keep the finalizer and return an error when Secrets Manager cleanup fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "delete-sm-fail", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint:      "thumb",
				crossAccountKey: generateTestRSAKeyPEM(),
			}
			r := newReconciler(infra)

			// Reach Ready state.
			_, err := reconcileN(r, testNS, "delete-sm-fail", 2)
			Expect(err).NotTo(HaveOccurred())

			// Delete.
			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "delete-sm-fail"}, &latest)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &latest)).To(Succeed())

			infra.deleteKeyErr = fmt.Errorf("secrets manager delete failed")

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "delete-sm-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("secrets manager delete failed"))

			// The finalizer must remain until Secrets Manager cleanup
			// succeeds, so reconciliation can retry.
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "delete-sm-fail"}, &latest)).To(Succeed())
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

		It("should return an error and set Error phase when thumbprint computation fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "thumb-fail", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				crossAccountKey: generateTestRSAKeyPEM(),
				thumbprintErr:   fmt.Errorf("TLS dial failed"),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "thumb-fail"},
			})
			// Reconcile 2: copies key, thumbprint fails. Returning the
			// error (rather than nil with a fixed RequeueAfter) lets
			// controller-runtime apply exponential backoff instead of
			// retrying every 30s forever, and the phase must reflect the
			// failure as Error.
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "thumb-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("TLS dial failed"))
			Expect(result.RequeueAfter).To(BeZero())

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "thumb-fail"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseError))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("ThumbprintFailed"))
		})

		It("should restore Ready after a thumbprint failure is followed by a successful reconcile", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "thumb-recover", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				thumbprint:      "recovered-thumb",
				thumbprintErr:   fmt.Errorf("TLS dial failed"),
				crossAccountKey: generateTestRSAKeyPEM(),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			// Reconcile 2: copies key, thumbprint fails, phase goes to Error.
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "thumb-recover"},
			})
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "thumb-recover"},
			})
			Expect(err).To(HaveOccurred())

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "thumb-recover"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseError))

			// The customer host becomes reachable again; the next
			// reconcile should restore Ready.
			infra.thumbprintErr = nil
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "thumb-recover"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(thumbprintRefreshDelay))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "thumb-recover"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
			Expect(updated.Status.Thumbprint).To(Equal("recovered-thumb"))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal("OIDCConfigured"))
		})

		It("should set Error condition when cross-account secret read fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "read-fail", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				readCrossAccountErr: fmt.Errorf("assume role denied"),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "read-fail"},
			})
			// Reconcile 2: key doesn't exist yet, cross-account read fails.
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "read-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("assume role denied"))
			Expect(infra.readCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(0))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "read-fail"}, &updated)).To(Succeed())
			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("CrossAccountReadFailed"))
		})

		It("should set Error condition when private key storage fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "store-fail", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				crossAccountKey: generateTestRSAKeyPEM(),
				storeErr:        fmt.Errorf("secrets manager throttled"),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "store-fail"},
			})
			// Reconcile 2: cross-account read succeeds, store fails.
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "store-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("secrets manager throttled"))
			Expect(infra.readCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(1))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "store-fail"}, &updated)).To(Succeed())
			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("SecretStoreFailed"))
		})

		It("should return error when checking private key existence fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "exists-fail", Namespace: testNS},
				Spec:       validSpec(),
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				existsErr: fmt.Errorf("secrets manager unreachable"),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "exists-fail"},
			})
			// Reconcile 2: PrivateKeyExists fails.
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "exists-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("secrets manager unreachable"))
			Expect(infra.existsCalled).To(Equal(1))
			Expect(infra.readCalled).To(Equal(0))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "exists-fail"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).NotTo(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
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
