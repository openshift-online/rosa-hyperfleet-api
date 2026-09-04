# Platform API Migration to v1alpha1/public Types

## Overview

This document describes the migration of platform-api from custom REST types (`pkg/types/`) to generated K8s-native types (`api/v1alpha1/public/`).

**Ticket:** ROSAENG-64871

## Problem Statement

Platform-api currently maintains two separate type hierarchies:

1. **Storage & Internal**: v1alpha1.Cluster, v1alpha1.NodePool (K8s-style CRD types)
2. **API Response**: types.Cluster, types.NodePool (custom REST types)

This creates:

- **Duplication**: Custom types mirror CRD structure already defined in v1alpha1
- **Inconsistency**: Two different response shapes for the same resource
- **Maintenance burden**: Changes to CRD types require updates in multiple places
- **Loss of K8s semantics**: Custom types flatten K8s metadata into separate fields

## Solution

Replace custom REST types with auto-generated `public` types from `api/v1alpha1/public/`:

```
v1alpha1.Cluster (internal K8s type - already in storage)
    ↓ (Project/Unproject)
public.Cluster (K8s-native public type - new)
    ↓ (REST API)
HTTP Response
```

### Key Benefits

1. **Leverage existing storage**: FleetDB already stores v1alpha1 types with K8s structure
2. **Single source of truth**: CRD types drive both storage and API contracts
3. **K8s alignment**: Responses use standard K8s metadata structure
4. **Type safety**: Generated types guarantee consistency
5. **Future-proof**: Enables kubectl/client-go integration
6. **No data migration**: Storage layer (FleetDB) unchanged

## Current Architecture (Already Using K8s Types)

### Storage Layer

FleetDB (`hyperfleet-db/`) already uses K8s-native types:

```go
// From platform-api/pkg/clients/hyperfleetdb/client.go
scheme := runtime.NewScheme()
hyperfleetv1alpha1.AddToScheme(scheme)
c, cleanup, err := hyperfleetdb.NewClient(hyperfleetdb.Options{
    Scheme: scheme,  // ← Registered with v1alpha1 types
    DSN:    dsn,
})
```

**Database storage** (PostgreSQL):

- Table: `kubernetes_resources`
- Columns: `spec` (JSONB), `status` (JSONB), `metadata` (JSONB)
- GVK identifier: `"hyperfleet.io/v1alpha1/Cluster"`
- Data format: JSON serialization of v1alpha1 types
- Already K8s-native under the hood

**Why this is important:**

- Migration doesn't change storage format
- Cluster and NodePool records already use v1alpha1 structure
- No database schema changes needed
- Complete type consistency: storage and API will both use K8s types

## Architecture

### Before (Current: Custom REST on top of K8s storage)

```
HTTP Request
    ↓
types.ClusterCreateRequest (custom REST format)
    ↓
hyperfleetdb.PlatformCreateToClusterCR()
    ↓
v1alpha1.Cluster (K8s type) → FleetDB (JSONB, GVK: "hyperfleet.io/v1alpha1/Cluster")
    ↓
hyperfleetdb.ClusterCRToPlatform()
    ↓
types.Cluster (custom REST format) → HTTP Response
```

Custom types add conversion layer between REST and storage:

- `types.Cluster` (flat REST fields: id, name, created_at, updated_at)
- `types.Condition` (custom condition type with time.Time)
- `types.APIEndpoint`, `types.PlacementReference`

### After (Target: K8s-native throughout)

```
HTTP Request
    ↓
public.Cluster (K8s-native format)
    ↓
hyperfleetdb.PublicToInternalCluster()
    ↓
v1alpha1.Cluster (K8s type) → FleetDB (JSONB, GVK: "hyperfleet.io/v1alpha1/Cluster")
    ↓
hyperfleetdb.InternalToPublicCluster()
    ↓
public.Cluster (K8s-native format) → HTTP Response
```

No conversion to custom types:

- `public.Cluster` (K8s structure: metadata, spec, status)
- `metav1.Condition` (K8s standard condition type)
- Full ObjectMeta with namespace, uid, labels, timestamps

## Response Structure Change

### Cluster Response

**Before (custom REST):**

