# Test Mapper - Stage 3 of V2 Passthrough Feature Acceleration Pipeline

## Overview

The **Test Mapper** maps fields in the delivery ledger to JIRA tickets and test cases, then classifies each field into delivery buckets. This determines which fields can be delivered "in one go" vs. which need additional work.

This is **Stage 3** of the acceleration pipeline described in `docs/api/acceleration-plan.md`.

## Purpose

After Stage 1 (Ledger Builder) enumerates all fields, Stage 3 answers the key question:

> **Which fields are passthrough-clean (have tests, deliverable now) vs. need work?**

The mapper:
1. Matches field paths → JIRA capability tickets (fuzzy matching)
2. Populates `feature_ref` (JIRA ticket), `test_ref` (test count), `status` (classification)
3. Reports delivery coverage statistics

## Usage

### Quick Start

```bash
# Run full pipeline: Stage 1 → Stage 3
make accel-test-mapper

# View pipeline status report
make accel-report

# Generate review guide for unmatched fields
make accel-review-helper

# Output: hack/accelerate/ledger-builder/output/ledger-mapped.csv
```

### Manual Usage

```bash
# Activate virtual environment
source hack/accelerate/test-mapper/.venv/bin/activate

# Run with defaults
python hack/accelerate/test-mapper/map_tests.py --verbose

# Custom paths
python hack/accelerate/test-mapper/map_tests.py \
  --ledger ../ledger-builder/output/ledger.csv \
  --matrix ../matrix \
  --output ./output/ledger-mapped.csv
```

### Clean Up

```bash
make accel-test-mapper-clean  # Remove venv and mapped CSV
```

## Classification Buckets

The mapper classifies each field based on **complexity** (from JIRA mapping) and **test availability**:

| Status | Complexity | Test Count | Meaning |
|--------|------------|------------|---------|
| **passthrough-clean** | passthrough | TBD=0, Tests>0 | ✅ **Deliverable now** - true passthrough with test coverage |
| **needs-test** | passthrough | TBD>0 or Tests=0 | ⚠️ Test writing needed - passthrough but missing tests |
| **not-passthrough** | complex or unknown | Any | ⚠️ Carved out - needs conversion, defaulting, or bespoke work |
| **unmatched** | N/A | N/A | ❓ No JIRA mapping found - manual review required |

## Mapping Strategy

### Field → JIRA Matching

The mapper uses **fuzzy keyword matching**:

1. **Extract keywords** from field path:
   - `spec.hostedCluster.configuration.kubelet.maxPods` → `["kubelet", "max", "pods"]`
   - `spec.hostedCluster.additionalTrustBundle` → `["additional", "trust", "bundle"]`

2. **Match against capability names** in JIRA mapping:
   - "Kubelet Configs" matches `kubelet` keyword
   - "Additional Trust Bundle" matches `additional`, `trust`, `bundle`

3. **Score confidence**:
   - Perfect match (all keywords match): 1.0
   - Partial match: 0.3 - 0.7
   - No match: 0.0

4. **Threshold**: Matches below 0.3 confidence are rejected

### Limitations

The fuzzy matching is **intentionally conservative**:
- High-level capability names (e.g., "Kubelet Configs") don't always match specific field paths
- False positives can occur (e.g., "autoNode" → "Windows License" due to "node" keyword)
- Expected match rate: **~50%** on first pass

**This is by design**: The mapper provides a starting point. Manual review and refinement are expected.

## Manual Review Process

For **unmatched fields** (90 out of 177 in initial run):

1. **Open the mapped ledger CSV**:
   ```bash
   open hack/accelerate/ledger-builder/output/ledger-mapped.csv
   ```

2. **Review unmatched rows** (status=unmatched)

3. **Manually populate**:
   - Search JIRA mapping file (`hack/accelerate/matrix/jira-test-mapping-2.csv`)
   - Find matching capability for the field
   - Copy JIRA ticket to `feature_ref` column
   - Update `status` based on complexity (passthrough/complex) and test count
   - Add notes explaining the match

4. **Common patterns**:
   - **Kubelet config fields** → ROSAENG-65216 "Kubelet Configs"
   - **Registry fields** → ROSAENG-65207 "Registry Allowlists" or ROSAENG-65225 "Additional Trusted CAs"
   - **Autoscaling fields** → ROSAENG-65202 "Cluster Autoscaler"
   - **OIDC fields** → ROSAENG-65215 "Bring Your Own OIDC"

## Input Files

### Ledger CSV (from Stage 1)

```csv
owner_type,field_path,write_mode,hidden,owner_gvk,feature_ref,test_ref,status,notes
Cluster,spec.deleteProtection,mutable,False,hyperfleet.io/v1alpha1.Cluster,,,needs-classification,
```

