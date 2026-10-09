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
	"errors"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

const (
	dnsUnitTestAccountID       = "123456789012"
	dnsUnitTestClusterUID      = "cluster-uid"
	dnsUnitTestReservationUID  = "reservation-uid"
	dnsUnitTestOIDCConfigUID   = "oidc-config-uid"
	dnsUnitTestBaseDomain      = "f7a3.0.example.com"
	dnsUnitTestClusterName     = "dns-unit-test-cluster"
	dnsUnitTestReservationName = "dns-unit-test-reservation"
)

type listFailureClient struct {
	client.Client
	dnsReservationListErr error
	oidcConfigListErr     error
}

type getFailureClient struct {
	client.Client
	err error
}

type createFailureClient struct {
	client.Client
	err error
}

type automaticDNSReservationCollisionClient struct {
	client.Client
	createCalls int
}

func (c *listFailureClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	switch list.(type) {
	case *hyperfleetv1alpha1.DNSReservationList:
		if c.dnsReservationListErr != nil {
			return c.dnsReservationListErr
		}
	case *hyperfleetv1alpha1.OidcConfigList:
		if c.oidcConfigListErr != nil {
			return c.oidcConfigListErr
		}
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *getFailureClient) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return c.err
}

func (c *createFailureClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, isReservation := obj.(*hyperfleetv1alpha1.DNSReservation); isReservation {
		return c.err
	}
	return c.Client.Create(ctx, obj, opts...)
}

func (c *automaticDNSReservationCollisionClient) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	reservation, isReservation := obj.(*hyperfleetv1alpha1.DNSReservation)
	if !isReservation {
		return c.Client.Create(ctx, obj, opts...)
	}
	c.createCalls++

	recovered := reservation.DeepCopy()
	recovered.UID = types.UID("raced-reservation-uid")
	if err := c.Client.Create(ctx, recovered, opts...); err != nil {
		return err
	}
	return apierrors.NewAlreadyExists(hyperfleetv1alpha1.GroupVersion.WithResource("dnsreservations").GroupResource(), reservation.Name)
}

func newClusterDNSUnitReconciler(t *testing.T, objects ...client.Object) *ClusterReconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := hyperfleetv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register API types: %v", err)
	}
	k8sClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(objects...).
		WithStatusSubresource(&hyperfleetv1alpha1.Cluster{}).
		Build()
	return &ClusterReconciler{Client: k8sClient}
}

func newDNSUnitTestCluster() *hyperfleetv1alpha1.Cluster {
	return &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dnsUnitTestClusterName,
			Namespace: accountNamespace(dnsUnitTestAccountID),
			UID:       types.UID(dnsUnitTestClusterUID),
			Labels:    map[string]string{accountIDLabel: dnsUnitTestAccountID},
		},
		Spec: hyperfleetv1alpha1.ClusterSpec{
			AccountID:        dnsUnitTestAccountID,
			DNSReservationID: dnsUnitTestReservationUID,
		},
	}
}

func newDNSUnitTestReservation(cluster *hyperfleetv1alpha1.Cluster) *hyperfleetv1alpha1.DNSReservation {
	return &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dnsUnitTestReservationName,
			Namespace: cluster.Namespace,
			UID:       types.UID(dnsUnitTestReservationUID),
			Labels: map[string]string{
				accountIDLabel:           dnsUnitTestAccountID,
				claimedByClusterUIDLabel: string(cluster.UID),
			},
		},
		Status: hyperfleetv1alpha1.DNSReservationStatus{
			Phase:      hyperfleetv1alpha1.DNSReservationPhaseReady,
			BaseDomain: dnsUnitTestBaseDomain,
		},
	}
}

func newDNSUnitTestOIDCConfig(cluster *hyperfleetv1alpha1.Cluster, claimedBy string) *hyperfleetv1alpha1.OidcConfig {
	return &hyperfleetv1alpha1.OidcConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dns-unit-test-oidc-config",
			Namespace: cluster.Namespace,
			UID:       types.UID(dnsUnitTestOIDCConfigUID),
			Labels: map[string]string{
				accountIDLabel:           dnsUnitTestAccountID,
				claimedByClusterUIDLabel: claimedBy,
			},
		},
	}
}

