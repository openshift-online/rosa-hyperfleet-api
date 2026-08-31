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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

var _ = Describe("OidcConfig CEL Validation", func() {
	const testNS = "oidcconfig-cel-test"
	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, testNS)
	})

	AfterEach(func() {
		list := &hyperfleetv1alpha1.OidcConfigList{}
		if err := k8sClient.List(ctx, list, client.InNamespace(testNS)); err == nil {
			for i := range list.Items {
				_ = k8sClient.Delete(ctx, &list.Items[i])
			}
		}
	})

	newOidcConfig := func(name string, spec hyperfleetv1alpha1.OidcConfigSpec) *hyperfleetv1alpha1.OidcConfig {
		return &hyperfleetv1alpha1.OidcConfig{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: testNS,
			},
			Spec: spec,
		}
	}

	validSpec := func() hyperfleetv1alpha1.OidcConfigSpec {
		return hyperfleetv1alpha1.OidcConfigSpec{
			IssuerUrl:        "https://oidc.example.com",
			SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
			InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
		}
	}

	// newOidcConfigMissing builds an OidcConfig via an unstructured object
	// so the omitted field is absent from the request body entirely, which
	// is what the CRD's "required" validation actually checks — a typed
	// struct without `omitempty` would always serialize the field (as an
	// empty string) and never exercise this path.
	newOidcConfigMissing := func(name string, omit string) *unstructured.Unstructured {
		spec := map[string]any{
			"issuerUrl":        "https://oidc.example.com",
			"secretArn":        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
			"installerRoleArn": "arn:aws:iam::123456789012:role/installer",
		}
		delete(spec, omit)
		return &unstructured.Unstructured{
			Object: map[string]any{
				"apiVersion": hyperfleetv1alpha1.GroupVersion.String(),
				"kind":       "OidcConfig",
				"metadata": map[string]any{
					"name":      name,
					"namespace": testNS,
				},
				"spec": spec,
			},
		}
	}

	Context("Create validation", func() {
		It("accepts a valid config", func() {
			oc := newOidcConfig("valid", validSpec())
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())
		})

		It("rejects a config missing secretArn", func() {
			oc := newOidcConfigMissing("no-secret", "secretArn")
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.secretArn: Required value"))
		})

		It("rejects a config missing installerRoleArn", func() {
			oc := newOidcConfigMissing("no-role", "installerRoleArn")
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.installerRoleArn: Required value"))
		})

		It("rejects a config missing issuerUrl", func() {
			oc := newOidcConfigMissing("no-issuer", "issuerUrl")
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.issuerUrl: Required value"))
		})
	})

	Context("Update immutability", func() {
		It("rejects changing secretArn", func() {
			oc := newOidcConfig("immut-secret", validSpec())
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "immut-secret"}, &latest)).To(Succeed())
			latest.Spec.SecretArn = "arn:aws:secretsmanager:us-east-1:123456789012:secret:key-v2"
			err := k8sClient.Update(ctx, &latest)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.secretArn is immutable"))
		})

		It("rejects changing installerRoleArn", func() {
			oc := newOidcConfig("immut-role", validSpec())
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "immut-role"}, &latest)).To(Succeed())
			latest.Spec.InstallerRoleArn = "arn:aws:iam::123456789012:role/installer-v2"
			err := k8sClient.Update(ctx, &latest)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.installerRoleArn is immutable"))
		})

		It("rejects changing issuerUrl", func() {
			oc := newOidcConfig("immut-issuer", validSpec())
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "immut-issuer"}, &latest)).To(Succeed())
			latest.Spec.IssuerUrl = "https://oidc-changed.example.com"
			err := k8sClient.Update(ctx, &latest)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.issuerUrl is immutable"))
		})
	})
})
