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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/silence"
)

func newSilenceReconciler(t *testing.T, cluster *hyperfleetv1alpha1.Cluster, fakeSilence *silence.FakeClient, clock time.Time) *SilenceReconciler {
	t.Helper()

	scheme := runtime.NewScheme()
	if err := hyperfleetv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}

	return &SilenceReconciler{
		Client:        fake.NewClientBuilder().WithScheme(scheme).WithObjects(cluster).WithStatusSubresource(cluster).Build(),
		SilenceClient: fakeSilence,
		Clock:         func() time.Time { return clock },
	}
}

func reconcileCluster(t *testing.T, reconciler *SilenceReconciler, namespace, name string) {
	t.Helper()
	if _, err := reconciler.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: namespace, Name: name},
	}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
}

func clusterWithPhase(name, namespace string, phase hyperfleetv1alpha1.ClusterPhase) *hyperfleetv1alpha1.Cluster {
	return &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec:   hyperfleetv1alpha1.ClusterSpec{DisplayName: name},
		Status: hyperfleetv1alpha1.ClusterStatus{Phase: phase},
	}
}

func TestSilenceReconcilerProvisioning(t *testing.T) {
	t.Parallel()

	const (
		clusterName = "silence-test-cluster"
		testNS      = "cluster-silence-test-id"
	)

	cluster := clusterWithPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseProvisioning)
	fakeSilence := silence.NewFakeClient()
	reconciler := newSilenceReconciler(t, cluster, fakeSilence, time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC))

	ctx := context.Background()
	reconcileCluster(t, reconciler, testNS, clusterName)

	identity := silence.ClusterIdentity{Namespace: testNS, Name: clusterName}
	silences, err := fakeSilence.List(ctx, identity)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(silences) != 1 {
		t.Fatalf("expected 1 silence, got %d", len(silences))
	}
	if !silence.MatchesReason(silences[0], silence.ReasonInstalling) {
		t.Fatalf("unexpected comment: %s", silences[0].Comment)
	}
	if len(silences[0].Matchers) != 3 {
		t.Fatalf("expected install exemption matcher, got %d matchers", len(silences[0].Matchers))
	}
}

func TestSilenceReconcilerWaitingForPlacement(t *testing.T) {
	t.Parallel()

	const (
		clusterName = "silence-wfp-cluster"
		testNS      = "cluster-silence-wfp-id"
	)

	cluster := clusterWithPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseWaitingForPlacement)
	fakeSilence := silence.NewFakeClient()
	reconciler := newSilenceReconciler(t, cluster, fakeSilence, time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC))

	ctx := context.Background()
	reconcileCluster(t, reconciler, testNS, clusterName)

	identity := silence.ClusterIdentity{Namespace: testNS, Name: clusterName}
	silences, err := fakeSilence.List(ctx, identity)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(silences) != 1 || !silence.MatchesReason(silences[0], silence.ReasonInstalling) {
		t.Fatalf("unexpected silences: %+v", silences)
	}
}

func TestSilenceReconcilerReadyExpiresSilence(t *testing.T) {
	t.Parallel()

	const (
		clusterName = "silence-ready-cluster"
		testNS      = "cluster-silence-ready-id"
	)

	cluster := clusterWithPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseReady)
	fakeSilence := silence.NewFakeClient()
	reconciler := newSilenceReconciler(t, cluster, fakeSilence, time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC))

	ctx := context.Background()
	identity := silence.ClusterIdentity{Namespace: testNS, Name: clusterName}
	if _, err := fakeSilence.Create(ctx, silence.BuildPostableSilence(identity, silence.ReasonInstalling, time.Now().UTC(), silence.DefaultTTL)); err != nil {
		t.Fatalf("seed silence: %v", err)
	}

	reconcileCluster(t, reconciler, testNS, clusterName)

	silences, err := fakeSilence.List(ctx, identity)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(silences) != 0 {
		t.Fatalf("expected silences removed, got %d", len(silences))
	}
}