func newDNSUnitTestAutomaticReservation(cluster *hyperfleetv1alpha1.Cluster, reservationUID string) *hyperfleetv1alpha1.DNSReservation {
	return &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      automaticDNSReservationName(cluster.Name, string(cluster.UID)),
			Namespace: cluster.Namespace,
			UID:       types.UID(reservationUID),
			Labels: map[string]string{
				accountIDLabel:           dnsUnitTestAccountID,
				claimedByClusterUIDLabel: string(cluster.UID),
			},
		},
	}
}

func TestConfirmedDNSReservation(t *testing.T) {
	dnsListFailure := errors.New("DNSReservation list failed")
	oidcListFailure := errors.New("OidcConfig list failed")
	tests := []struct {
		name          string
		configure     func(*hyperfleetv1alpha1.Cluster, *hyperfleetv1alpha1.DNSReservation) []client.Object
		dnsListErr    error
		oidcListErr   error
		wantDomain    string
		wantConfirmed bool
		wantError     error
	}{
		{
			name: "missing account ID",
			configure: func(cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				cluster.Spec.AccountID = ""
				delete(cluster.Labels, accountIDLabel)
				return []client.Object{reservation}
			},
		},
		{
			name: "missing DNSReservation ID",
			configure: func(cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				cluster.Spec.DNSReservationID = ""
				return []client.Object{reservation}
			},
		},
		{
			name: "missing database Cluster UID",
			configure: func(cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				cluster.UID = ""
				return []client.Object{reservation}
			},
		},
		{
			name: "account ID falls back to Cluster label",
			configure: func(cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				cluster.Spec.AccountID = ""
				return []client.Object{reservation}
			},
			wantDomain:    dnsUnitTestBaseDomain,
			wantConfirmed: true,
		},
		{
			name:       "reservation list error",
			dnsListErr: dnsListFailure,
			wantError:  dnsListFailure,
		},
		{
			name: "referenced reservation is missing",
			configure: func(_ *hyperfleetv1alpha1.Cluster, _ *hyperfleetv1alpha1.DNSReservation) []client.Object {
				return nil
			},
		},
		{
			name: "reservation UID does not match Cluster reference",
			configure: func(_ *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				reservation.UID = "different-reservation-uid"
				return []client.Object{reservation}
			},
		},
		{
			name: "reservation is claimed by another Cluster",
			configure: func(cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				reservation.Labels[claimedByClusterUIDLabel] = "other-cluster-uid"
				return []client.Object{reservation}
			},
		},
		{
			name: "reservation is not Ready",
			configure: func(_ *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				reservation.Status.Phase = hyperfleetv1alpha1.DNSReservationPhasePending
				return []client.Object{reservation}
			},
		},
		{
			name: "reservation has no base domain",
			configure: func(_ *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				reservation.Status.BaseDomain = ""
				return []client.Object{reservation}
			},
		},
		{
			name: "referenced OIDC config is missing",
			configure: func(cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				cluster.Spec.OidcConfigID = dnsUnitTestOIDCConfigUID
				return []client.Object{reservation}
			},
		},
		{
			name: "OIDC config list error",
			configure: func(cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				cluster.Spec.OidcConfigID = dnsUnitTestOIDCConfigUID
				return []client.Object{reservation}
			},
			oidcListErr: oidcListFailure,
			wantError:   oidcListFailure,
		},
		{
			name: "OIDC config claim belongs to another Cluster",
			configure: func(cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				cluster.Spec.OidcConfigID = dnsUnitTestOIDCConfigUID
				return []client.Object{reservation, newDNSUnitTestOIDCConfig(cluster, "other-cluster-uid")}
			},
		},
		{
			name: "DNS and OIDC claims are confirmed",
			configure: func(cluster *hyperfleetv1alpha1.Cluster, reservation *hyperfleetv1alpha1.DNSReservation) []client.Object {
				cluster.Spec.OidcConfigID = dnsUnitTestOIDCConfigUID
				return []client.Object{reservation, newDNSUnitTestOIDCConfig(cluster, string(cluster.UID))}
			},
			wantDomain:    dnsUnitTestBaseDomain,
			wantConfirmed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := newDNSUnitTestCluster()
			reservation := newDNSUnitTestReservation(cluster)
			objects := []client.Object{reservation}
			if tt.configure != nil {
				objects = tt.configure(cluster, reservation)
			}
			reconciler := newClusterDNSUnitReconciler(t, objects...)
			if tt.dnsListErr != nil || tt.oidcListErr != nil {
				reconciler.Client = &listFailureClient{
					Client:                reconciler.Client,
					dnsReservationListErr: tt.dnsListErr,
					oidcConfigListErr:     tt.oidcListErr,
				}
			}

			domain, confirmed, err := reconciler.confirmedDNSReservation(context.Background(), cluster)
			if tt.wantError != nil {
				if !errors.Is(err, tt.wantError) {
					t.Fatalf("confirmedDNSReservation error = %v, want wrapped %v", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("confirmedDNSReservation returned unexpected error: %v", err)
			}
			if domain != tt.wantDomain || confirmed != tt.wantConfirmed {
				t.Errorf("confirmedDNSReservation() = (%q, %t), want (%q, %t)", domain, confirmed, tt.wantDomain, tt.wantConfirmed)
			}
		})
	}
}

func TestReconcileDNSReservationWaitsForUnconfirmedClaims(t *testing.T) {
	cluster := newDNSUnitTestCluster()
	reservation := newDNSUnitTestReservation(cluster)
	reservation.Labels[claimedByClusterUIDLabel] = "other-cluster-uid"
	reconciler := newClusterDNSUnitReconciler(t, cluster, reservation)

	domain, ready, result, err := reconciler.reconcileDNSReservation(context.Background(), cluster)
	if err != nil {
		t.Fatalf("reconcileDNSReservation returned error: %v", err)
	}
	if domain != "" || ready {
		t.Errorf("reconcileDNSReservation() = (%q, %t), want empty domain and not ready", domain, ready)
	}
	if result.RequeueAfter != 5*time.Second {
		t.Errorf("RequeueAfter = %s, want 5s", result.RequeueAfter)
	}
}

func TestReconcileDNSReservationPersistsAndReturnsConfirmedDomain(t *testing.T) {
	cluster := newDNSUnitTestCluster()
	reservation := newDNSUnitTestReservation(cluster)
	reconciler := newClusterDNSUnitReconciler(t, cluster, reservation)
	ctx := context.Background()

	domain, ready, result, err := reconciler.reconcileDNSReservation(ctx, cluster)
	if err != nil {
		t.Fatalf("reconcileDNSReservation returned error while persisting domain: %v", err)
	}
	if domain != "" || ready || result.RequeueAfter != 0 {
		t.Errorf("first reconcile = (%q, %t, %s), want empty domain, not ready, no requeue", domain, ready, result.RequeueAfter)
	}

	var updated hyperfleetv1alpha1.Cluster
	key := client.ObjectKeyFromObject(cluster)
	if err := reconciler.Get(ctx, key, &updated); err != nil {
		t.Fatalf("get Cluster after status update: %v", err)
	}
	if updated.Status.BaseDomain != dnsUnitTestBaseDomain {
		t.Fatalf("Cluster base domain = %q, want %q", updated.Status.BaseDomain, dnsUnitTestBaseDomain)
	}

	domain, ready, result, err = reconciler.reconcileDNSReservation(ctx, &updated)
	if err != nil {
		t.Fatalf("reconcileDNSReservation returned error after status persisted: %v", err)
	}
	if domain != dnsUnitTestBaseDomain || !ready || result.RequeueAfter != 0 {
		t.Errorf("second reconcile = (%q, %t, %s), want confirmed domain, ready, no requeue", domain, ready, result.RequeueAfter)
	}
}

func TestReconcileDNSReservationPropagatesErrors(t *testing.T) {
	t.Run("claim verification error", func(t *testing.T) {
		cluster := newDNSUnitTestCluster()
		reservation := newDNSUnitTestReservation(cluster)
		wantErr := errors.New("reservation listing unavailable")
		reconciler := newClusterDNSUnitReconciler(t, cluster, reservation)
		reconciler.Client = &listFailureClient{Client: reconciler.Client, dnsReservationListErr: wantErr}

		_, _, _, err := reconciler.reconcileDNSReservation(context.Background(), cluster)
		if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "verify Cluster claims") {
			t.Fatalf("reconcileDNSReservation error = %v, want claim verification failure wrapping %v", err, wantErr)
		}
	})

	t.Run("persist base domain error", func(t *testing.T) {
		cluster := newDNSUnitTestCluster()
		reservation := newDNSUnitTestReservation(cluster)
		reconciler := newClusterDNSUnitReconciler(t, reservation)

		_, _, _, err := reconciler.reconcileDNSReservation(context.Background(), cluster)
		if err == nil || !strings.Contains(err.Error(), "persist reservation base domain") {
			t.Fatalf("reconcileDNSReservation error = %v, want base-domain persistence failure", err)
		}
	})
}

