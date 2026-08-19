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

	issuerBaseURL string
	thumbprint    string

	generateErr         error
	uploadErr           error
	storeErr            error
	existsErr           error
	readCrossAccountErr error
	deleteDocsErr       error
	deleteKeyErr        error
	thumbprintErr       error

	keyExists       bool
	crossAccountKey []byte

	generateCalled   int
	uploadCalled     int
	storeCalled      int
	existsCalled     int
	readCalled       int
	deleteDocsCalled int
	deleteKeyCalled  int
	thumbprintCalled int
}

func (f *fakeOidcInfra) GenerateKeyPair() ([]byte, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.generateCalled++
	if f.generateErr != nil {
		return nil, nil, f.generateErr
	}
	return []byte("fake-private-key-pem"), []byte(`{"keys":[]}`), nil
}

func (f *fakeOidcInfra) UploadOIDCDocuments(_ context.Context, _ string, _ []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploadCalled++
	return f.uploadErr
}

func (f *fakeOidcInfra) DeleteOIDCDocuments(_ context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteDocsCalled++
	return f.deleteDocsErr
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

func (f *fakeOidcInfra) IssuerURL(configID string) string {
	return f.issuerBaseURL + "/" + configID
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

	Context("Managed OidcConfig", func() {
		It("should add finalizer, set up infrastructure, and reach Ready", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-01", Namespace: testNS},
				Spec:       hyperfleetv1alpha1.OidcConfigSpec{Type: "managed"},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				issuerBaseURL: "https://oidc.example.com",
				thumbprint:    "abc123def456",
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-01"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-01"}, &updated)).To(Succeed())
			Expect(controllerutil.ContainsFinalizer(&updated, oidcConfigFinalizer)).To(BeTrue())

			// Reconcile 2: generates key, uploads, stores, sets issuerUrl.
			result, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-01"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.Requeue).To(BeTrue()) //nolint:staticcheck

			Expect(infra.generateCalled).To(Equal(1))
			Expect(infra.uploadCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(1))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-01"}, &updated)).To(Succeed())
			Expect(updated.Spec.IssuerUrl).To(Equal("https://oidc.example.com/managed-01"))

			// Reconcile 3: computes thumbprint, sets Ready.
			result, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-01"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).To(Equal(thumbprintRefreshDelay))

			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-01"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
			Expect(updated.Status.Thumbprint).To(Equal("abc123def456"))

			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionTrue))
			Expect(readyCond.Reason).To(Equal("OIDCConfigured"))
		})

		It("should not regenerate key on re-reconcile when already Ready", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-idem", Namespace: testNS},
				Spec:       hyperfleetv1alpha1.OidcConfigSpec{Type: "managed"},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				issuerBaseURL: "https://oidc.example.com",
				thumbprint:    "thumb01",
			}
			r := newReconciler(infra)

			// 3 reconciles to reach Ready.
			_, err := reconcileN(r, testNS, "managed-idem", 3)
			Expect(err).NotTo(HaveOccurred())

			// 4th reconcile: should not regenerate.
			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-idem"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(infra.generateCalled).To(Equal(1))
			Expect(infra.uploadCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(1))
		})
	})

	Context("Unmanaged OidcConfig", func() {
		It("should copy customer key and reach Ready", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-01", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             "unmanaged",
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				issuerBaseURL:   "https://oidc.example.com",
				thumbprint:      "customer-thumb",
				crossAccountKey: generateTestRSAKeyPEM(),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			// Reconcile 2: copies key, computes thumbprint, sets Ready.
			_, err := reconcileN(r, testNS, "unmanaged-01", 2)
			Expect(err).NotTo(HaveOccurred())

			Expect(infra.readCalled).To(Equal(1))
			Expect(infra.storeCalled).To(Equal(1))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "unmanaged-01"}, &updated)).To(Succeed())
			Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.OidcConfigPhaseReady))
			Expect(updated.Status.Thumbprint).To(Equal("customer-thumb"))
		})

		It("should set Error phase for invalid private key", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-bad-key", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             "unmanaged",
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				issuerBaseURL:   "https://oidc.example.com",
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
					Type:             "unmanaged",
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				issuerBaseURL: "https://oidc.example.com",
				thumbprint:    "thumb",
				keyExists:     true,
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
	})

	Context("Deletion", func() {
		It("should clean up S3 and SM for managed config", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-del", Namespace: testNS},
				Spec:       hyperfleetv1alpha1.OidcConfigSpec{Type: "managed"},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				issuerBaseURL: "https://oidc.example.com",
				thumbprint:    "thumb",
			}
			r := newReconciler(infra)

			// Reach Ready state.
			_, err := reconcileN(r, testNS, "managed-del", 3)
			Expect(err).NotTo(HaveOccurred())

			// Delete.
			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-del"}, &latest)).To(Succeed())
			Expect(k8sClient.Delete(ctx, &latest)).To(Succeed())

			_, err = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-del"},
			})
			Expect(err).NotTo(HaveOccurred())

			Expect(infra.deleteDocsCalled).To(Equal(1))
			Expect(infra.deleteKeyCalled).To(Equal(1))
		})

		It("should clean up SM only for unmanaged config", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "unmanaged-del", Namespace: testNS},
				Spec: hyperfleetv1alpha1.OidcConfigSpec{
					Type:             "unmanaged",
					IssuerUrl:        "https://customer-oidc.example.com",
					SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
					InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
				},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				issuerBaseURL:   "https://oidc.example.com",
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

			Expect(infra.deleteDocsCalled).To(Equal(0))
			Expect(infra.deleteKeyCalled).To(Equal(1))
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

		It("should return error when key generation fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-gen-fail", Namespace: testNS},
				Spec:       hyperfleetv1alpha1.OidcConfigSpec{Type: "managed"},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				issuerBaseURL: "https://oidc.example.com",
				generateErr:   fmt.Errorf("entropy failure"),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			_, _ = r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-gen-fail"},
			})
			// Reconcile 2: key generation fails.
			_, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-gen-fail"},
			})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("entropy failure"))

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-gen-fail"}, &updated)).To(Succeed())
			readyCond := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
			Expect(readyCond).NotTo(BeNil())
			Expect(readyCond.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCond.Reason).To(Equal("KeyGenerationFailed"))
		})

		It("should requeue when thumbprint computation fails", func() {
			oc := &hyperfleetv1alpha1.OidcConfig{
				ObjectMeta: metav1.ObjectMeta{Name: "managed-thumb-fail", Namespace: testNS},
				Spec:       hyperfleetv1alpha1.OidcConfigSpec{Type: "managed"},
			}
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			infra := &fakeOidcInfra{
				issuerBaseURL: "https://oidc.example.com",
				thumbprintErr: fmt.Errorf("TLS dial failed"),
			}
			r := newReconciler(infra)

			// Reconcile 1: adds finalizer.
			// Reconcile 2: generates key, uploads, stores, sets issuerUrl.
			_, err := reconcileN(r, testNS, "managed-thumb-fail", 2)
			Expect(err).NotTo(HaveOccurred())

			// Reconcile 3: thumbprint fails, should requeue.
			result, err := r.Reconcile(ctx, reconcile.Request{
				NamespacedName: types.NamespacedName{Namespace: testNS, Name: "managed-thumb-fail"},
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result.RequeueAfter).NotTo(BeZero())

			var updated hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "managed-thumb-fail"}, &updated)).To(Succeed())
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
