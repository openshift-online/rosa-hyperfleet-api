# Marker Validator & Suggester - Stage 2 of V2 Passthrough Feature Acceleration Pipeline

## Overview

The **Marker Validator** validates existing marker assignments in the Field Registry and suggests improvements based on field analysis and consistency rules.

This is **Stage 2** of the acceleration pipeline described in `docs/api/acceleration-plan.md`.

## Purpose

While markers already exist in `field_metadata.json`, Stage 2 provides **quality control** by:

1. **Validating** marker assignments against rules and conventions
2. **Detecting** potential marker issues (e.g., mutable IDs, hidden public fields)
3. **Suggesting** improvements for consistency
4. **Identifying** fields that may need marker updates

This ensures marker quality before proceeding to Stage 3 (Test Mapper) and Stage 4 (Codegen).

## Usage

### Quick Start

```bash
# Run marker validation
make accel-validate-markers

# View the validation report
open hack/accelerate/marker-suggester/output/marker-validation-report.md
```

### Manual Usage

```bash
# Activate virtual environment
source hack/accelerate/marker-suggester/.venv/bin/activate

# Run validator
python hack/accelerate/marker-suggester/validate_markers.py --verbose

# Custom paths
python hack/accelerate/marker-suggester/validate_markers.py \
  --ledger ../ledger-builder/output/ledger.csv \
  --output ./output/my-validation-report.md
```

### Clean Up

```bash
make accel-marker-clean  # Remove venv and reports
```

## Validation Rules

The validator checks markers against these rules:

### 1. **Write-Mode Consistency**
- Fields in `spec.hostedCluster.*` (passthrough) should usually be `service-set` or `immutable`
- Exception: Some hostedCluster fields ARE mutable (e.g., `autoNode`, `deleteProtection`)
- **Warning** if mutable field found in unexpected location

### 2. **Hidden Flag Logic**
- `service-set` fields with sensitive names (ARN, key, secret) should be `hidden=true`
- `mutable` fields should usually be public (`hidden=false`) so users can modify them
- **Warning** if mutable field is hidden

### 3. **Immutability Detection**
- Fields ending in `id`, `arn`, `name`, `issuerurl` often suggest immutability
- `service-set` is acceptable for IDs (service creates them)
- **Warning** if field name suggests immutability but marked `mutable`

### 4. **Service-Set Validation**
- `service-set` fields should be in `hostedCluster.*` or platform-specific locations
- **Info** if service-set field found outside typical locations

### 5. **Marker Consistency**
- Similar fields (same suffix like `*LogMaxFiles`) should have consistent markers
- **Info** if similar fields have different write modes

## Report Structure

The validation report (`marker-validation-report.md`) contains:

### Summary Section
- Total fields analyzed
- Issue counts by severity (warnings vs. info)
- Issue breakdown by category

### Validation Issues Section
For each issue:
- **Field path**
- **Category** (write-mode, visibility, immutability, consistency)
- **Issue description**
- **Current state**
- **Suggestion** for improvement
- **Confidence score** (0-100%)

### Suggestions Section
High-level marker improvement recommendations

## Interpreting Results

### Issue Severity

| Severity | Icon | Meaning | Action |
|----------|------|---------|--------|
| **Warning** | ⚠️ | Should review - potential marker issue | Review and consider updating markers |
| **Info** | ℹ️ | Informational - may or may not be an issue | Review if time permits |

### Confidence Scores

- **70-100%**: High confidence - likely a real issue
- **50-70%**: Medium confidence - review recommended
- **30-50%**: Low confidence - informational only

### Common Issues

**Issue:** "Mutable field in hostedCluster (usually service-set)"  
**Meaning:** Field in passthrough path marked mutable  
**Action:** Verify if field should actually be user-mutable. If not, update to `service-set`.

**Issue:** "Field name suggests immutability but marked mutable"  
**Meaning:** Field ending in `id`, `name`, `arn` is mutable  
**Action:** Verify if field changes after creation. If not, update to `immutable` or `service-set`.

**Issue:** "Mutable field is hidden (users can't modify)"  
**Meaning:** User-editable field is not exposed in API  
**Action:** Verify if field should be public. If yes, update to `hidden=false`.

