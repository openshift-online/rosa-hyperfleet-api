package markers

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExtractMarkers_FeatureGateAwareWriteMode(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "types.go")

	content := `package test

type Root struct {
	Spec Spec ` + "`json:\"spec\"`" + `
}

type Spec struct {
	// GA field with customer-tier-based write-mode control
	// Standard customers: immutable
	// Premium customers (with gate enabled): mutable
	// +hyperfleet:write-mode=immutable
	// +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="",writeMode="immutable"
	// +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="PremiumFeature",writeMode="mutable"
	ReleaseChannel string ` + "`json:\"releaseChannel\"`" + `

	// TechPreview field - default service-set, mutable when gated
	// +hyperfleet:write-mode=service-set
	// +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="",writeMode="service-set"
	// +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="HyperFleetEtcdConfig",writeMode="mutable"
	// +openshift:enable:FeatureGate=HyperFleetEtcdConfig
	Etcd string ` + "`json:\"etcd\"`" + `
}
`

	if err := os.WriteFile(testFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	scanner := NewScanner([]string{tmpDir})
	if err := scanner.Scan(); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	// Test releaseChannel field
	releaseMeta, found := scanner.Registry["spec.releaseChannel"]
	if !found {
		t.Fatal("spec.releaseChannel not found in registry")
	}

	if releaseMeta.WriteMode != Immutable {
		t.Errorf("Base WriteMode = %v, want %v", releaseMeta.WriteMode, Immutable)
	}

	if len(releaseMeta.FeatureGateAwareWriteModes) != 2 {
		t.Fatalf("Expected 2 gated write modes, got %d", len(releaseMeta.FeatureGateAwareWriteModes))
	}

	// Check default mode (empty gate)
	if releaseMeta.FeatureGateAwareWriteModes[0].FeatureGate != "" {
		t.Errorf("First override FeatureGate = %q, want empty string", releaseMeta.FeatureGateAwareWriteModes[0].FeatureGate)
	}
	if releaseMeta.FeatureGateAwareWriteModes[0].WriteMode != Immutable {
		t.Errorf("First override WriteMode = %v, want %v", releaseMeta.FeatureGateAwareWriteModes[0].WriteMode, Immutable)
	}

	// Check premium mode (with gate)
	if releaseMeta.FeatureGateAwareWriteModes[1].FeatureGate != "PremiumFeature" {
		t.Errorf("Second override FeatureGate = %q, want %q", releaseMeta.FeatureGateAwareWriteModes[1].FeatureGate, "PremiumFeature")
	}
	if releaseMeta.FeatureGateAwareWriteModes[1].WriteMode != Mutable {
		t.Errorf("Second override WriteMode = %v, want %v", releaseMeta.FeatureGateAwareWriteModes[1].WriteMode, Mutable)
	}

	// Test etcd field
	etcdMeta, found := scanner.Registry["spec.etcd"]
	if !found {
		t.Fatal("spec.etcd not found in registry")
	}

	if etcdMeta.WriteMode != ServiceSet {
		t.Errorf("Base WriteMode = %v, want %v", etcdMeta.WriteMode, ServiceSet)
	}

	if etcdMeta.FeatureGate != "HyperFleetEtcdConfig" {
		t.Errorf("FeatureGate = %q, want %q", etcdMeta.FeatureGate, "HyperFleetEtcdConfig")
	}

	if len(etcdMeta.FeatureGateAwareWriteModes) != 2 {
		t.Fatalf("Expected 2 gated write modes, got %d", len(etcdMeta.FeatureGateAwareWriteModes))
	}

	// Check default mode (service-set)
	if etcdMeta.FeatureGateAwareWriteModes[0].FeatureGate != "" {
		t.Errorf("First override FeatureGate = %q, want empty string", etcdMeta.FeatureGateAwareWriteModes[0].FeatureGate)
	}
	if etcdMeta.FeatureGateAwareWriteModes[0].WriteMode != ServiceSet {
		t.Errorf("First override WriteMode = %v, want %v", etcdMeta.FeatureGateAwareWriteModes[0].WriteMode, ServiceSet)
	}

	// Check gated mode (mutable when HyperFleetEtcdConfig enabled)
	if etcdMeta.FeatureGateAwareWriteModes[1].FeatureGate != "HyperFleetEtcdConfig" {
		t.Errorf("Second override FeatureGate = %q, want %q", etcdMeta.FeatureGateAwareWriteModes[1].FeatureGate, "HyperFleetEtcdConfig")
	}
	if etcdMeta.FeatureGateAwareWriteModes[1].WriteMode != Mutable {
		t.Errorf("Second override WriteMode = %v, want %v", etcdMeta.FeatureGateAwareWriteModes[1].WriteMode, Mutable)
	}
}
