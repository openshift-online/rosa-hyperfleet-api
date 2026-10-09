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
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/render"
)

var _ = Describe("DNSReservation Controller", func() {
	const (
		reservationNamespace = "account-123456789012"
		reservationName      = "reservation-test"
		shardNamespace       = "dns-shard-0-reservations"
		accountID            = "123456789012"
	)

	ctx := context.Background()

	BeforeEach(func() {
		ensureNamespace(ctx, reservationNamespace)
		ensureNamespace(ctx, shardNamespace)
	})

	AfterEach(func() {
		var reservations hyperfleetv1alpha1.DNSReservationList
		if err := k8sClient.List(ctx, &reservations, client.InNamespace(reservationNamespace)); err == nil {
			for i := range reservations.Items {
				reservation := &reservations.Items[i]
				controllerutil.RemoveFinalizer(reservation, dnsReservationFinalizer)
				_ = k8sClient.Update(ctx, reservation)
				_ = k8sClient.Delete(ctx, reservation)
			}
		}
		var indexes hyperfleetv1alpha1.IndexList
		if err := k8sClient.List(ctx, &indexes, client.InNamespace(shardNamespace), client.MatchingLabels{accountIDLabel: accountID}); err == nil {
			for i := range indexes.Items {
				_ = k8sClient.Delete(ctx, &indexes.Items[i])
			}
		}
	})

	newReservation := func(name string) *hyperfleetv1alpha1.DNSReservation {
		return &hyperfleetv1alpha1.DNSReservation{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: reservationNamespace,
				Labels:    map[string]string{accountIDLabel: accountID},
			},
			Spec: hyperfleetv1alpha1.DNSReservationSpec{},
			Status: hyperfleetv1alpha1.DNSReservationStatus{
				Phase: hyperfleetv1alpha1.DNSReservationPhasePending,
			},
		}
	}

	newReconciler := func() *DNSReservationReconciler {
		return &DNSReservationReconciler{
			Client:         k8sClient,
			APIReader:      k8sClient,
			Scheme:         k8sClient.Scheme(),
			RegionalConfig: render.RegionalConfig{BaseDomainSuffix: "example.com"},
		}
	}

	reconcileReservation := func(r *DNSReservationReconciler, name string) (ctrl.Result, error) {
		return r.Reconcile(ctx, reconcile.Request{NamespacedName: types.NamespacedName{Namespace: reservationNamespace, Name: name}})
	}

	It("allocates a base domain and records UID ownership on the shard Index", func() {
		reservation := newReservation(reservationName)
		Expect(k8sClient.Create(ctx, reservation)).To(Succeed())

		r := newReconciler()
		r.generatePrefix = func() (string, error) { return "f7a3", nil }
		_, err := reconcileReservation(r, reservation.Name)
		Expect(err).NotTo(HaveOccurred()) // add finalizer
		result, err := reconcileReservation(r, reservation.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(statusRefreshDelay))

		var updated hyperfleetv1alpha1.DNSReservation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: reservationNamespace, Name: reservationName}, &updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.DNSReservationPhaseReady))
		Expect(updated.Status.BaseDomain).To(Equal("f7a3.0.example.com"))
		Expect(meta.FindStatusCondition(updated.Status.Conditions, "Ready")).To(BeNil())

		var index hyperfleetv1alpha1.Index
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: shardNamespace, Name: "f7a3"}, &index)).To(Succeed())
		Expect(index.Labels[ownerUIDLabel]).To(Equal(string(updated.UID)))
		Expect(index.Labels[accountIDLabel]).To(Equal(accountID))
	})

	It("recovers an Index created before reservation status was written", func() {
		reservation := newReservation(reservationName)
		Expect(k8sClient.Create(ctx, reservation)).To(Succeed())
		index := &hyperfleetv1alpha1.Index{ObjectMeta: metav1.ObjectMeta{
			Name:      "a1b2",
			Namespace: shardNamespace,
			Labels: map[string]string{
				accountIDLabel: accountID,
				ownerUIDLabel:  string(reservation.UID),
			},
		}}
		Expect(k8sClient.Create(ctx, index)).To(Succeed())

		r := newReconciler()
		r.generatePrefix = func() (string, error) { return "", fmt.Errorf("should not allocate again") }
		_, err := reconcileReservation(r, reservation.Name)
		Expect(err).NotTo(HaveOccurred())
		_, err = reconcileReservation(r, reservation.Name)
		Expect(err).NotTo(HaveOccurred())

		var updated hyperfleetv1alpha1.DNSReservation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: reservationNamespace, Name: reservationName}, &updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.DNSReservationPhaseReady))
		Expect(updated.Status.BaseDomain).To(Equal("a1b2.0.example.com"))
	})

	It("reports bounded prefix collisions as retryable and later recovers", func() {
		reservation := newReservation(reservationName)
		Expect(k8sClient.Create(ctx, reservation)).To(Succeed())
		prefixes := []string{"1001", "1002", "1003", "1004", "1005"}
		for _, prefix := range prefixes {
			idx := &hyperfleetv1alpha1.Index{ObjectMeta: metav1.ObjectMeta{
				Name:      prefix,
				Namespace: shardNamespace,
				Labels: map[string]string{
					accountIDLabel: accountID,
					ownerUIDLabel:  "another-reservation",
				},
			}}
			Expect(k8sClient.Create(ctx, idx)).To(Succeed())
		}

		i := 0
		r := newReconciler()
		r.generatePrefix = func() (string, error) {
			prefix := prefixes[i]
			i++
			return prefix, nil
		}
		_, err := reconcileReservation(r, reservation.Name)
		Expect(err).NotTo(HaveOccurred())
		result, err := reconcileReservation(r, reservation.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.RequeueAfter).To(Equal(dnsReservationRetryInterval))

		var updated hyperfleetv1alpha1.DNSReservation
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: reservationNamespace, Name: reservationName}, &updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.DNSReservationPhasePending))
		Expect(updated.Status.BaseDomain).To(BeEmpty())
		condition := meta.FindStatusCondition(updated.Status.Conditions, "Ready")
		Expect(condition).NotTo(BeNil())
		Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		Expect(condition.Reason).To(Equal("PrefixAllocationFailed"))

		r.generatePrefix = func() (string, error) { return "f777", nil }
		_, err = reconcileReservation(r, reservation.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: reservationNamespace, Name: reservationName}, &updated)).To(Succeed())
		Expect(updated.Status.Phase).To(Equal(hyperfleetv1alpha1.DNSReservationPhaseReady))
		Expect(updated.Status.BaseDomain).To(Equal("f777.0.example.com"))
		Expect(meta.FindStatusCondition(updated.Status.Conditions, "Ready")).To(BeNil())
	})

	It("deletes only Indexes owned by the reservation UID", func() {
		reservation := newReservation(reservationName)
		Expect(k8sClient.Create(ctx, reservation)).To(Succeed())
		owned := &hyperfleetv1alpha1.Index{ObjectMeta: metav1.ObjectMeta{
			Name:      "f7a3",
			Namespace: shardNamespace,
			Labels: map[string]string{
				accountIDLabel: accountID,
				ownerUIDLabel:  string(reservation.UID),
			},
		}}
		foreign := &hyperfleetv1alpha1.Index{ObjectMeta: metav1.ObjectMeta{
			Name:      "f7a4",
			Namespace: shardNamespace,
			Labels: map[string]string{
				accountIDLabel: accountID,
				ownerUIDLabel:  "another-reservation",
			},
		}}
		Expect(k8sClient.Create(ctx, owned)).To(Succeed())
		Expect(k8sClient.Create(ctx, foreign)).To(Succeed())

		r := newReconciler()
		_, err := reconcileReservation(r, reservation.Name)
		Expect(err).NotTo(HaveOccurred()) // add finalizer
		Expect(k8sClient.Delete(ctx, reservation)).To(Succeed())
		_, err = reconcileReservation(r, reservation.Name)
		Expect(err).NotTo(HaveOccurred())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, client.ObjectKeyFromObject(owned), &hyperfleetv1alpha1.Index{}))).To(BeTrue())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(foreign), &hyperfleetv1alpha1.Index{})).To(Succeed())
		Expect(apierrors.IsNotFound(k8sClient.Get(ctx, types.NamespacedName{Namespace: reservationNamespace, Name: reservation.Name}, &hyperfleetv1alpha1.DNSReservation{}))).To(BeTrue())
	})
})
