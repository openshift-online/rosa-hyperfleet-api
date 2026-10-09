package validation

import (
	"strings"
	"testing"
)

func TestValidateClusterName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "cluster-1"},
		{name: "a"},
		{name: "", wantErr: true},
		{name: "Cluster", wantErr: true},
		{name: "cluster.name", wantErr: true},
		{name: strings.Repeat("a", 64), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClusterName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateClusterName(%q) error = %v, wantErr %t", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestValidateResourceName(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{name: "my-cluster.workers"},
		{name: "cluster"},
		{name: "", wantErr: true},
		{name: "Cluster.workers", wantErr: true},
		{name: "my_cluster.workers", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateResourceName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateResourceName(%q) error = %v, wantErr %t", tt.name, err, tt.wantErr)
			}
		})
	}
}

func TestParseNodePoolName(t *testing.T) {
	tests := []struct {
		name      string
		wantOwner string
		wantChild string
		wantErr   bool
	}{
		{name: "my-cluster.workers", wantOwner: "my-cluster", wantChild: "workers"},
		{name: "workers", wantErr: true},
		{name: "cluster.workers.extra", wantErr: true},
		{name: "cluster.", wantErr: true},
		{name: "cluster.Workers", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, child, err := ParseNodePoolName(tt.name)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseNodePoolName(%q) error = %v, wantErr %t", tt.name, err, tt.wantErr)
			}
			if err == nil && (owner != tt.wantOwner || child != tt.wantChild) {
				t.Fatalf("ParseNodePoolName(%q) = (%q, %q), want (%q, %q)", tt.name, owner, child, tt.wantOwner, tt.wantChild)
			}
		})
	}
}

func TestValidateAccountNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		accountID string
		wantErr   bool
	}{
		{name: "omitted namespace", accountID: "123456789012"},
		{name: "canonical namespace", namespace: "account-123456789012", accountID: "123456789012"},
		{name: "wrong account namespace", namespace: "account-999999999999", accountID: "123456789012", wantErr: true},
		{name: "legacy cluster namespace", namespace: "cluster-550e8400-e29b-41d4-a716-446655440000", accountID: "123456789012", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateAccountNamespace(tt.namespace, tt.accountID)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateAccountNamespace(%q, %q) error = %v, wantErr %t", tt.namespace, tt.accountID, err, tt.wantErr)
			}
		})
	}
}
