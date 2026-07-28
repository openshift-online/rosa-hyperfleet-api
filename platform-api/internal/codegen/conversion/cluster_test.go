package conversion

import (
	"testing"

	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"
	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

func TestInjectClusterServiceSet(t *testing.T) {
	spec := &v1alpha1.ClusterSpec{
		HostedCluster: hypershiftv1beta1.HostedClusterSpec{},
	}

	InjectClusterServiceSet(spec, ClusterServiceSetFields{
		CreatorARN: "arn:aws:iam::123456789:user/test",
		IssuerURL:  "https://oidc.example.com/cluster-abc",
	})

	if spec.CreatorARN != "arn:aws:iam::123456789:user/test" {
		t.Errorf("expected CreatorARN to be set, got %q", spec.CreatorARN)
	}
	if spec.HostedCluster.IssuerURL != "https://oidc.example.com/cluster-abc" {
		t.Errorf("expected IssuerURL to be set, got %q", spec.HostedCluster.IssuerURL)
	}
}

func TestInjectClusterServiceSet_EmptyValues(t *testing.T) {
	spec := &v1alpha1.ClusterSpec{
		CreatorARN: "existing-arn",
		HostedCluster: hypershiftv1beta1.HostedClusterSpec{
			IssuerURL: "existing-url",
		},
	}

	InjectClusterServiceSet(spec, ClusterServiceSetFields{})

	if spec.CreatorARN != "existing-arn" {
		t.Errorf("expected CreatorARN to remain unchanged, got %q", spec.CreatorARN)
	}
	if spec.HostedCluster.IssuerURL != "existing-url" {
		t.Errorf("expected IssuerURL to remain unchanged, got %q", spec.HostedCluster.IssuerURL)
	}
}

func TestPreserveClusterServiceSet(t *testing.T) {
	snapshot := &v1alpha1.ClusterSpec{
		CreatorARN: "arn:aws:iam::123456789:user/creator",
		AccountID:  "acct-001",
		InternalID: "internal-xyz",
		HostedCluster: hypershiftv1beta1.HostedClusterSpec{
			IssuerURL: "https://oidc.example.com/cluster-abc",
		},
	}

	updated := &v1alpha1.ClusterSpec{
		DisplayName:   "updated-name",
		HostedCluster: hypershiftv1beta1.HostedClusterSpec{},
	}

	PreserveClusterServiceSet(updated, snapshot)

	if updated.CreatorARN != "arn:aws:iam::123456789:user/creator" {
		t.Errorf("expected CreatorARN restored, got %q", updated.CreatorARN)
	}
	if updated.AccountID != "acct-001" {
		t.Errorf("expected AccountID restored, got %q", updated.AccountID)
	}
	if updated.InternalID != "internal-xyz" {
		t.Errorf("expected InternalID restored, got %q", updated.InternalID)
	}
	if updated.HostedCluster.IssuerURL != "https://oidc.example.com/cluster-abc" {
		t.Errorf("expected IssuerURL restored, got %q", updated.HostedCluster.IssuerURL)
	}
	if updated.DisplayName != "updated-name" {
		t.Errorf("expected DisplayName preserved, got %q", updated.DisplayName)
	}
}
