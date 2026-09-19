#!/usr/bin/env python3
"""
Ledger Builder - Stage 1 of the V2 Passthrough Feature Acceleration Pipeline

Transforms the existing field_metadata.json (Field Registry) into a CSV ledger
with additional columns for tracking feature delivery: feature_ref, test_ref, status, notes.

This ledger becomes the single source of truth for the acceleration pipeline.
Subsequent stages (marker suggester, test mapper) will augment this CSV.

Usage:
    python build_ledger.py --input <path-to-field_metadata.json> --output <path-to-ledger.csv>
"""

import argparse
import json
import sys
from pathlib import Path
from typing import Dict, List

import pandas as pd


def load_field_metadata(input_path: Path) -> List[Dict]:
    """Load and parse field_metadata.json."""
    try:
        with open(input_path, 'r') as f:
            data = json.load(f)

        if not isinstance(data, list):
            raise ValueError(f"Expected JSON array, got {type(data).__name__}")

        return data
    except FileNotFoundError:
        print(f"Error: Input file not found: {input_path}", file=sys.stderr)
        sys.exit(1)
    except json.JSONDecodeError as e:
        print(f"Error: Invalid JSON in {input_path}: {e}", file=sys.stderr)
        sys.exit(1)


def transform_to_ledger_rows(field_metadata: List[Dict]) -> List[Dict]:
    """
    Transform field_metadata.json records into ledger CSV rows.

    Adds new columns:
    - feature_ref: empty (to be filled by Stage 3 test mapper)
    - test_ref: empty (to be filled by Stage 3 test mapper)
    - status: 'needs-classification' (initial state)
    - notes: empty (for manual annotations)
    """
    ledger_rows = []

    for record in field_metadata:
        # Extract existing fields
        field_path = record.get('fieldPath', '')
        write_mode = record.get('writeMode', '')
        hidden = record.get('hidden', False)
        owner_type = record.get('ownerType', '')
        owner_gvk = record.get('ownerGVK', '')

        # Build ledger row with new columns
        ledger_row = {
            'owner_type': owner_type,
            'field_path': field_path,
            'write_mode': write_mode,
            'hidden': hidden,
            'owner_gvk': owner_gvk,
            'feature_ref': '',  # Empty, for Stage 3 to fill
            'test_ref': '',     # Empty, for Stage 3 to fill
            'status': 'needs-classification',  # Initial status
            'notes': ''         # Empty, for manual annotations
        }

        ledger_rows.append(ledger_row)

    return ledger_rows


def build_dataframe(ledger_rows: List[Dict]) -> pd.DataFrame:
    """Build pandas DataFrame and sort by owner_type then field_path."""
    df = pd.DataFrame(ledger_rows)

    # Sort by owner_type (Cluster, NodePool, etc.) then field_path
    df = df.sort_values(by=['owner_type', 'field_path'], ignore_index=True)

    return df


def print_statistics(df: pd.DataFrame, verbose: bool = False):
    """Print summary statistics about the ledger."""
    print("\n=== Ledger Builder Statistics ===")
    print(f"Total fields: {len(df)}")

    print("\nFields per resource type:")
    for owner_type, count in df['owner_type'].value_counts().sort_index().items():
        print(f"  {owner_type}: {count}")

    print("\nWrite mode distribution:")
    for write_mode, count in df['write_mode'].value_counts().sort_index().items():
        print(f"  {write_mode}: {count}")

    hidden_count = df['hidden'].sum()
    public_count = len(df) - hidden_count
    print(f"\nVisibility:")
    print(f"  Public (hidden=false): {public_count}")
    print(f"  Hidden (hidden=true): {hidden_count}")

    if verbose:
        print("\nSample rows (first 5):")
        print(df.head().to_string(index=False))


def write_csv(df: pd.DataFrame, output_path: Path):
    """Write DataFrame to CSV."""
    output_path.parent.mkdir(parents=True, exist_ok=True)

    df.to_csv(output_path, index=False)
    print(f"\n✓ Ledger written to: {output_path}")


def verify_output(input_count: int, output_count: int):
    """Verify row count matches input."""
    if input_count != output_count:
        print(f"\n⚠️  Warning: Row count mismatch!", file=sys.stderr)
        print(f"  Input records: {input_count}", file=sys.stderr)
        print(f"  Output rows: {output_count}", file=sys.stderr)
    else:
        print(f"✓ Verification passed: {output_count} rows")


def main():
    parser = argparse.ArgumentParser(
        description='Build delivery ledger CSV from field_metadata.json',
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Use default paths
  python build_ledger.py

  # Specify custom input/output
  python build_ledger.py --input ../api-codegen/pkg/registry/field_metadata.json \\
                         --output ./output/ledger.csv

  # Verbose output with sample rows
  python build_ledger.py --verbose
        """
    )

    parser.add_argument(
        '--input',
        type=Path,
        default=Path('../../api-codegen/pkg/registry/field_metadata.json'),
        help='Path to field_metadata.json (default: ../../api-codegen/pkg/registry/field_metadata.json)'
    )

    parser.add_argument(
        '--output',
        type=Path,
        default=Path('./output/ledger.csv'),
        help='Path to output CSV (default: ./output/ledger.csv)'
    )

    parser.add_argument(
        '--verbose', '-v',
        action='store_true',
        help='Print verbose output including sample rows'
    )

    args = parser.parse_args()

    # Convert to absolute paths
    input_path = args.input.resolve()
    output_path = args.output.resolve()

    print(f"Ledger Builder - Stage 1")
    print(f"Input:  {input_path}")
    print(f"Output: {output_path}")

    # Load field metadata
    field_metadata = load_field_metadata(input_path)
    print(f"\n✓ Loaded {len(field_metadata)} field records")

    # Transform to ledger rows
    ledger_rows = transform_to_ledger_rows(field_metadata)
    print(f"✓ Transformed {len(ledger_rows)} ledger rows")

    # Build DataFrame
    df = build_dataframe(ledger_rows)
    print(f"✓ Built DataFrame with {len(df)} rows")

    # Print statistics
    print_statistics(df, verbose=args.verbose)

    # Write CSV
    write_csv(df, output_path)

    # Verify
    verify_output(len(field_metadata), len(df))

    print("\n✓ Ledger build complete!")
    print("\nNext steps:")
    print("  1. Review the ledger CSV")
    print("  2. Stage 2: Run marker suggester (AI-assisted)")
    print("  3. Stage 3: Run test mapper to populate feature_ref and test_ref")


if __name__ == '__main__':
    main()