```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "name": "my-cluster",
  "target_project_id": "account-123",
  "created_by": "arn:aws:iam::...",
  "oidc_issuer_url": "https://...",
  "generation": 1,
  "resource_version": "12345",
  "created_at": "2026-08-20T09:00:00Z",
  "updated_at": "2026-08-20T10:00:00Z",
  "spec": { ... },
  "status": { ... }
}
```

**After (K8s-native):**

```json
{
  "apiVersion": "hyperfleet.openshift.io/v1alpha1",
  "kind": "Cluster",
  "metadata": {
    "name": "my-cluster",
    "namespace": "cluster-550e8400-e29b-41d4-a716-446655440000",
    "uid": "550e8400-e29b-41d4-a716-446655440000",
    "generation": 1,
    "resourceVersion": "12345",
    "creationTimestamp": "2026-08-20T09:00:00Z",
    "labels": {
      "hyperfleet.io/account-id": "account-123"
    }
  },
  "spec": { ... },
  "status": { ... }
}
```

**Field Mapping:**

- `id` → `metadata.uid`
- `name` → `metadata.name`
- `target_project_id` → `metadata.labels["hyperfleet.io/account-id"]`
- `created_by` → `spec.creatorARN` (service-set, filtered in public response)
- `oidc_issuer_url` → `spec.hostedCluster.issuerURL`
- `generation` → `metadata.generation`
- `resource_version` → `metadata.resourceVersion`
- `created_at` → `metadata.creationTimestamp`
- `updated_at` → computed from metadata

## Implementation Phases

### Phase 1: Conversion Layer

Create wrapper functions that reuse existing Project/Unproject from conversion/v1alpha1:

**New file**: `platform-api/pkg/clients/hyperfleetdb/convert.go`

Functions:

- `PublicToInternalCluster(pub *public.Cluster, accountID, clusterID) *v1alpha1.Cluster`
- `InternalToPublicCluster(cr *v1alpha1.Cluster) *public.Cluster`
- `PublicToInternalNodePool(pub *public.NodePool, accountID, clusterID, poolID) *v1alpha1.NodePool`
- `InternalToPublicNodePool(cr *v1alpha1.NodePool) *public.NodePool`
- `enrichMetadata(meta *metav1.ObjectMeta, resourceID, accountID)` - uses clusterNamespace() and label enrichment
- `syncNodePoolPassthrough(spec *v1alpha1.NodePoolSpec)` - syncs top-level to passthrough
- `syncNodePoolPublic(spec *public.NodePoolSpec)` - syncs passthrough to top-level

**Key design decisions:**

- Reuse existing Project/Unproject functions (already handle service-set filtering)
- Use simple metadata enrichment (no complex reflection or JSON roundtrip)
- Keep NodePool field syncing logic (autoRepair, labels)
- Zero code generation overhead
- Use existing `clusterNamespace()` helper for namespace formatting (handles max length validation)

### Phase 2: Handler Updates

Update handlers to accept/return public types:

**Modified files:**

- `platform-api/pkg/handlers/cluster.go`
- `platform-api/pkg/handlers/nodepool.go`

Changes:

- Create endpoint: Accept `public.Cluster` request body
- Update endpoint: Accept `{spec: public.ClusterSpec}` or raw JSON merge
- All responses: Return `public.Cluster` or `public.NodePool`
- Validation: Works unchanged (operates on spec JSON)
- Enrichment: Set service-set fields (CreatorARN, InternalID, etc.)

### Phase 3: Type Deletion

Remove custom types that are now replaced:

**Deleted files:**

- `platform-api/pkg/types/cluster.go`
- `platform-api/pkg/types/nodepool.go`

### Phase 4: Testing

Update handler tests and add conversion tests:

**Modified files:**

- `platform-api/pkg/handlers/cluster_test.go`
- `platform-api/pkg/handlers/nodepool_test.go`

**New file:**

- `platform-api/pkg/clients/hyperfleetdb/convert_test.go`

Test coverage:

- Metadata enrichment (namespace, UID, labels)
- Service-set field injection and filtering
- NodePool field syncing (both directions)
- Roundtrip conversions
- K8s response structure validation

