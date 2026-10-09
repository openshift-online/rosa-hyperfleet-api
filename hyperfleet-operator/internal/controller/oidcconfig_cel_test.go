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

	newRawManagedConfig := func(name, arnField string, includeARN bool) *unstructured.Unstructured {
		spec := map[string]interface{}{
			"type":      hyperfleetv1alpha1.OidcConfigTypeManaged,
			"issuerUrl": "https://oidc.example.com/" + name,
		}
		if includeARN {
			spec[arnField] = ""
		}
		oc := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": hyperfleetv1alpha1.GroupVersion.String(),
			"kind":       "OidcConfig",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": testNS,
			},
			"spec": spec,
		}}
		oc.SetGroupVersionKind(hyperfleetv1alpha1.GroupVersion.WithKind("OidcConfig"))
		return oc
	}

	Context("Create validation", func() {
		It("accepts a valid managed config", func() {
			oc := newOidcConfig("managed-valid", hyperfleetv1alpha1.OidcConfigSpec{
				Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
				IssuerUrl: "https://oidc.example.com/managed-valid",
			})
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())
		})

		It("rejects managed config with secretArn set", func() {
			oc := newOidcConfig("managed-secret", hyperfleetv1alpha1.OidcConfigSpec{
				Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
				IssuerUrl: "https://oidc.example.com/managed-secret",
				SecretArn: "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
			})
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("managed type must not set secretArn or installerRoleArn"))
		})

		It("rejects managed config with installerRoleArn set", func() {
			oc := newOidcConfig("managed-role", hyperfleetv1alpha1.OidcConfigSpec{
				Type:             hyperfleetv1alpha1.OidcConfigTypeManaged,
				IssuerUrl:        "https://oidc.example.com/managed-role",
				InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
			})
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("managed type must not set secretArn or installerRoleArn"))
		})

		It("accepts a valid unmanaged config", func() {
			oc := newOidcConfig("unmanaged-valid", hyperfleetv1alpha1.OidcConfigSpec{
				Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
				IssuerUrl:        "https://oidc.example.com",
				SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
				InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
			})
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())
		})

		It("rejects unmanaged config missing secretArn", func() {
			oc := newOidcConfig("unmanaged-no-secret", hyperfleetv1alpha1.OidcConfigSpec{
				Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
				IssuerUrl:        "https://oidc.example.com",
				InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
			})
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unmanaged type requires secretArn and installerRoleArn"))
		})

		It("rejects unmanaged config missing installerRoleArn", func() {
			oc := newOidcConfig("unmanaged-no-role", hyperfleetv1alpha1.OidcConfigSpec{
				Type:      hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
				IssuerUrl: "https://oidc.example.com",
				SecretArn: "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
			})
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("unmanaged type requires secretArn and installerRoleArn"))
		})

		It("rejects a managed config missing issuerUrl", func() {
			oc := newOidcConfig("managed-no-issuer", hyperfleetv1alpha1.OidcConfigSpec{
				Type: hyperfleetv1alpha1.OidcConfigTypeManaged,
			})
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("issuerUrl"))
		})

		It("rejects an unmanaged config missing issuerUrl", func() {
			oc := newOidcConfig("unmanaged-no-issuer", hyperfleetv1alpha1.OidcConfigSpec{
				Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
				SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
				InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
			})
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("issuerUrl"))
		})

		It("rejects invalid type value", func() {
			oc := newOidcConfig("bad-type", hyperfleetv1alpha1.OidcConfigSpec{
				Type:      "invalid",
				IssuerUrl: "https://oidc.example.com/bad-type",
			})
			err := k8sClient.Create(ctx, oc)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("Unsupported value"))
		})
	})

	Context("Update immutability", func() {
		It("allows updates when both optional ARN fields remain absent", func() {
			oc := newRawManagedConfig("managed-arns-absent", "", false)
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			var latest unstructured.Unstructured
			latest.SetGroupVersionKind(hyperfleetv1alpha1.GroupVersion.WithKind("OidcConfig"))
			key := types.NamespacedName{Namespace: testNS, Name: oc.GetName()}
			Expect(k8sClient.Get(ctx, key, &latest)).To(Succeed())
			Expect(unstructured.SetNestedMap(latest.Object, map[string]interface{}{"updated": "true"}, "metadata", "annotations")).To(Succeed())
			Expect(k8sClient.Update(ctx, &latest)).To(Succeed())
		})

		It("rejects adding or removing either optional ARN field", func() {
			tests := []struct {
				name         string
				field        string
				initiallySet bool
				setOnUpdate  bool
				wantMessage  string
			}{
				{
					name:        "add-secret-arn",
					field:       "secretArn",
					setOnUpdate: true,
					wantMessage: "spec.secretArn is immutable",
				},
				{
					name:         "remove-secret-arn",
					field:        "secretArn",
					initiallySet: true,
					wantMessage:  "spec.secretArn is immutable",
				},
				{
					name:        "add-installer-role-arn",
					field:       "installerRoleArn",
					setOnUpdate: true,
					wantMessage: "spec.installerRoleArn is immutable",
				},
				{
					name:         "remove-installer-role-arn",
					field:        "installerRoleArn",
					initiallySet: true,
					wantMessage:  "spec.installerRoleArn is immutable",
				},
			}

			for _, tt := range tests {
				By(tt.name)
				oc := newRawManagedConfig(tt.name, tt.field, tt.initiallySet)
				Expect(k8sClient.Create(ctx, oc)).To(Succeed())

				var latest unstructured.Unstructured
				latest.SetGroupVersionKind(hyperfleetv1alpha1.GroupVersion.WithKind("OidcConfig"))
				key := types.NamespacedName{Namespace: testNS, Name: tt.name}
				Expect(k8sClient.Get(ctx, key, &latest)).To(Succeed())
				spec, found, err := unstructured.NestedMap(latest.Object, "spec")
				Expect(err).NotTo(HaveOccurred())
				Expect(found).To(BeTrue())
				if tt.setOnUpdate {
					spec[tt.field] = ""
				} else {
					delete(spec, tt.field)
				}
				Expect(unstructured.SetNestedMap(latest.Object, spec, "spec")).To(Succeed())

				err = k8sClient.Update(ctx, &latest)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring(tt.wantMessage))
			}
		})

		It("rejects changing type", func() {
			oc := newOidcConfig("immut-type", hyperfleetv1alpha1.OidcConfigSpec{
				Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
				IssuerUrl: "https://oidc.example.com/immut-type",
			})
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "immut-type"}, &latest)).To(Succeed())
			latest.Spec.Type = hyperfleetv1alpha1.OidcConfigTypeUnmanaged
			latest.Spec.SecretArn = "arn:aws:secretsmanager:us-east-1:123456789012:secret:key"
			latest.Spec.InstallerRoleArn = "arn:aws:iam::123456789012:role/installer"
			err := k8sClient.Update(ctx, &latest)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.type is immutable"))
		})

		It("rejects changing secretArn", func() {
			oc := newOidcConfig("immut-secret", hyperfleetv1alpha1.OidcConfigSpec{
				Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
				IssuerUrl:        "https://oidc.example.com",
				SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key-v1",
				InstallerRoleArn: "arn:aws:iam::123456789012:role/installer",
			})
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "immut-secret"}, &latest)).To(Succeed())
			latest.Spec.SecretArn = "arn:aws:secretsmanager:us-east-1:123456789012:secret:key-v2"
			err := k8sClient.Update(ctx, &latest)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.secretArn is immutable"))
		})

		It("rejects changing installerRoleArn", func() {
			oc := newOidcConfig("immut-role", hyperfleetv1alpha1.OidcConfigSpec{
				Type:             hyperfleetv1alpha1.OidcConfigTypeUnmanaged,
				IssuerUrl:        "https://oidc.example.com",
				SecretArn:        "arn:aws:secretsmanager:us-east-1:123456789012:secret:key",
				InstallerRoleArn: "arn:aws:iam::123456789012:role/installer-v1",
			})
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "immut-role"}, &latest)).To(Succeed())
			latest.Spec.InstallerRoleArn = "arn:aws:iam::123456789012:role/installer-v2"
			err := k8sClient.Update(ctx, &latest)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.installerRoleArn is immutable"))
		})

		It("rejects changing issuerUrl (immutable from creation)", func() {
			oc := newOidcConfig("issuer-immut", hyperfleetv1alpha1.OidcConfigSpec{
				Type:      hyperfleetv1alpha1.OidcConfigTypeManaged,
				IssuerUrl: "https://cloudfront.example.com/config-id",
			})
			Expect(k8sClient.Create(ctx, oc)).To(Succeed())

			var latest hyperfleetv1alpha1.OidcConfig
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNS, Name: "issuer-immut"}, &latest)).To(Succeed())
			latest.Spec.IssuerUrl = "https://cloudfront.example.com/different-id"
			err := k8sClient.Update(ctx, &latest)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.issuerUrl is immutable"))
		})
	})
})
