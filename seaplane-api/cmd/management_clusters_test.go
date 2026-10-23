package main

import (
	"io"
	"log/slog"
	"testing"

	seaplanev1alpha1 "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/clients/seaplanedb"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestManagementClustersParse(t *testing.T) {
	clusters, err := managementClusters(" mc01:111111111111, mc02:222222222222 ,", "us-east-1")
	if err != nil || len(clusters) != 2 {
		t.Fatalf("clusters = %+v, err = %v", clusters, err)
	}
	if mc := clusters[1]; mc.Name != "mc02" || mc.Spec.AccountID != "222222222222" || mc.Spec.Region != "us-east-1" {
		t.Fatalf("wrong cluster: %+v", mc)
	}
	if clusters, err := managementClusters("", "us-east-1"); err != nil || len(clusters) != 0 {
		t.Fatalf("empty value: %+v %v", clusters, err)
	}
	for _, value := range []string{"mc01", ":111111111111", "mc01:12345"} {
		if _, err := managementClusters(value, "us-east-1"); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestRegisterManagementClustersIdempotent(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := seaplanev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	store := fake.NewClientBuilder().WithScheme(scheme).Build()
	db := seaplanedb.NewClientFrom(store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// A restart registers the same clusters again; existing objects are kept.
	for range 2 {
		clusters, err := managementClusters("mc01:111111111111", "us-east-1")
		if err != nil {
			t.Fatal(err)
		}
		if err := registerManagementClusters(t.Context(), db, clusters, logger); err != nil {
			t.Fatal(err)
		}
	}
	var stored seaplanev1alpha1.ManagementCluster
	if err := store.Get(t.Context(), client.ObjectKey{Name: "mc01"}, &stored); err != nil || stored.Spec.AccountID != "111111111111" || stored.Spec.Region != "us-east-1" {
		t.Fatalf("stored = %+v, err = %v", stored, err)
	}
}