func TestReconcileDNSReservationBindsAutomaticReservation(t *testing.T) {
	cluster := newDNSUnitTestCluster()
	cluster.Spec.DNSReservationID = ""
	reservation := &hyperfleetv1alpha1.DNSReservation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      automaticDNSReservationName(cluster.Name, string(cluster.UID)),
			Namespace: cluster.Namespace,
			UID:       types.UID("automatic-reservation-uid"),
			Labels: map[string]string{
				accountIDLabel:           dnsUnitTestAccountID,
				claimedByClusterUIDLabel: string(cluster.UID),
			},
		},
	}
	reconciler := newClusterDNSUnitReconciler(t, cluster, reservation)
	ctx := context.Background()

	var storedCluster hyperfleetv1alpha1.Cluster
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(cluster), &storedCluster); err != nil {
		t.Fatalf("get Cluster before automatic reservation bind: %v", err)
	}
	domain, ready, result, err := reconciler.reconcileDNSReservation(ctx, &storedCluster)
	if err != nil {
		t.Fatalf("reconcileDNSReservation returned error: %v", err)
	}
	if domain != "" || ready || result.RequeueAfter != 0 {
		t.Errorf("reconcileDNSReservation() = (%q, %t, %s), want empty domain, not ready, no requeue", domain, ready, result.RequeueAfter)
	}

	var updated hyperfleetv1alpha1.Cluster
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(cluster), &updated); err != nil {
		t.Fatalf("get Cluster after automatic reservation bind: %v", err)
	}
	if updated.Spec.DNSReservationID != string(reservation.UID) {
		t.Errorf("Cluster DNSReservationID = %q, want %q", updated.Spec.DNSReservationID, reservation.UID)
	}
}

