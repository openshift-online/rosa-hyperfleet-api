package silence

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
)

func TestIntentForCluster(t *testing.T) {
	t.Parallel()

	cluster := func(phase hyperfleetv1alpha1.ClusterPhase, deleting bool) *hyperfleetv1alpha1.Cluster {
		c := &hyperfleetv1alpha1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "cluster-id"},
			Status:     hyperfleetv1alpha1.ClusterStatus{Phase: phase},
		}
		if deleting {
			now := metav1.Now()
			c.DeletionTimestamp = &now
		}
		return c
	}

	tests := []struct {
		name    string
		cluster *hyperfleetv1alpha1.Cluster
		want    *Intent
	}{
		{"waiting for placement", cluster(hyperfleetv1alpha1.ClusterPhaseWaitingForPlacement, false), &Intent{Reason: ReasonInstalling}},
		{"provisioning", cluster(hyperfleetv1alpha1.ClusterPhaseProvisioning, false), &Intent{Reason: ReasonInstalling}},
		{"ready", cluster(hyperfleetv1alpha1.ClusterPhaseReady, false), nil},
		{"deleting phase", cluster(hyperfleetv1alpha1.ClusterPhaseDeleting, false), &Intent{Reason: ReasonDeleting}},
		{"deletion timestamp", cluster(hyperfleetv1alpha1.ClusterPhaseReady, true), &Intent{Reason: ReasonDeleting}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IntentForCluster(tt.cluster)
			if tt.want == nil {
				if got != nil {
					t.Fatalf("got %+v, want nil", got)
				}
				return
			}
			if got == nil || got.Reason != tt.want.Reason {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestBuildPostableSilenceInstallExemption(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	identity := ClusterIdentity{Namespace: "cluster-abc", Name: "my-cluster"}

	install := BuildPostableSilence(identity, ReasonInstalling, now, DefaultTTL)
	if len(install.Matchers) != 3 {
		t.Fatalf("expected 3 matchers, got %d", len(install.Matchers))
	}
	exempt := install.Matchers[2]
	if exempt.Name != "alertname" || exempt.Value != InstallExemptAlerts[0] || exempt.IsEqual {
		t.Fatalf("unexpected install exemption matcher: %+v", exempt)
	}

	deleteSilence := BuildPostableSilence(identity, ReasonDeleting, now, DefaultTTL)
	if len(deleteSilence.Matchers) != 2 {
		t.Fatalf("expected 2 matchers for delete, got %d", len(deleteSilence.Matchers))
	}
}

func TestNeedsRenewal(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	active := GettableSilence{
		Status: SilenceStatus{State: "active"},
		EndsAt: now.Add(30 * time.Minute),
	}
	if !NeedsRenewal(active, now) {
		t.Fatal("expected renewal when less than 1h remains")
	}

	fresh := GettableSilence{
		Status: SilenceStatus{State: "active"},
		EndsAt: now.Add(3 * time.Hour),
	}
	if NeedsRenewal(fresh, now) {
		t.Fatal("did not expect renewal when plenty of TTL remains")
	}
}

func TestFakeClientCreateAndExpire(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	client := NewFakeClient()
	identity := ClusterIdentity{Namespace: "cluster-1", Name: "c1"}
	now := time.Now().UTC()

	id, err := client.Create(ctx, BuildPostableSilence(identity, ReasonInstalling, now, DefaultTTL))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	silences, err := client.List(ctx, identity)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(silences) != 1 || silences[0].ID != id {
		t.Fatalf("unexpected silences: %+v", silences)
	}

	if err := client.Expire(ctx, id); err != nil {
		t.Fatalf("expire: %v", err)
	}
	silences, err = client.List(ctx, identity)
	if err != nil {
		t.Fatalf("list after expire: %v", err)
	}
	if len(silences) != 0 {
		t.Fatalf("expected no active silences, got %+v", silences)
	}
}
