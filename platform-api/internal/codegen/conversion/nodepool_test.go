package conversion

import (
	"testing"

	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"
)

func TestPreserveNodePoolServiceSet(t *testing.T) {
	snapshot := &v1alpha1.NodePoolSpec{
		AccountID:      "acct-001",
		InternalPoolID: "pool-xyz",
		DisplayName:    "original-name",
	}

	updated := &v1alpha1.NodePoolSpec{
		DisplayName: "updated-name",
	}

	PreserveNodePoolServiceSet(updated, snapshot)

	if updated.AccountID != "acct-001" {
		t.Errorf("expected AccountID restored, got %q", updated.AccountID)
	}
	if updated.InternalPoolID != "pool-xyz" {
		t.Errorf("expected InternalPoolID restored, got %q", updated.InternalPoolID)
	}
	if updated.DisplayName != "updated-name" {
		t.Errorf("expected DisplayName preserved, got %q", updated.DisplayName)
	}
}
