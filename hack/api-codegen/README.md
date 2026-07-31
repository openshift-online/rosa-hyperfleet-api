# api-codegen

![Coverage](https://img.shields.io/badge/coverage-31.3%25-yellow)

Build-time code generators for the ROSA Hyperfleet API. These tools scan Go types with markers and generate passthrough types, OpenAPI schemas, CRD variants, conversion functions, and field validation metadata.

## Generators

| Command | Purpose |
|---------|---------|
| `passthrough-gen` | Generate passthrough struct types from HyperShift API types with `+hyperfleet:` markers |
| `marker-scanner` | Extract marker metadata into `field_metadata.go` / `field_metadata.json` (field registry) |
| `openapi-gen` | Generate Swagger 2.0 JSON schemas from annotated Go types (respects `+k8s:openapi-gen=false`) |
| `openapi-merge` | Merge generated schemas into the hand-written `openapi.yaml` (Swagger 2.0 → OAS 3.0 conversion) |
| `conversion-gen` | Generate conversion functions between API versions |
| `crd-variants` | Produce CRD variants filtered by feature gates |
| `featuregate-info` | Emit feature gate metadata for CRD fields |
| `verify-configuration` | Validate marker consistency across types |

## Codegen pipeline

The generators are chained in a dependency order. Each phase builds on the output of the previous one.

```text
Phase 0: Port codegen tools into monorepo (ROSAENG-62606)
Phase 1: passthrough-gen → marker-scanner (ROSAENG-61801)
           Generates typed passthrough structs with markers,
           then extracts field_metadata.json registry.
Phase 2: Field validation middleware (ROSAENG-61802)
           Uses field_metadata.json to enforce write-mode
           (mutable/immutable/service-set) and feature gates
           on create/update requests.
Phase 3: Conversion functions (ROSAENG-61803)
           Typed service-set injection and preservation functions
           replace hardcoded field assignment in handlers.
Phase 5: OpenAPI alignment (ROSAENG-61805)
           openapi-gen → openapi-merge pipeline generates typed
           schemas for CRD types and merges them into openapi.yaml.
Phase 6: CI verification (ROSAENG-61806)
           Wire codegen checks into CI.
```

## Makefile targets

```bash
# Build
make build-api-codegen       # Build all 8 generator binaries
make test-api-codegen        # Run tests
make coverage-api-codegen    # Generate coverage report

# Codegen
make codegen-passthrough     # Run passthrough-gen (typed passthrough structs)
make codegen-registry        # Run marker-scanner (field_metadata.go/.json)
make codegen-openapi         # Run openapi-gen + openapi-merge (update openapi.yaml)
make codegen-verify          # Verify codegen packages compile

# Verification
make verify-openapi          # Verify openapi.yaml matches generated schemas

# API docs
make swagger-ui-serve        # Serve Swagger UI locally on port 8080 (requires Python 3)
make swagger-ui-open         # Open Swagger UI in browser
```

## Key markers

| Marker | Purpose |
|--------|---------|
| `+hyperfleet:write-mode=mutable` | Field can be set on create and updated |
| `+hyperfleet:write-mode=immutable` | Field can be set on create but not changed |
| `+hyperfleet:write-mode=service-set` | Platform-managed field, rejected if customer sets it |
| `+k8s:openapi-gen=false` | Exclude field from generated OpenAPI schemas |
| `+openshift:enable:FeatureGate=X` | Field requires feature gate X to be enabled |

## How openapi-merge works

1. `openapi-gen` scans `hyperfleet-operator/api/v1alpha1` and emits Swagger 2.0 JSON with definitions for all visible types (fields marked `+k8s:openapi-gen=false` are excluded).
2. `openapi-merge` reads that JSON and:
   - Converts `$ref` paths from `#/definitions/X` to `#/components/schemas/X`
   - Strips `+hyperfleet:*` and `+kubebuilder:*` marker lines from descriptions
   - Links `ClusterSpec.hostedCluster` to `HostedClusterSpecPassthrough`
   - Inlines self-referential `$ref`s (e.g. `NodePoolSpec.nodePool`)
   - Replaces 6 schema entries in `platform-api/openapi/openapi.yaml`: `ClusterSpec`, `NodePoolSpec`, `ClusterConfiguration`, `KubeletConfig`, `MachineConfigSpec`, `HostedClusterSpecPassthrough`
3. The pipeline is idempotent — running `make codegen-openapi` twice produces no diff.

## OpenAPI hybrid model

The `platform-api/openapi/openapi.yaml` uses a hybrid approach:
- **Codegen-owned**: CRD spec schemas (ClusterSpec, NodePoolSpec, sub-types) — generated from Go types with markers
- **Human-owned**: Routes, request/response envelopes, non-CRD schemas (Error, Cluster, NodePool wrapper types, etc.)