func TestReconcileDNSReservationRejectsAutomaticReservationWithoutAccount(t *testing.T) {
	cluster := newDNSUnitTestCluster()
	cluster.Spec.DNSReservationID = ""
	cluster.Spec.AccountID = ""
	delete(cluster.Labels, accountIDLabel)
	reconciler := newClusterDNSUnitReconciler(t, cluster)

	_, _, _, err := reconciler.reconcileDNSReservation(context.Background(), cluster)
	if err == nil || !strings.Contains(err.Error(), "ensure automatic DNS reservation") {
		t.Fatalf("reconcileDNSReservation error = %v, want automatic reservation setup failure", err)
	}
}

func TestEnsureAutomaticDNSReservationRecoversCreateCollision(t *testing.T) {
	cluster := newDNSUnitTestCluster()
	cluster.Spec.AccountID = "" // The persisted account label is the fallback.
	cluster.Spec.DNSReservationID = ""
	reconciler := newClusterDNSUnitReconciler(t, cluster)
	collisionClient := &automaticDNSReservationCollisionClient{Client: reconciler.Client}
	reconciler.Client = collisionClient
	ctx := context.Background()

	var storedCluster hyperfleetv1alpha1.Cluster
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(cluster), &storedCluster); err != nil {
		t.Fatalf("get Cluster before recovery: %v", err)
	}
	if err := reconciler.ensureAutomaticDNSReservation(ctx, &storedCluster); err != nil {
		t.Fatalf("ensureAutomaticDNSReservation returned error after create collision: %v", err)
	}
	if collisionClient.createCalls != 1 {
		t.Fatalf("automatic reservation create calls = %d, want 1", collisionClient.createCalls)
	}

	var updatedCluster hyperfleetv1alpha1.Cluster
	if err := reconciler.Get(ctx, client.ObjectKeyFromObject(cluster), &updatedCluster); err != nil {
		t.Fatalf("get Cluster after recovery: %v", err)
	}
	if updatedCluster.Spec.DNSReservationID != "raced-reservation-uid" {
		t.Errorf("Cluster DNSReservationID = %q, want raced-reservation-uid", updatedCluster.Spec.DNSReservationID)
	}

	var recovered hyperfleetv1alpha1.DNSReservation
	key := types.NamespacedName{
		Namespace: cluster.Namespace,
		Name:      automaticDNSReservationName(cluster.Name, string(cluster.UID)),
	}
	if err := reconciler.Get(ctx, key, &recovered); err != nil {
		t.Fatalf("get recovered DNSReservation: %v", err)
	}
	if string(recovered.UID) != "raced-reservation-uid" {
		t.Errorf("recovered DNSReservation UID = %q, want raced-reservation-uid", recovered.UID)
	}
	if recovered.Labels[claimedByClusterUIDLabel] != string(cluster.UID) {
		t.Errorf("recovered DNSReservation claim = %q, want Cluster UID %q", recovered.Labels[claimedByClusterUIDLabel], cluster.UID)
	}
}