## Fixing Marker Issues

When validation finds issues:

1. **Review the report** - Open `marker-validation-report.md`

2. **Identify real issues** - Focus on warnings with high confidence (>70%)

3. **Update source code** - Modify marker tags in Go code:
   ```go
   // Update in api/v1alpha1/public/*.go or api/v1alpha1/*.go
   
   // Before:
   DisplayName string `json:"displayName"` // +hyperfleet:write-mode=mutable
   
   // After:
   DisplayName string `json:"displayName"` // +hyperfleet:write-mode=immutable
   ```

4. **Regenerate field registry:**
   ```bash
   make codegen-registry
   ```

5. **Re-validate:**
   ```bash
   make accel-validate-markers
   ```

6. **Verify fixes** - Check that warnings are resolved

## Current Validation Results

From latest run (run `make accel-validate-markers` for current state):

- **Total fields:** 177
- **Warnings:** 14 (should review)
- **Info:** 52 (informational)

**Top issue categories:**
- write-mode: 62 issues (mostly mutable fields in hostedCluster)
- immutability: 3 issues (field names suggest immutability)
- consistency: 1 issue (kubelet fields split between mutable/service-set)

## Why This Matters

Correct markers are critical because they:

1. **Control API behavior** - Determine what users can modify
2. **Gate delivery** - Stage 3 classification depends on marker accuracy
3. **Drive codegen** - OpenAPI schema, CRDs, and clientset are generated from markers
4. **Affect security** - Hidden fields protect sensitive data

Bad markers can cause:
- Users unable to modify fields they should control
- Service-managed fields exposed to users
- Inconsistent API behavior across resources

## Development

### Requirements

- Python 3.10+
- `pandas>=2.0.0`

### Project Structure

```
hack/accelerate/marker-suggester/
├── validate_markers.py     # Main validation script
├── requirements.txt        # Python dependencies
├── README.md              # This file
└── output/                # Validation reports
    └── marker-validation-report.md
```

### Extending Validation

To add new validation rules:

1. Add method to `MarkerValidator` class in `validate_markers.py`:
   ```python
   def _validate_my_rule(self):
       for idx, row in self.ledger.iterrows():
           # Your validation logic
           if condition:
               self.issues.append({
                   'field': row['field_path'],
                   'severity': 'warning',  # or 'info'
                   'category': 'my-category',
                   'issue': 'Description',
                   'current': f"Current state",
                   'suggestion': "What to do",
                   'confidence': 0.7
               })
   ```

2. Call from `validate_all()`:
   ```python
   def validate_all(self):
       self._validate_write_mode_consistency()
       # ... existing validators
       self._validate_my_rule()  # Add here
   ```

## Integration with Pipeline

Stage 2 is **optional** in the pipeline:

```
Stage 1: Ledger Builder → ledger.csv
    ↓
Stage 2: Marker Validator (optional) → validation report
    ↓
Stage 3: Test Mapper → ledger-mapped.csv
    ↓
Stages 4-7: Codegen → Translation → PR Orchestration
```

**Run validation:**
- Before Stage 3 to ensure marker quality
- After marker updates to verify fixes
- Periodically as part of code review

## References

- **Acceleration Plan:** [docs/api/acceleration-plan.md](../../../docs/api/acceleration-plan.md)
- **Field Registry:** [hack/api-codegen/pkg/registry/field_metadata.json](../../api-codegen/pkg/registry/field_metadata.json)
- **Marker Reference:** See `hack/api-codegen/pkg/markers/` for marker definitions

## FAQ

**Q: All my markers are already set. Why do I need this?**  
A: Validation catches inconsistencies, suggests improvements, and prevents marker drift over time.

**Q: Should I fix all warnings?**  
A: Focus on high-confidence warnings (>70%). Info-level issues are advisory.

**Q: What if validation suggests a change I disagree with?**  
A: Validation suggests based on conventions. If a field intentionally breaks conventions (e.g., mutable ID for a valid reason), you can ignore the suggestion. Add a comment in the code explaining why.

**Q: How often should I run validation?**  
A: Run when adding new fields, changing markers, or as part of code review before major releases.

**Q: Can I add custom validation rules?**  
A: Yes! See "Extending Validation" section above.