func TestSilenceReconcilerDeleting(t *testing.T) {
	t.Parallel()

	const (
		clusterName = "silence-delete-cluster"
		testNS      = "cluster-silence-delete-id"
	)

	now := metav1.Now()
	cluster := &hyperfleetv1alpha1.Cluster{
		ObjectMeta: metav1.ObjectMeta{
			Name:              clusterName,
			Namespace:         testNS,
			DeletionTimestamp: &now,
			Finalizers:        []string{clusterFinalizer},
		},
		Spec:   hyperfleetv1alpha1.ClusterSpec{DisplayName: clusterName},
		Status: hyperfleetv1alpha1.ClusterStatus{Phase: hyperfleetv1alpha1.ClusterPhaseDeleting},
	}

	fakeSilence := silence.NewFakeClient()
	reconciler := newSilenceReconciler(t, cluster, fakeSilence, time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC))

	ctx := context.Background()
	reconcileCluster(t, reconciler, testNS, clusterName)

	identity := silence.ClusterIdentity{Namespace: testNS, Name: clusterName}
	silences, err := fakeSilence.List(ctx, identity)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(silences) != 1 {
		t.Fatalf("expected 1 silence, got %d", len(silences))
	}
	if !silence.MatchesReason(silences[0], silence.ReasonDeleting) {
		t.Fatalf("unexpected comment: %s", silences[0].Comment)
	}
	if len(silences[0].Matchers) != 2 {
		t.Fatalf("expected no install exemption, got %d matchers", len(silences[0].Matchers))
	}
}

func TestSilenceReconcilerRenewal(t *testing.T) {
	t.Parallel()

	const (
		clusterName = "silence-renew-cluster"
		testNS      = "cluster-silence-renew-id"
	)

	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cluster := clusterWithPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseProvisioning)
	fakeSilence := silence.NewFakeClient()
	reconciler := newSilenceReconciler(t, cluster, fakeSilence, now)

	ctx := context.Background()
	identity := silence.ClusterIdentity{Namespace: testNS, Name: clusterName}
	oldID, err := fakeSilence.Create(ctx, silence.BuildPostableSilence(identity, silence.ReasonInstalling, now.Add(-5*time.Hour-time.Minute), silence.DefaultTTL))
	if err != nil {
		t.Fatalf("seed silence: %v", err)
	}

	reconcileCluster(t, reconciler, testNS, clusterName)

	silences, err := fakeSilence.List(ctx, identity)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(silences) != 1 {
		t.Fatalf("expected 1 active silence after renewal, got %d", len(silences))
	}
	if silences[0].ID == oldID {
		t.Fatalf("expected renewed silence ID, still have %s", oldID)
	}
}

func TestSilenceReconcilerDuplicateCleanup(t *testing.T) {
	t.Parallel()

	const (
		clusterName = "silence-dup-cluster"
		testNS      = "cluster-silence-dup-id"
	)

	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cluster := clusterWithPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseProvisioning)
	fakeSilence := silence.NewFakeClient()
	reconciler := newSilenceReconciler(t, cluster, fakeSilence, now)

	ctx := context.Background()
	identity := silence.ClusterIdentity{Namespace: testNS, Name: clusterName}
	if _, err := fakeSilence.Create(ctx, silence.BuildPostableSilence(identity, silence.ReasonInstalling, now, silence.DefaultTTL)); err != nil {
		t.Fatalf("seed silence 1: %v", err)
	}
	if _, err := fakeSilence.Create(ctx, silence.BuildPostableSilence(identity, silence.ReasonInstalling, now, silence.DefaultTTL)); err != nil {
		t.Fatalf("seed silence 2: %v", err)
	}

	reconcileCluster(t, reconciler, testNS, clusterName)

	silences, err := fakeSilence.List(ctx, identity)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(silences) != 1 {
		t.Fatalf("expected duplicate cleanup to leave 1 silence, got %d", len(silences))
	}
}

func TestSilenceReconcilerRenewalExpireFailureRequeues(t *testing.T) {
	t.Parallel()

	const (
		clusterName = "silence-renew-expire-cluster"
		testNS      = "cluster-silence-renew-expire-id"
	)

	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	cluster := clusterWithPhase(clusterName, testNS, hyperfleetv1alpha1.ClusterPhaseProvisioning)
	fakeSilence := silence.NewFakeClient()
	oldID, err := fakeSilence.Create(context.Background(), silence.BuildPostableSilence(
		silence.ClusterIdentity{Namespace: testNS, Name: clusterName},
		silence.ReasonInstalling,
		now.Add(-5*time.Hour-time.Minute),
		silence.DefaultTTL,
	))
	if err != nil {
		t.Fatalf("seed silence: %v", err)
	}
	fakeSilence.ExpireHook = func(id string) error {
		if id == oldID {
			return fmt.Errorf("expire failed")
		}
		return nil
	}

	reconciler := newSilenceReconciler(t, cluster, fakeSilence, now)
	result, err := reconciler.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: testNS, Name: clusterName},
	})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if result.RequeueAfter != time.Minute {
		t.Fatalf("expected 1m requeue after expire failure, got %s", result.RequeueAfter)
	}

	silences, err := fakeSilence.List(context.Background(), silence.ClusterIdentity{Namespace: testNS, Name: clusterName})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(silences) != 2 {
		t.Fatalf("expected replacement silence to remain while old expire retries, got %d", len(silences))
	}
}
