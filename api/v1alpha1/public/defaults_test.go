package public

import (
	"encoding/json"
	"testing"

	hypershiftv1beta1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

// parseNetworkingJSON parses a JSON string into a ClusterNetworking object for testing
func parseNetworkingJSON(t *testing.T, jsonStr string) *hypershiftv1beta1.ClusterNetworking {
	t.Helper()
	var networking hypershiftv1beta1.ClusterNetworking
	if err := json.Unmarshal([]byte(jsonStr), &networking); err != nil {
		t.Fatalf("failed to parse networking JSON: %v", err)
	}
	return &networking
}

func TestSetNetworkingDefaults(t *testing.T) {
	tests := []struct {
		name                string
		input               *hypershiftv1beta1.ClusterNetworking
		expectedType        hypershiftv1beta1.NetworkType
		expectedClusterCIDR string
		expectedServiceCIDR string
		expectedMachineCIDR string
	}{
		{
			name:                "empty networking gets all defaults",
			input:               &hypershiftv1beta1.ClusterNetworking{},
			expectedType:        DefaultNetworkType,
			expectedClusterCIDR: DefaultClusterNetworkCIDR,
			expectedServiceCIDR: DefaultServiceNetworkCIDR,
			expectedMachineCIDR: DefaultMachineNetworkCIDR,
		},
		{
			name: "custom NetworkType is preserved",
			input: &hypershiftv1beta1.ClusterNetworking{
				NetworkType: hypershiftv1beta1.OpenShiftSDN,
			},
			expectedType:        hypershiftv1beta1.OpenShiftSDN,
			expectedClusterCIDR: DefaultClusterNetworkCIDR,
			expectedServiceCIDR: DefaultServiceNetworkCIDR,
			expectedMachineCIDR: DefaultMachineNetworkCIDR,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			SetNetworkingDefaults(tt.input)

			if tt.input.NetworkType != tt.expectedType {
				t.Errorf("NetworkType = %v, want %v", tt.input.NetworkType, tt.expectedType)
			}

			if len(tt.input.ClusterNetwork) > 0 {
				if tt.input.ClusterNetwork[0].CIDR.String() != tt.expectedClusterCIDR {
					t.Errorf("ClusterNetwork CIDR = %v, want %v", tt.input.ClusterNetwork[0].CIDR.String(), tt.expectedClusterCIDR)
				}
			} else {
				t.Error("ClusterNetwork should have default value")
			}

			if len(tt.input.ServiceNetwork) > 0 {
				if tt.input.ServiceNetwork[0].CIDR.String() != tt.expectedServiceCIDR {
					t.Errorf("ServiceNetwork CIDR = %v, want %v", tt.input.ServiceNetwork[0].CIDR.String(), tt.expectedServiceCIDR)
				}
			} else {
				t.Error("ServiceNetwork should have default value")
			}

			if len(tt.input.MachineNetwork) > 0 {
				if tt.input.MachineNetwork[0].CIDR.String() != tt.expectedMachineCIDR {
					t.Errorf("MachineNetwork CIDR = %v, want %v", tt.input.MachineNetwork[0].CIDR.String(), tt.expectedMachineCIDR)
				}
			} else {
				t.Error("MachineNetwork should have default value")
			}
		})
	}
}

