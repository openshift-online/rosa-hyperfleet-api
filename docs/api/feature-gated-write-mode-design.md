# Feature-Gated Write-Mode Control

**Status**: Design Proposal
**Author**: Chris Doan
**Date**: 2026-07-09
**Reviewers**: Lucas (reach out for detailed requirements)
**Parent Design**: [API Management](api-management.md)

## Problem Statement

The [API Management](api-management.md) spec defines three independent dimensions for field control:

| Dimension | Marker | Purpose |
|-----------|--------|---------|
| Visibility | `+k8s:openapi-gen=false` | Whether fields appear in OpenAPI/API surface |
| Write Mode | `+hyperfleet:write-mode=X` | Customer mutability (`mutable` / `immutable` / `service-set`) |
| Feature Gating | `+openshift:enable:FeatureGate=X` | Per-customer entitlements based on feature set |

**The limitation**: A field's write-mode is **fixed for all customers**. We cannot vary write-mode based on customer tier or feature gate enablement.

### Primary Use Case

Write-mode control needs to work **independently** of feature gating, not only for fields behind a FeatureGate.

**Scenario**: A field is **GA** (no `+openshift:enable:FeatureGate` marker) but we want to give specific customers the ability to mutate a field that is otherwise immutable or service-set.

**Example 1 — Customer-tier based control**:

- **Standard customers**: `releaseChannel` is `immutable` (set on create, cannot change)
- **Premium customers**: `releaseChannel` is `mutable` (can change anytime)

**Example 2 — Gradual rollout with feature gates**:

- **Default customers** (production): `etcd` is `service-set` (read-only, platform-managed)
- **TechPreview customers** (early adopters): `etcd` is `mutable` (customer-controlled for testing)

Today, we must choose ONE write-mode for all customers, preventing these patterns.

## Proposed Solution

### Follow Existing OpenShift Marker Patterns

Instead of inventing new bracket syntax, follow the existing OpenShift marker conventions that support **multiple arguments**.

**Reference patterns** (from openshift/api):

- `+openshift:validation:FeatureGateAwareEnum:featureGate="MyAwesomeFeature",enum="Val1";"Val2"`
- `+openshift:validation:FeatureGateAwareXValidation` (CEL validation rules conditional on feature gates)

### Proposed Marker: `FeatureGateAwareWriteMode`

Add a new marker that allows different write-modes based on feature gate state:

```go
type ClusterSpec struct {
    // Simple case: no feature gate, fixed write-mode
    // +hyperfleet:write-mode=mutable
    DisplayName string `json:"displayName"`

    // GA field (no FeatureGate marker) with customer-tier-based write-mode control
    // Default: immutable for all customers
    // Override: mutable when MyPremiumFeature gate is enabled
    // +hyperfleet:write-mode=immutable
    // +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="MyPremiumFeature",writeMode="mutable"
    ReleaseChannel string `json:"releaseChannel"`

    // Gated field with different write-modes per feature set
    // Default: service-set (platform-managed)
    // TechPreview+: mutable (customer-controlled)
    // +hyperfleet:write-mode=service-set
    // +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="HyperFleetEtcdConfig",writeMode="mutable"
    // +openshift:enable:FeatureGate=HyperFleetEtcdConfig
    Etcd *EtcdSpec `json:"etcd,omitempty"`
}
```

**Syntax summary**:

| Element | Marker | Notes |
|---------|--------|-------|
| Base mode (fallback) | `+hyperfleet:write-mode=X` | Used when no gate overrides match |
| Gate-aware override | `+hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="GateName",writeMode="X"` | Multiple overrides allowed |
| Valid write modes | `mutable`, `immutable`, `service-set` | Same as base write-mode values |

Overrides can be more permissive (e.g., `immutable` -> `mutable` for premium) or more restrictive (e.g., `mutable` -> `immutable` for compliance).

### Data Model Changes

**Current FieldMeta** (from [API Management](api-management.md)):

```go
type FieldMeta struct {
    FieldPath   string
    WriteMode   WriteMode
    FeatureGate string
    Hidden      bool
}
```

**Proposed FieldMeta**:

```go
type FieldMeta struct {
    FieldPath                  string
    WriteMode                  WriteMode                          // Base mode (fallback)
    FeatureGate                string                             // Gate required for visibility
    Hidden                     bool
    FeatureGateAwareWriteModes []FeatureGateWriteMode `json:"featureGateAwareWriteModes,omitempty"`
}

type FeatureGateWriteMode struct {
    FeatureGate string    // Gate name; empty string = default (no gates enabled)
    WriteMode   WriteMode
}
```