## Data Storage Impact

### No Migration Required ✅

FleetDB storage layer is **completely unaffected**:

**Database structure (already using K8s types):**

- Stores v1alpha1.Cluster and v1alpha1.NodePool CRD types
- Uses PostgreSQL JSONB columns (spec, status, metadata)
- GVK identifier: `"hyperfleet.io/v1alpha1/Cluster"`
- Scheme registration: v1alpha1 types only (`hyperfleetv1alpha1.AddToScheme()`)

**Why no migration needed:**

- FleetDB already stores the same v1alpha1 types we'll use in public responses
- Migration only changes the API response layer, not storage
- All existing cluster and nodepool records remain valid
- GVK and schema unchanged
- Complete backward compatibility at storage level

## Validation

The validator continues working unchanged:

- Operates on spec JSON (structure-agnostic)
- No changes needed for public types
- All validation rules preserved (immutable, service-set, feature-gates)

## Service-Set Field Handling

Service-set (platform-managed) fields are:

- **Injected** during public→internal conversion (via UnprojectCluster)
- **Filtered** during internal→public conversion (via ProjectCluster)

Examples:

- `spec.accountId` - Platform assigns, never user-visible
- `spec.internalId` - Platform generates, never user-visible
- `spec.creatorARN` - Platform sets, filtered from public responses
- `spec.hostedCluster.pullSecret` - Platform-managed, hidden

## Request Format Change

### Create Cluster

**Before:**

```json
POST /api/v0/clusters
{
  "name": "my-cluster",
  "target_project_id": "account-123",
  "spec": { ... }
}
```

**After (K8s-native):**

```json
POST /api/v0/clusters
{
  "metadata": {
    "name": "my-cluster",
    "labels": {
      "hyperfleet.io/account-id": "account-123"
    }
  },
  "spec": { ... }
}
```

### Update Cluster

**Before:**

```json
PATCH /api/v0/clusters/{id}
{
  "spec": { ... }
}
```

**After (unchanged):**

```json
PATCH /api/v0/clusters/{id}
{
  "spec": { ... }
}
```

## Risk Mitigation

| Risk                        | Mitigation                                                    |
| --------------------------- | ------------------------------------------------------------- |
| Storage incompatibility     | No risk - storage already uses v1alpha1 types; GVK unchanged  |
| Existing data loss          | All existing cluster/nodepool records remain readable         |
| Metadata mapping errors     | Unit tests for namespace/UID/label conversion                 |
| Service-set field leakage   | Project/Unproject verified via integration tests              |
| Validation regression       | Validator is structure-agnostic; full test coverage           |
| Client compatibility        | No external customers yet; internal tools coordinated         |
| Namespace length validation | Use existing `clusterNamespace()` helper (handles max length) |

## Timeline & Success Criteria

### Checklist

- [ ] Phase 1: Conversion layer implemented and tested (convert.go, convert_test.go)
- [ ] Phase 2: Handlers updated (cluster.go, nodepool.go)
- [ ] Phase 3: Old types deleted (pkg/types/)
- [ ] Phase 4: Tests updated (handler tests + conversion tests)
- [ ] Build succeeds: `make build-api`
- [ ] All tests pass: `make test-api`
- [ ] No regressions: Validation, pagination, filtering work correctly
- [ ] Documentation: API docs updated with K8s-native structure
- [ ] Code review: Approved and merged

## References

- **CRD Types**: `api/v1alpha1/cluster_types.go`, `nodepool_types.go`
- **Public Types**: `api/v1alpha1/public/cluster_types.go`, `nodepool_types.go`
- **Conversion**: `platform-api/pkg/conversion/v1alpha1/`
- **Existing Handlers**: `platform-api/pkg/handlers/`
- **Validation**: `platform-api/pkg/validation/field_validator.go`
- **FleetDB**: `hyperfleet-db/` (storage layer, uses v1alpha1.AddToScheme)
- **Current Client**: `platform-api/pkg/clients/hyperfleetdb/client.go` (line 31: `hyperfleetv1alpha1.AddToScheme`)

## Related Issues

- ROSAENG-64871: Migrate platform-api to use v1alpha1/public types