### JIRA Mapping (jira-test-mapping-2.csv)

```csv
Jira,Capability,Jira status,Priority,Tests,TBD,N/A,Runtimes,Complexity,Test ids,Join
ROSAENG-65216,Kubelet Configs,To Do,Undefined,7,4,3,Day 2,passthrough,"68828, 68835, ...",KubeletConfig HCP CRUD
```

## Output

### Mapped Ledger CSV

```csv
owner_type,field_path,write_mode,hidden,owner_gvk,feature_ref,test_ref,status,notes
Cluster,spec.deleteProtection,mutable,False,hyperfleet.io/v1alpha1.Cluster,ROSAENG-65121,6 tests,not-passthrough,Matched: Delete Protection (confidence: 1.00)
Cluster,spec.hostedCluster.additionalTrustBundle,service-set,True,hyperfleet.io/v1alpha1.Cluster,ROSAENG-65147,2 tests,needs-test,Matched: Additional Trust Bundle (confidence: 1.00)
```

## Statistics (Initial Run)

From test run on 177 fields:

| Bucket | Count | Percentage |
|--------|-------|------------|
| passthrough-clean | 2 | 1.1% |
| needs-test | 68 | 38.4% |
| not-passthrough | 17 | 9.6% |
| unmatched | 90 | 50.8% |

**Delivery coverage: 1.1%** (2 fields ready now)  
**Needs work: 48.0%** (85 fields need test writing or conversion)  
**Manual review: 50.8%** (90 fields need JIRA assignment)

## Improving Match Rate

To improve matching for specific field groups:

1. **Add custom keyword rules** in `extract_keywords()`:
   ```python
   # Example: all kubelet.* fields → add "kubelet" keyword
   if 'kubelet.' in field_path:
       keywords.append('kubelet')
   ```

2. **Add direct mapping overrides**:
   ```python
   # Example: spec.hostedCluster.oidcConfig.* → ROSAENG-65215
   FIELD_OVERRIDES = {
       'spec.hostedCluster.oidcConfig': 'ROSAENG-65215',
       'spec.hostedCluster.configuration.kubelet': 'ROSAENG-65216',
   }
   ```

3. **Lower threshold** for specific capabilities:
   ```python
   # Accept lower confidence for "Kubelet Configs" since field names are verbose
   if 'Kubelet' in capability and score > 0.2:
       # Accept match
   ```

## Development

### Requirements

- Python 3.10+
- `pandas>=2.0.0`

### Project Structure

```
hack/accelerate/test-mapper/
├── map_tests.py         # Main script
├── requirements.txt     # Python dependencies
└── README.md           # This file
```

### Extending the Mapper

To add new classification logic:

1. Edit `classify_status()` in `map_tests.py`
2. Add new status values (e.g., "ai-test-gen-candidate")
3. Update statistics reporting

## Next Steps (After Manual Review)

Once the ledger is manually reviewed and unmatched fields are populated:

1. **Stage 4:** Run codegen over passthrough-clean fields
2. **Stage 5:** Build command-to-SDK translator for client wiring
3. **Stage 6:** Generate missing tests for needs-test fields
4. **Stage 7:** Batch PR orchestration

## References

- **Acceleration Plan:** [docs/api/acceleration-plan.md](../../../docs/api/acceleration-plan.md)
- **JIRA Mapping:** [hack/accelerate/matrix/jira-test-mapping-2.csv](../matrix/jira-test-mapping-2.csv)
- **Parent Epic:** [ROSA-848](https://redhat.atlassian.net/browse/ROSA-848) "V2 SDK & Client Support"

## FAQ

**Q: Why only 1.1% delivery coverage?**  
A: The fuzzy matching is conservative by design. After manual review and JIRA assignment for unmatched fields, coverage should increase to the ~40-70% range mentioned in the acceleration plan (bucket 1: passthrough-clean + test).

**Q: Can I manually edit the mapped CSV?**  
A: Yes! That's expected. Edit `feature_ref`, `test_ref`, `status`, and `notes` for unmatched or incorrectly matched fields.

**Q: What if a field doesn't map to any JIRA ticket?**  
A: Either it's not a V2 passthrough feature (carve it out with status="not-in-scope"), or it needs a new JIRA ticket created.

**Q: Should I re-run the mapper after manual edits?**  
A: No. The mapper is a one-time starting point. Manual edits persist in the CSV. Only re-run if you regenerate the ledger from scratch (Stage 1).

**Q: How do I know if a match is correct?**  
A: Check the JIRA ticket's "Capability" name and "Join" description in jira-test-mapping-2.csv. If the capability description matches the field's purpose, it's likely correct.
