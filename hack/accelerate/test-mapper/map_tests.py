#!/usr/bin/env python3
"""
Test Mapper - Stage 3 of the V2 Passthrough Feature Acceleration Pipeline

Maps fields in the delivery ledger to JIRA tickets and test cases, then classifies
each field into delivery buckets (passthrough-clean, needs-test, not-passthrough).

This determines which fields can be delivered "in one go" vs. which need additional work.

Usage:
    python map_tests.py --ledger <ledger.csv> --matrix <matrix-dir> --output <updated-ledger.csv>
"""

import argparse
import re
import sys
from pathlib import Path
from typing import Dict, List, Optional, Tuple

import pandas as pd


def load_ledger(ledger_path: Path) -> pd.DataFrame:
    """Load the delivery ledger CSV from Stage 1."""
    try:
        df = pd.read_csv(ledger_path)
        required_columns = ['owner_type', 'field_path', 'write_mode', 'hidden', 'owner_gvk']
        missing = [col for col in required_columns if col not in df.columns]
        if missing:
            raise ValueError(f"Ledger missing required columns: {missing}")
        return df
    except FileNotFoundError:
        print(f"Error: Ledger file not found: {ledger_path}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"Error loading ledger: {e}", file=sys.stderr)
        sys.exit(1)


def load_jira_mapping(matrix_dir: Path) -> pd.DataFrame:
    """Load JIRA-to-test mapping from matrix directory."""
    # Primary mapping file: jira-test-mapping-2.csv (JIRA-centric)
    mapping_file = matrix_dir / 'jira-test-mapping-2.csv'

    try:
        df = pd.read_csv(mapping_file)
        required_columns = ['Jira', 'Capability', 'Complexity']
        missing = [col for col in required_columns if col not in df.columns]
        if missing:
            raise ValueError(f"Mapping file missing required columns: {missing}")
        return df
    except FileNotFoundError:
        print(f"Error: Mapping file not found: {mapping_file}", file=sys.stderr)
        print(f"Expected jira-test-mapping-2.csv in {matrix_dir}", file=sys.stderr)
        sys.exit(1)
    except Exception as e:
        print(f"Error loading JIRA mapping: {e}", file=sys.stderr)
        sys.exit(1)


def extract_keywords(field_path: str) -> List[str]:
    """
    Extract keywords from field path for matching.

    Examples:
        "spec.hostedCluster.configuration.kubelet.containerLogMaxFiles"
        → ["kubelet", "container", "log", "max", "files"]

        "spec.hostedCluster.autoNode"
        → ["auto", "node"]
    """
    # Split on dots and camelCase boundaries
    parts = field_path.split('.')

    keywords = []
    for part in parts:
        # Skip common prefixes
        if part in ['spec', 'status', 'hostedCluster', 'configuration', 'platform']:
            continue

        # Split camelCase: "autoNode" → ["auto", "node"]
        words = re.findall(r'[A-Z]?[a-z]+|[A-Z]+(?=[A-Z][a-z]|\b)', part)
        keywords.extend([w.lower() for w in words if len(w) > 2])

    return keywords


def match_capability(field_keywords: List[str], capability: str) -> float:
    """
    Score how well field keywords match a capability name.
    Returns a score between 0.0 (no match) and 1.0 (perfect match).
    """
    if pd.isna(capability):
        return 0.0

    capability_lower = capability.lower()
    capability_words = set(re.findall(r'\b\w+\b', capability_lower))

    if not field_keywords:
        return 0.0

    # Count keyword matches
    matches = sum(1 for kw in field_keywords if kw in capability_lower)

    # Bonus if exact field keyword appears as whole word in capability
    exact_matches = sum(1 for kw in field_keywords if kw in capability_words)

    # Score: weighted combination
    score = (matches * 0.5 + exact_matches * 0.5) / len(field_keywords)

    return min(score, 1.0)


def find_best_jira_match(field_path: str, jira_mapping: pd.DataFrame, threshold: float = 0.3) -> Optional[Tuple[str, str, float, float]]:
    """
    Find best JIRA ticket match for a field.

    Returns: (jira_ticket, capability_name, complexity, test_count, confidence) or None
    """
    keywords = extract_keywords(field_path)

    if not keywords:
        return None

    best_score = 0.0
    best_match = None

    for _, row in jira_mapping.iterrows():
        capability = row.get('Capability', '')
        score = match_capability(keywords, capability)

        if score > best_score:
            best_score = score
            best_match = row

    if best_score < threshold or best_match is None:
        return None

    jira_ticket = best_match.get('Jira', '')
    capability_name = best_match.get('Capability', '')
    complexity = best_match.get('Complexity', 'unknown')
    test_count = best_match.get('Tests', 0)
    tbd_count = best_match.get('TBD', 0)

    return (jira_ticket, capability_name, complexity, test_count, tbd_count, best_score)


def classify_status(complexity: str, test_count: int, tbd_count: int) -> str:
    """
    Classify field into delivery bucket based on complexity and test availability.

    Buckets:
    - passthrough-clean: complexity=passthrough, tests exist (TBD=0)
    - needs-test: complexity=passthrough, but tests missing (TBD>0)
    - not-passthrough: complexity=complex or unknown (needs bespoke work)
    - unmatched: no JIRA mapping found
    """
    if complexity == 'passthrough':
        if tbd_count == 0 and test_count > 0:
            return 'passthrough-clean'
        else:
            return 'needs-test'
    elif complexity in ['complex', 'unknown']:
        return 'not-passthrough'
    else:
        return 'unmatched'


def map_fields_to_jiras(ledger: pd.DataFrame, jira_mapping: pd.DataFrame, verbose: bool = False) -> pd.DataFrame:
    """
    Map each ledger field to JIRA ticket and classify status.
    """
    results = []

    for idx, row in ledger.iterrows():
        field_path = row['field_path']
        owner_type = row['owner_type']

        # Find best JIRA match
        match = find_best_jira_match(field_path, jira_mapping)

        if match:
            jira_ticket, capability, complexity, test_count, tbd_count, confidence = match
            status = classify_status(complexity, test_count, tbd_count)
            test_ref = f"{test_count} tests" if test_count > 0 else ""
            notes = f"Matched: {capability} (confidence: {confidence:.2f})"

            if verbose and confidence < 0.5:
                print(f"  Low confidence: {field_path} → {capability} ({confidence:.2f})")

        else:
            jira_ticket = ""
            test_ref = ""
            status = "unmatched"
            notes = "No JIRA mapping found"

        # Update row
        row['feature_ref'] = jira_ticket
        row['test_ref'] = test_ref
        row['status'] = status
        row['notes'] = notes

        results.append(row)

    return pd.DataFrame(results)


def print_statistics(df: pd.DataFrame):
    """Print classification statistics."""
    print("\n=== Test Mapper Statistics ===")
    print(f"Total fields: {len(df)}")

    print("\nClassification buckets:")
    for status, count in df['status'].value_counts().sort_index().items():
        print(f"  {status}: {count}")

    print("\nFields per resource type:")
    for owner_type, count in df['owner_type'].value_counts().sort_index().items():
        print(f"  {owner_type}: {count}")

    # Deliverable count
    deliverable = len(df[df['status'] == 'passthrough-clean'])
    needs_work = len(df[df['status'].isin(['needs-test', 'not-passthrough'])])
    unmatched = len(df[df['status'] == 'unmatched'])

    print(f"\n=== Delivery Summary ===")
    print(f"✓ Passthrough-clean (deliverable now): {deliverable}")
    print(f"⚠ Needs work (test writing or conversion): {needs_work}")
    print(f"? Unmatched (manual review needed): {unmatched}")

    coverage_pct = (deliverable / len(df)) * 100 if len(df) > 0 else 0
    print(f"\nDelivery coverage: {coverage_pct:.1f}%")


def write_ledger(df: pd.DataFrame, output_path: Path):
    """Write updated ledger to CSV."""
    output_path.parent.mkdir(parents=True, exist_ok=True)
    df.to_csv(output_path, index=False)
    print(f"\n✓ Updated ledger written to: {output_path}")


def main():
    parser = argparse.ArgumentParser(
        description='Map fields to JIRA tickets and classify delivery buckets',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Use default paths
  python map_tests.py

  # Custom paths
  python map_tests.py --ledger ../ledger-builder/output/ledger.csv \\
                      --matrix ../matrix \\
                      --output ./output/ledger-mapped.csv \\
                      --verbose
        """
    )

    parser.add_argument(
        '--ledger',
        type=Path,
        default=Path('../ledger-builder/output/ledger.csv'),
        help='Path to ledger CSV from Stage 1 (default: ../ledger-builder/output/ledger.csv)'
    )

    parser.add_argument(
        '--matrix',
        type=Path,
        default=Path('../matrix'),
        help='Path to matrix directory with JIRA mapping CSVs (default: ../matrix)'
    )

    parser.add_argument(
        '--output',
        type=Path,
        default=Path('../ledger-builder/output/ledger-mapped.csv'),
        help='Path to output updated ledger CSV (default: ../ledger-builder/output/ledger-mapped.csv)'
    )

    parser.add_argument(
        '--verbose', '-v',
        action='store_true',
        help='Print verbose output including low-confidence matches'
    )

    args = parser.parse_args()

    # Convert to absolute paths
    ledger_path = args.ledger.resolve()
    matrix_dir = args.matrix.resolve()
    output_path = args.output.resolve()

    print(f"Test Mapper - Stage 3")
    print(f"Ledger:  {ledger_path}")
    print(f"Matrix:  {matrix_dir}")
    print(f"Output:  {output_path}")

    # Load data
    print(f"\n✓ Loading ledger...")
    ledger = load_ledger(ledger_path)
    print(f"  {len(ledger)} fields loaded")

    print(f"\n✓ Loading JIRA mapping...")
    jira_mapping = load_jira_mapping(matrix_dir)
    print(f"  {len(jira_mapping)} JIRA tickets loaded")

    # Map fields to JIRAs
    print(f"\n✓ Mapping fields to JIRA tickets...")
    mapped_ledger = map_fields_to_jiras(ledger, jira_mapping, verbose=args.verbose)

    # Print statistics
    print_statistics(mapped_ledger)

    # Write output
    write_ledger(mapped_ledger, output_path)

    print("\n✓ Test mapping complete!")
    print("\nNext steps:")
    print("  1. Review the mapped ledger CSV")
    print("  2. Fix unmatched fields (manual JIRA assignment)")
    print("  3. Stage 4: Run codegen over passthrough-clean fields")
    print("  4. Stage 5: Build command-to-SDK translator")


if __name__ == '__main__':
    main()