**JSON Registry Examples**:

GA field with customer-tier control:

```json
{
  "fieldPath": "spec.releaseChannel",
  "writeMode": "immutable",
  "featureGateAwareWriteModes": [
    { "featureGate": "MyPremiumFeature", "writeMode": "mutable" }
  ]
}
```

Gated field with different write-modes:

```json
{
  "fieldPath": "spec.etcd",
  "writeMode": "service-set",
  "featureGate": "HyperFleetEtcdConfig",
  "featureGateAwareWriteModes": [
    { "featureGate": "HyperFleetEtcdConfig", "writeMode": "mutable" }
  ]
}
```

## Implementation Plan

### Phase 1: Marker Parsing

**File**: `pkg/markers/scanner.go`

Update `extractMarkers()` to recognize multi-argument FeatureGateAwareWriteMode markers:

```go
var featureGateAwareWriteModePattern = regexp.MustCompile(
    `\+hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="([^"]*)",writeMode="(mutable|immutable|service-set)"`,
)

func (s *MarkerScanner) extractMarkers(field *ast.Field, fieldPath string) *FieldMeta {
    // ... existing code ...

    var gatedModes []FeatureGateWriteMode
    for _, match := range featureGateAwareWriteModePattern.FindAllStringSubmatch(comments, -1) {
        gatedModes = append(gatedModes, FeatureGateWriteMode{
            FeatureGate: match[1],
            WriteMode:   WriteMode(match[2]),
        })
    }

    if len(gatedModes) > 0 {
        meta.FeatureGateAwareWriteModes = gatedModes
    }
}
```

### Phase 2: Registry Generation

**Files**: `pkg/markers/generator.go`, `pkg/markers/json.go`

Update templates to include gate-aware write-modes:

```go
type templateField struct {
    FieldPath       string
    WriteMode       string
    FeatureGate     string
    Hidden          bool
    GatedWriteModes map[string]string
}
```

### Phase 3: Runtime Validation

**File**: `pkg/validation/validator.go`

Update `validateWriteMode()` to resolve effective write-mode from enabled gates:

```go
func (v *Validator) validateWriteMode(fieldPath string, meta registry.FieldMeta, req *Request) error {
    effectiveMode := meta.WriteMode // Default fallback

    for _, override := range meta.FeatureGateAwareWriteModes {
        if req.IsFeatureGateEnabled(override.FeatureGate) {
            effectiveMode = override.WriteMode
            break // First matching gate wins
        }
    }

    switch effectiveMode {
    case registry.ServiceSet:
        return &ValidationError{
            FieldPath: fieldPath,
            Reason:    "field is platform-managed (service-set) for your account tier",
        }
    // ... rest of validation
    }
}
```

The validation Request needs a new method:

```go
type Request struct {
    // ... existing fields ...
    EnabledFeatureGates []string
}