func TestSetNetworkingDefaults_CustomCIDRs(t *testing.T) {
	t.Run("custom ClusterNetwork is preserved", func(t *testing.T) {
		networking := parseNetworkingJSON(t, `{"clusterNetwork":[{"cidr":"192.168.0.0/16"}]}`)
		SetNetworkingDefaults(networking)

		if networking.NetworkType != DefaultNetworkType {
			t.Errorf("NetworkType = %v, want %v", networking.NetworkType, DefaultNetworkType)
		}
		if networking.ClusterNetwork[0].CIDR.String() != "192.168.0.0/16" {
			t.Errorf("ClusterNetwork CIDR = %v, want 192.168.0.0/16", networking.ClusterNetwork[0].CIDR.String())
		}
		if len(networking.ServiceNetwork) == 0 || networking.ServiceNetwork[0].CIDR.String() != DefaultServiceNetworkCIDR {
			t.Errorf("ServiceNetwork should have default CIDR")
		}
	})

	t.Run("custom ServiceNetwork is preserved", func(t *testing.T) {
		networking := parseNetworkingJSON(t, `{"serviceNetwork":[{"cidr":"172.30.0.0/16"}]}`)
		SetNetworkingDefaults(networking)

		if networking.ServiceNetwork[0].CIDR.String() != "172.30.0.0/16" {
			t.Errorf("ServiceNetwork CIDR = %v, want 172.30.0.0/16", networking.ServiceNetwork[0].CIDR.String())
		}
		if len(networking.ClusterNetwork) == 0 || networking.ClusterNetwork[0].CIDR.String() != DefaultClusterNetworkCIDR {
			t.Errorf("ClusterNetwork should have default CIDR")
		}
	})

	t.Run("custom MachineNetwork is preserved", func(t *testing.T) {
		networking := parseNetworkingJSON(t, `{"machineNetwork":[{"cidr":"10.10.0.0/16"}]}`)
		SetNetworkingDefaults(networking)

		if networking.MachineNetwork[0].CIDR.String() != "10.10.0.0/16" {
			t.Errorf("MachineNetwork CIDR = %v, want 10.10.0.0/16", networking.MachineNetwork[0].CIDR.String())
		}
	})
}

func TestSetNetworkingDefaults_NilDoesNotPanic(t *testing.T) {
	var networking *hypershiftv1beta1.ClusterNetworking
	SetNetworkingDefaults(networking)
	// If we get here without panicking, test passes
}

func TestSetHostedClusterSpecDefaults(t *testing.T) {
	t.Run("nil spec does not panic", func(t *testing.T) {
		var spec *HostedClusterSpecPassthrough
		SetHostedClusterSpecDefaults(spec)
		// If we get here without panicking, test passes
	})

	t.Run("empty networking gets defaults", func(t *testing.T) {
		spec := &HostedClusterSpecPassthrough{
			Networking: hypershiftv1beta1.ClusterNetworking{},
		}
		SetHostedClusterSpecDefaults(spec)

		if spec.Networking.NetworkType != DefaultNetworkType {
			t.Errorf("NetworkType = %v, want %v", spec.Networking.NetworkType, DefaultNetworkType)
		}
		if len(spec.Networking.ClusterNetwork) == 0 {
			t.Error("ClusterNetwork should have default value")
		}
		if len(spec.Networking.ServiceNetwork) == 0 {
			t.Error("ServiceNetwork should have default value")
		}
		if len(spec.Networking.MachineNetwork) == 0 {
			t.Error("MachineNetwork should have default value")
		}
	})

	t.Run("custom networking is preserved", func(t *testing.T) {
		networking := parseNetworkingJSON(t, `{"networkType":"OpenShiftSDN","clusterNetwork":[{"cidr":"192.168.0.0/16"}]}`)
		spec := &HostedClusterSpecPassthrough{
			Networking: *networking,
		}
		SetHostedClusterSpecDefaults(spec)

		if spec.Networking.NetworkType != hypershiftv1beta1.OpenShiftSDN {
			t.Errorf("NetworkType = %v, want %v", spec.Networking.NetworkType, hypershiftv1beta1.OpenShiftSDN)
		}
		if len(spec.Networking.ClusterNetwork) != 1 {
			t.Errorf("ClusterNetwork length = %d, want 1", len(spec.Networking.ClusterNetwork))
		}
		if spec.Networking.ClusterNetwork[0].CIDR.String() != "192.168.0.0/16" {
			t.Errorf("ClusterNetwork CIDR = %v, want 192.168.0.0/16", spec.Networking.ClusterNetwork[0].CIDR.String())
		}
	})
}
