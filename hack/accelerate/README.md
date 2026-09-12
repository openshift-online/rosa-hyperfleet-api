# V2 Passthrough Feature Acceleration Pipeline

This directory contains the tooling for the accelerated delivery of ~70 V2 passthrough features to the ROSA HyperFleet API.

See the [Acceleration Plan](../../docs/api/acceleration-plan.md) for the full strategy.

## Quick Start

```bash
# Run the full pipeline
make accel-test-mapper

# View status report
make accel-report

# Review unmatched fields
make accel-review-helper
open hack/accelerate/test-mapper/output/review-guide.md

# Edit mapped ledger to assign JIRA tickets
open hack/accelerate/ledger-builder/output/ledger-mapped.csv
```

## Directory Structure

```
hack/accelerate/
├── README.md                    # This file
├── report.py                    # Pipeline status report generator
├── ledger-builder/              # Stage 1: Field enumeration
│   ├── build_ledger.py
│   ├── requirements.txt
│   ├── README.md
│   └── output/
│       ├── ledger.csv           # 177 fields enumerated
│       └── ledger-mapped.csv    # With JIRA assignments (Stage 3)
├── test-mapper/                 # Stage 3: JIRA mapping & classification
│   ├── map_tests.py
│   ├── review_helper.py         # Manual review assistant
│   ├── requirements.txt
│   ├── README.md
│   └── output/
│       └── review-guide.md      # JIRA suggestions for unmatched fields
└── matrix/                      # JIRA-to-test mapping data
    ├── jira-test-mapping-1.csv  # Test-centric
    ├── jira-test-mapping-2.csv  # JIRA-centric (primary)
    ├── jira-test-mapping-3.csv  # Additional
    └── README.md
```

## Pipeline Stages

### Stage 1: Ledger Builder ✅
**Status:** Complete  
**Tool:** `ledger-builder/build_ledger.py`  
**Input:** `hack/api-codegen/pkg/registry/field_metadata.json`  
**Output:** `ledger.csv` (177 fields)

Transforms the existing Field Registry into a CSV-based delivery ledger.

```bash
make accel-build-ledger
```

### Stage 2: Marker Validator ✅
**Status:** Complete (optional QC step)  
**Tool:** `marker-suggester/validate_markers.py`  
**Input:** `ledger.csv`  
**Output:** `marker-validation-report.md`

Validates existing marker assignments and suggests improvements.

```bash
make accel-validate-markers
```

**Note:** While markers already exist, this stage provides quality control by validating marker consistency and suggesting improvements.

### Stage 3: Test Mapper ✅
**Status:** Complete  
**Tool:** `test-mapper/map_tests.py`  
**Input:** `ledger.csv` + `matrix/jira-test-mapping-2.csv`  
**Output:** `ledger-mapped.csv` (with JIRA assignments)

Maps fields to JIRA tickets and classifies into delivery buckets.

```bash
make accel-test-mapper
```

### Manual Review (Stage 3 refinement) 🔄
**Status:** In Progress  
**Tool:** `test-mapper/review_helper.py`

90 unmatched fields need JIRA assignment. The review helper suggests candidates.

```bash
make accel-review-helper
```

### Stage 4: Codegen 🔜
**Status:** Planned  
**Prerequisites:** Mostly exists (passthrough-gen, conversion-gen)

Run existing codegen over passthrough-clean fields.

### Stage 5: Command-to-SDK Translator 🔜
**Status:** Planned  
**Tool:** New, AI-assisted

Map rosa CLI commands → v2 clientset calls.

### Stage 6: Missing-Test Generator 🔜
**Status:** Planned  
**Tool:** New, AI-assisted

Generate tests for needs-test fields.

### Stage 7: Batch PR Orchestrator 🔜
**Status:** Planned  
**Prerequisites:** Partly exists (Konflux renovate)

Chain PR1 (API) → PR2 (operator) → PR3 (CLI) → PR4 (terraform).

## Make Targets

### Pipeline Execution

```bash
make accel-build-ledger        # Stage 1: Build ledger
make accel-test-mapper         # Stage 1 + 3: Full pipeline
make accel-review-helper       # Generate review guide for unmatched fields
make accel-report              # Show pipeline status report
```

### Setup & Cleanup

```bash
make accel-build-setup         # Setup venv for ledger builder
make accel-test-mapper-setup   # Setup venv for test mapper
make accel-build-clean         # Clean ledger artifacts
make accel-test-mapper-clean   # Clean mapper artifacts
```

## Current Status (Run `make accel-report` for latest)

- **Total fields:** 177
- **Passthrough-clean:** 2 (1.1%) - deliverable now
- **Needs-test:** 68 (38.4%) - test writing needed
- **Not-passthrough:** 17 (9.6%) - carved out for bespoke work
- **Unmatched:** 90 (50.8%) - manual JIRA assignment needed

**Delivery coverage:** 1.1% (expected to increase after manual review)

## Next Steps

1. **Manual review:** Assign JIRA tickets to 90 unmatched fields
   - Use review guide: `make accel-review-helper`
   - Edit: `hack/accelerate/ledger-builder/output/ledger-mapped.csv`

2. **Stage 4:** Run codegen over passthrough-clean fields

3. **Stage 5:** Build command-to-SDK translator

4. **Stage 6:** Generate missing tests

5. **Stage 7:** Batch PR orchestration

## References

- **Acceleration Plan:** [docs/api/acceleration-plan.md](../../docs/api/acceleration-plan.md)
- **Parent Epic:** [ROSA-848](https://redhat.atlassian.net/browse/ROSA-848) "V2 SDK & Client Support"
- **Field Registry:** [hack/api-codegen/pkg/registry/field_metadata.json](../api-codegen/pkg/registry/field_metadata.json)