func (r *Request) IsFeatureGateEnabled(gateName string) bool {
    for _, gate := range r.EnabledFeatureGates {
        if gate == gateName {
            return true
        }
    }
    return false
}
```

### Phase 4: Testing

**File**: `pkg/validation/validator_test.go`

| Test Case | Field | Feature Set | Operation | Expected |
|-----------|-------|-------------|-----------|----------|
| service-set blocks Default customers | `spec.etcd` | Default | Create | Error: service-set |
| mutable allows TechPreview customers | `spec.etcd` | TechPreviewNoUpgrade | Create | Success |
| immutable blocks update for standard tier | `spec.releaseChannel` | Default | Update | Error: immutable |
| mutable allows update for premium tier | `spec.releaseChannel` | MyPremiumFeature enabled | Update | Success |

## Examples

### Example 1: GA Field with Customer-Tier Write-Mode Control

```go
// GA field (no FeatureGate marker) with different write-modes by customer tier
// Standard customers: immutable (set on create only)
// Premium customers (with MyPremiumFeature gate): mutable
// +hyperfleet:write-mode=immutable
// +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="MyPremiumFeature",writeMode="mutable"
ReleaseChannel string `json:"releaseChannel"`
```

### Example 2: Gated Field with Progressive Write-Mode Rollout

```go
// Field behind feature gate with different write-modes
// Default: service-set (platform-managed, read-only)
// TechPreview+: mutable (customer-controlled for testing)
// +hyperfleet:write-mode=service-set
// +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="HyperFleetEtcdConfig",writeMode="mutable"
// +openshift:enable:FeatureGate=HyperFleetEtcdConfig
Etcd *EtcdSpec `json:"etcd,omitempty"`
```

### Example 3: Simple Case (No Gate-Aware Control)

```go
// Works today, continues to work unchanged
// +hyperfleet:write-mode=mutable
Tags map[string]string `json:"tags,omitempty"`
```

### Example 4: Multiple Gate-Based Overrides

```go
// Different write-modes for different gates
// No gates: immutable (standard tier)
// BetaFeature1: mutable (beta testers)
// PremiumFeature: mutable (premium tier)
// +hyperfleet:write-mode=immutable
// +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="BetaFeature1",writeMode="mutable"
// +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="PremiumFeature",writeMode="mutable"
AdvancedConfig *Config `json:"advancedConfig,omitempty"`
```

### Example 5: Restrictive Override (Compliance Lock-Down)

```go
// Field is mutable by default, but becomes immutable under compliance gate
// Default: mutable (customers can change freely)
// StrictComplianceMode: immutable (locked down for regulated environments)
// +hyperfleet:write-mode=mutable
// +hyperfleet:validation:FeatureGateAwareWriteMode:featureGate="StrictComplianceMode",writeMode="immutable"
AuditLogDestination string `json:"auditLogDestination"`
```

## Migration Strategy

### Backward Compatibility

- No breaking changes to current markers
- New `FeatureGateAwareWriteMode` syntax is opt-in
- Empty `FeatureGateAwareWriteModes` slice treated as "no overrides"
- Existing fields without gated write-modes continue to work unchanged

### Rollout

1. Merge code changes
2. Update documentation
3. Add gated write-modes to specific fields as needed (opt-in)
4. Monitor validation metrics

No mass migration required — fields adopt gated modes incrementally.

## Trade-offs and Alternatives

| Alternative | Description | Why Rejected |
|-------------|-------------|--------------|
| Bracket syntax | `+hyperfleet:write-mode[FeatureSetName]=mutable` | Invents new syntax; OpenShift API already has established multi-argument marker patterns |
| Separate fields per tier | `releaseChannelStandard`, `releaseChannelPremium` | API surface explosion, confusing for customers, doesn't scale |
| Runtime-only config | Env vars or DB config to control write-mode | Not declarative, harder to audit, no OpenAPI integration |
| Extend FeatureSet enum | `PremiumDefault`, `PremiumTechPreview`, etc. | Feature sets are for API stability tiers, not subscription tiers; mixing concerns |

### Chosen Approach: FeatureGateAwareWriteMode

**Pros**:

- Follows existing OpenShift API patterns (`FeatureGateAwareEnum`, `FeatureGateAwareXValidation`)
- Declarative (visible in code)
- Works independently of feature gating (GA fields can use it)
- Backward compatible (optional marker)
- Scales to many fields and multiple gates

**Cons**:

- More verbose than bracket syntax
- Requires parser changes
- Validation logic more complex (must check enabled gates)
- Need to track customer's enabled feature gates at runtime

## Open Questions

1. **Marker name**: Is `FeatureGateAwareWriteMode` the right name?
   - Current: `+hyperfleet:validation:FeatureGateAwareWriteMode`
   - Alternative: `+hyperfleet:FeatureGateAwareWriteMode` (shorter)

2. **Multiple gate behavior**: If a customer has multiple gates enabled, which write-mode wins?
   - Recommendation: First specific match in marker order (earliest in source code)
   - Alternative: Most permissive (`mutable` > `immutable` > `service-set`)

3. **Validation error messages**: How detailed should errors be for gate-aware rejections?
   - Recommendation: "field is {write-mode} for your account tier" (don't expose gate names to customers)

4. **CRD variant generation**: Should CRD YAML show different write-modes per variant?
   - Recommendation: Not initially — CRDs show schema, not validation rules. This is runtime validation.

5. **Migration path**: Should we auto-migrate existing fields or require explicit opt-in?
   - Recommendation: Explicit opt-in only. No automatic migration.

6. **Customer gate enablement**: How do we determine which gates are enabled for a customer at runtime?
   - Option A: Lookup from database based on subscription tier
   - Option B: Include in request authentication/authorization context
   - Option C: Explicit parameter in Platform API request

## References

- [API Management Design](api-management.md) — parent design spec for the field control system
- **Implementation targets**: `pkg/markers/`, `pkg/validation/`, `pkg/featuregate/`
- [OpenShift API codegen](https://github.com/openshift/api/tree/master/tools/codegen) — upstream marker patterns

---

<!-- Generation prompt: review rosa-hyperfleet#678, adapt for rosa-hyperfleet-api docs/api/, cross-reference api-management.md, apply style rules (tables for structured field lists, ASCII art for digraphs, bullet points in tables, relative paths from docs/). -->
