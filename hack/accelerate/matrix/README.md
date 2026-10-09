# Test Mapping Matrix

This directory contains the JIRA-to-test-case mapping data used by Stage 3 (Test Mapper) to classify fields for delivery.

## Files

- **`jira-test-mapping-1.csv`** - Test-centric mapping (Test ID → JIRA tickets)
- **`jira-test-mapping-2.csv`** - JIRA-centric mapping (JIRA ticket → Capability → Tests) **← Primary file used by test mapper**
- **`jira-test-mapping-3.csv`** - Additional test mapping data

All files exported from Google Sheets.

## File Schemas

### jira-test-mapping-2.csv (Primary)

Used by `test-mapper/map_tests.py` for field→JIRA matching.

**Columns:**
- `Jira` - JIRA ticket ID (e.g., ROSAENG-65216)
- `Capability` - Feature name (e.g., "Kubelet Configs")
- `Jira status` - To Do, In Progress, etc.
- `Priority` - Undefined, High, Critical
- `Tests` - Total number of tests
- `TBD` - Number of tests marked as TBD (not yet implemented)
- `N/A` - Number of tests marked N/A (not applicable)
- `Runtimes` - Test runtime categories (Day 1, Day 2, etc.)
- `Complexity` - **passthrough** / **complex** / **unknown** ← Used for classification
- `Test ids` - Comma-separated test IDs
- `Join` - Description of the JIRA ticket

### jira-test-mapping-1.csv (Test-centric)

**Columns:**
- `Test id` - Unique test identifier
- `Test` - Test name
- `e2e file` - Test file path (e.g., tests/e2e/hcp_cluster_test.go)
- `Runtime` - Day 1, Day 2, Destroy, etc.
- `Runs on` - Classic only, HyperFleet, Both
- `Status` - TBD, N/A
- `Importance` - Critical, High, Medium, Low
- `Mapped Jiras` - JIRA tickets covered by this test
- `Notes` - Additional context

## Usage

The Test Mapper (Stage 3) reads this mapping to populate the ledger:

- `feature_ref` - Matched JIRA ticket from `Jira` column
- `test_ref` - Test count from `Tests` column
- `status` - Classification based on `Complexity` + test availability:
  - `passthrough-clean` if complexity=passthrough AND TBD=0 AND Tests>0
  - `needs-test` if complexity=passthrough BUT TBD>0
  - `not-passthrough` if complexity=complex or unknown

**Run the test mapper:**
```bash
make accel-test-mapper
```

## Updating the Mapping

To update the mapping data:

1. **Export from Google Sheets:**
   - Open: https://docs.google.com/spreadsheets/d/1GmWCxpvSVGIDVCYYI3ZE1ab9hGztyT8awxI04IYRpx4/edit
   - File → Download → Comma Separated Values (.csv)
   - Save each sheet as `jira-test-mapping-N.csv` in this directory

2. **Re-run test mapper:**
   ```bash
   make accel-test-mapper
   ```

3. **Review changes:**
   ```bash
   git diff hack/accelerate/ledger-builder/output/ledger-mapped.csv
   ```

## Statistics

From current mapping (jira-test-mapping-2.csv):

- **Total JIRA tickets:** 101
- **Complexity breakdown:**
  - passthrough: ~70-80% (deliverable via pipeline)
  - complex: ~15-20% (needs bespoke work)
  - unknown: ~5% (needs investigation)

## References

- **Google Sheets (requires auth):** https://docs.google.com/spreadsheets/d/1GmWCxpvSVGIDVCYYI3ZE1ab9hGztyT8awxI04IYRpx4/edit?gid=1095763576
- **Acceleration Plan:** [docs/api/acceleration-plan.md](../../../docs/api/acceleration-plan.md)
- **Parent Epic:** [ROSA-848](https://redhat.atlassian.net/browse/ROSA-848)