func TestEnsureAutomaticDNSReservationRejectsInvalidExistingReservation(t *testing.T) {
	tests := []struct {
		name        string
		configure   func(*hyperfleetv1alpha1.DNSReservation)
		wantMessage string
	}{
		{
			name: "reservation belongs to another account",
			configure: func(reservation *hyperfleetv1alpha1.DNSReservation) {
				reservation.Labels[accountIDLabel] = "999999999999"
			},
			wantMessage: "is not claimed by Cluster UID",
		},
		{
			name: "reservation belongs to another Cluster",
			configure: func(reservation *hyperfleetv1alpha1.DNSReservation) {
				reservation.Labels[claimedByClusterUIDLabel] = "other-cluster-uid"
			},
			wantMessage: "is not claimed by Cluster UID",
		},
		{
			name: "reservation has no database UID",
			configure: func(reservation *hyperfleetv1alpha1.DNSReservation) {
				reservation.UID = ""
			},
			wantMessage: "has no database UID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cluster := newDNSUnitTestCluster()
			cluster.Spec.DNSReservationID = ""
			reservation := newDNSUnitTestAutomaticReservation(cluster, "existing-reservation-uid")
			tt.configure(reservation)
			reconciler := newClusterDNSUnitReconciler(t, reservation)

			err := reconciler.ensureAutomaticDNSReservation(context.Background(), cluster)
			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("ensureAutomaticDNSReservation error = %v, want message containing %q", err, tt.wantMessage)
			}
		})
	}
}

func TestEnsureAutomaticDNSReservationReturnsClientErrors(t *testing.T) {
	getFailure := errors.New("automatic reservation read failed")
	t.Run("get failure", func(t *testing.T) {
		cluster := newDNSUnitTestCluster()
		cluster.Spec.DNSReservationID = ""
		reconciler := newClusterDNSUnitReconciler(t, cluster)
		reconciler.Client = &getFailureClient{Client: reconciler.Client, err: getFailure}

		err := reconciler.ensureAutomaticDNSReservation(context.Background(), cluster)
		if !errors.Is(err, getFailure) || !strings.Contains(err.Error(), "get automatic DNSReservation") {
			t.Fatalf("ensureAutomaticDNSReservation error = %v, want wrapped get failure", err)
		}
	})

	createFailure := errors.New("automatic reservation create failed")
	t.Run("create failure", func(t *testing.T) {
		cluster := newDNSUnitTestCluster()
		cluster.Spec.DNSReservationID = ""
		reconciler := newClusterDNSUnitReconciler(t, cluster)
		reconciler.Client = &createFailureClient{Client: reconciler.Client, err: createFailure}

		err := reconciler.ensureAutomaticDNSReservation(context.Background(), cluster)
		if !errors.Is(err, createFailure) || !strings.Contains(err.Error(), "create automatic DNSReservation") {
			t.Fatalf("ensureAutomaticDNSReservation error = %v, want wrapped create failure", err)
		}
	})
}
