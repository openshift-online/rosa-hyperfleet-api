# Networking Defaults Implementation

## Overview

Default values for `hostedcluster.networking` fields are now managed using Kubernetes best practices with `+kubebuilder:default` markers and code generation.

## Architecture

### Single Source of Truth: Type Markers

Default values are defined once using `+kubebuilder:default` markers in the API types:

```go
// api/v1alpha1/cluster_networking_types.go
type ClusterNetworking struct {
    // +kubebuilder:default={{cidr: "10.0.0.0/16"}}
    MachineNetwork []hypershiftv1beta1.MachineNetworkEntry `json:"machineNetwork,omitempty"`
    
    // +kubebuilder:default={{cidr: "10.132.0.0/14"}}
    ClusterNetwork []hypershiftv1beta1.ClusterNetworkEntry `json:"clusterNetwork,omitempty"`
    
    // +kubebuilder:default={{cidr: "172.31.0.0/16"}}
    ServiceNetwork []hypershiftv1beta1.ServiceNetworkEntry `json:"serviceNetwork,omitempty"`
    
    // +kubebuilder:default="OVNKubernetes"
    NetworkType hypershiftv1beta1.NetworkType `json:"networkType,omitempty"`
}
```

### Code Generation Flow

```
+kubebuilder:default markers in api/v1alpha1/cluster_networking_types.go
    ↓
default-gen scans markers
    ↓
Generates api/v1alpha1/public/zz_generated.defaults.go (constants only)
    ↓
Used by api/v1alpha1/public/defaults.go (manually maintained SetDefaults functions)
```

## Default Values

| Field | Default Value | Description |
|-------|--------------|-------------|
| `networkType` | `OVNKubernetes` | SDN provider |
| `clusterNetwork` | `10.132.0.0/14` | Pod IP CIDR |
| `serviceNetwork` | `172.31.0.0/16` | Service IP CIDR |
| `machineNetwork` | `10.0.0.0/16` | Machine IP CIDR |

## Usage

### Client-Side Defaulting

```go
import "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"

spec := &public.HostedClusterSpecPassthrough{
    Networking: hypershiftv1beta1.ClusterNetworking{
        // Only specify what you want to override
        NetworkType: hypershiftv1beta1.OVNKubernetes,
    },
}

// Apply defaults to fill in missing values
public.SetHostedClusterSpecDefaults(spec)

// Now spec.Networking has all CIDRs filled in with defaults
```

### Server-Side Defaulting

The server (hyperfleet-operator) also applies these same defaults at `hyperfleet-operator/internal/render/cluster.go:300-314`.

## Code Generation

### Build the Generator

```bash
make build-api-codegen
```

### Generate Defaults

```bash
make generate-defaults
```

### Verify Defaults

```bash
make verify-defaults
```

The verify step ensures the generated file matches the markers.

## Files

### Source Files
- `api/v1alpha1/cluster_networking_types.go` - Type definitions with `+kubebuilder:default` markers

### Generated Files  
- `api/v1alpha1/public/zz_generated.defaults.go` - Generated constants from markers

### Manual Files
- `api/v1alpha1/public/defaults.go` - SetDefaults functions (uses generated constants)
- `api/v1alpha1/public/defaults_test.go` - Tests for defaulting

### Code Generation
- `hack/api-codegen/cmd/default-gen/main.go` - Generator entry point
- `hack/api-codegen/pkg/defaults/generator.go` - Generator implementation

## Maintenance

### Adding New Defaults

1. Add `+kubebuilder:default` marker to the field in `api/v1alpha1/` types
2. Run `make generate-defaults`
3. Update `defaults.go` SetDefaults function if needed
4. Run `make verify-defaults` to ensure generated code is committed

### Modifying Existing Defaults

1. Update the `+kubebuilder:default` marker value
2. Run `make generate-defaults`
3. Run `make verify-defaults`
4. Tests will catch any incompatible changes

## Benefits

✅ **Single Source of Truth** - Defaults defined once in type markers  
✅ **Kubernetes Best Practice** - Follows controller-gen/kubebuilder patterns  
✅ **Automatic Generation** - Part of `make generate` workflow  
✅ **CI Verification** - `make verify` catches outdated generated code  
✅ **Type-Safe** - Generated Go constants, not string magic  
✅ **Client-Side Optional** - Server always applies defaults regardless

## See Also

- [Upstream HyperShift defaults](https://github.com/openshift/hypershift/blob/main/api/hypershift/v1beta1/hostedcluster_types.go) - Uses same pattern
- [controller-gen markers](https://book.kubebuilder.io/reference/markers.html) - Kubebuilder marker reference
