#!/usr/bin/env python3
"""
Acceleration Pipeline Report

Generates a formatted summary of the current pipeline state:
- Field enumeration statistics (Stage 1)
- Classification buckets (Stage 3)
- Delivery coverage

Usage:
    python report.py [--ledger <path>]
"""

import argparse
import sys
from pathlib import Path

import pandas as pd


def print_table_header(title: str, width: int = 80):
    """Print section header."""
    print(f"\n{'=' * width}")
    print(f"  {title}")
    print(f"{'=' * width}\n")


def print_box_table(headers: list, rows: list, column_widths: list = None):
    """Print a box-style ASCII table."""
    if not rows:
        print("  No data")
        return

    # Auto-calculate column widths if not provided
    if column_widths is None:
        column_widths = []
        for i, header in enumerate(headers):
            max_width = len(str(header))
            for row in rows:
                if i < len(row):
                    max_width = max(max_width, len(str(row[i])))
            column_widths.append(max_width)

    # Box drawing characters
    top_left, top_right = '┌', '┐'
    bottom_left, bottom_right = '└', '┘'
    horizontal, vertical = '─', '│'
    t_down, t_up = '┬', '┴'
    t_right, t_left = '├', '┤'
    cross = '┼'

    # Calculate total width
    total_width = sum(column_widths) + len(headers) * 3 + 1

    # Print top border
    border_parts = []
    for width in column_widths:
        border_parts.append(horizontal * (width + 2))
    print(f"  {top_left}{t_down.join(border_parts)}{top_right}")

    # Print headers
    header_parts = []
    for header, width in zip(headers, column_widths):
        header_parts.append(f" {str(header).center(width)} ")
    print(f"  {vertical}{vertical.join(header_parts)}{vertical}")

    # Print header separator
    separator_parts = []
    for width in column_widths:
        separator_parts.append(horizontal * (width + 2))
    print(f"  {t_right}{cross.join(separator_parts)}{t_left}")

    # Print rows
    for i, row in enumerate(rows):
        row_parts = []
        for cell, width in zip(row, column_widths):
            # Right-align numbers, left-align text
            cell_str = str(cell)
            if cell_str.replace('.', '').replace('%', '').replace(',', '').isdigit():
                formatted = cell_str.rjust(width)
            else:
                formatted = cell_str.ljust(width)
            row_parts.append(f" {formatted} ")
        print(f"  {vertical}{vertical.join(row_parts)}{vertical}")

    # Print bottom border
    print(f"  {bottom_left}{t_up.join(border_parts)}{bottom_right}")


def print_field_enumeration(df: pd.DataFrame):
    """Print Stage 1: Field enumeration statistics."""
    print_table_header("📊 Field Enumeration (Stage 1)")

    # Count by resource type
    resource_counts = df['owner_type'].value_counts().sort_values(ascending=False)

    # Prepare table data
    headers = ['Resource', 'Fields']
    rows = []

    for resource, count in resource_counts.items():
        rows.append([resource, str(count)])

    # Add total row
    rows.append(['─' * 20, '─' * 6])
    rows.append(['Total', str(len(df))])

    print_box_table(headers, rows, column_widths=[22, 8])

    # Write mode distribution
    print("\n  Write Mode Distribution:")
    for write_mode, count in df['write_mode'].value_counts().sort_index().items():
        pct = (count / len(df)) * 100
        print(f"    • {write_mode}: {count} ({pct:.1f}%)")

    # Visibility
    hidden_count = df['hidden'].sum()
    public_count = len(df) - hidden_count
    print("\n  Visibility:")
    print(f"    • Public (hidden=false): {public_count}")
    print(f"    • Hidden (hidden=true): {hidden_count}")


def print_classification(df: pd.DataFrame):
    """Print Stage 3: Classification buckets."""
    print_table_header("📋 Classification (Stage 3)")

    # Check if classification exists
    if 'status' not in df.columns or df['status'].isna().all():
        print("  ⚠️  Classification not run yet. Run 'make accel-test-mapper' first.\n")
        return

    # Count by status
    status_counts = df['status'].value_counts()

    # Define bucket order and descriptions
    bucket_info = {
        'passthrough-clean': ('✅', 'Deliverable now (Stage 4: Codegen)'),
        'needs-test': ('⚠️', 'Test writing (Stage 6)'),
        'not-passthrough': ('⚠️', 'Carved out (bespoke work)'),
        'unmatched': ('❓', 'Manual JIRA assignment'),
        'needs-classification': ('⏳', 'Not classified yet'),
    }

    # Prepare table data
    headers = ['Bucket', 'Count', '%', 'Next Action']
    rows = []

    for bucket in ['passthrough-clean', 'needs-test', 'not-passthrough', 'unmatched', 'needs-classification']:
        if bucket in status_counts:
            count = status_counts[bucket]
            pct = (count / len(df)) * 100
            icon, action = bucket_info.get(bucket, ('', 'Unknown'))
            rows.append([bucket, str(count), f"{pct:.1f}%", f"{icon} {action}"])

    print_box_table(headers, rows, column_widths=[19, 7, 7, 41])


def print_delivery_summary(df: pd.DataFrame):
    """Print delivery summary."""
    print_table_header("🎯 Delivery Summary")

    if 'status' not in df.columns or df['status'].isna().all():
        print("  ⚠️  Classification not run yet.\n")
        return

    deliverable = len(df[df['status'] == 'passthrough-clean'])
    needs_work = len(df[df['status'].isin(['needs-test', 'not-passthrough'])])
    unmatched = len(df[df['status'] == 'unmatched'])
    total = len(df)

    coverage_pct = (deliverable / total) * 100 if total > 0 else 0

    print(f"  ✅ Passthrough-clean (deliverable now): {deliverable} / {total} ({coverage_pct:.1f}%)")
    print(f"  ⚠️  Needs work (test writing or conversion): {needs_work} / {total} ({(needs_work/total)*100:.1f}%)")
    print(f"  ❓ Unmatched (manual review needed): {unmatched} / {total} ({(unmatched/total)*100:.1f}%)")

    print(f"\n  Delivery Coverage: {coverage_pct:.1f}%")

    # Progress bar
    bar_width = 50
    deliverable_bar = int((deliverable / total) * bar_width)
    needs_work_bar = int((needs_work / total) * bar_width)
    unmatched_bar = bar_width - deliverable_bar - needs_work_bar

    print(f"\n  [{'█' * deliverable_bar}{'▓' * needs_work_bar}{'░' * unmatched_bar}]")
    print(f"   └─ Green: deliverable, Gray: needs work, Light: unmatched\n")


def print_next_steps(df: pd.DataFrame):
    """Print recommended next steps."""
    print_table_header("🚀 Next Steps")

    if 'status' not in df.columns or df['status'].isna().all():
        print("  1. Run classification: make accel-test-mapper")
        print("  2. Review unmatched fields: make accel-review-helper")
        print("  3. Manually assign JIRA tickets to unmatched fields")
    else:
        unmatched = len(df[df['status'] == 'unmatched'])
        deliverable = len(df[df['status'] == 'passthrough-clean'])

        if unmatched > 0:
            print(f"  1. Review {unmatched} unmatched fields:")
            print(f"     make accel-review-helper")
            print(f"     open hack/accelerate/test-mapper/output/review-guide.md")
            print()
            print(f"  2. Edit ledger-mapped.csv to assign JIRA tickets")
            print(f"     open hack/accelerate/ledger-builder/output/ledger-mapped.csv")
            print()
        else:
            print("  ✅ All fields have JIRA assignments!")
            print()

        if deliverable > 0:
            print(f"  3. Stage 4: Run codegen over {deliverable} passthrough-clean fields")
        else:
            print(f"  3. Stage 4: Codegen (waiting for passthrough-clean fields)")

        print(f"  4. Stage 5: Build command-to-SDK translator")
        print(f"  5. Stage 6: Generate missing tests")
        print(f"  6. Stage 7: Batch PR orchestration")

    print()


def main():
    parser = argparse.ArgumentParser(
        description='Generate acceleration pipeline report'
    )

    parser.add_argument(
        '--ledger',
        type=Path,
        default=Path('hack/accelerate/ledger-builder/output/ledger-mapped.csv'),
        help='Path to ledger CSV (default: ledger-mapped.csv, falls back to ledger.csv)'
    )

    args = parser.parse_args()

    # Try mapped ledger first, fall back to unmapped
    ledger_path = args.ledger.resolve()
    if not ledger_path.exists():
        ledger_path = ledger_path.parent / 'ledger.csv'

    if not ledger_path.exists():
        print(f"Error: Ledger file not found at {args.ledger} or {ledger_path}", file=sys.stderr)
        print("\nRun 'make accel-build-ledger' first to generate the ledger.", file=sys.stderr)
        sys.exit(1)

    # Load ledger
    df = pd.read_csv(ledger_path)

    # Print banner
    print("\n" + "=" * 80)
    print("  ROSA HyperFleet API - V2 Passthrough Feature Acceleration Pipeline")
    print("=" * 80)
    print(f"\n  Data source: {ledger_path.name}")

    # Print sections
    print_field_enumeration(df)
    print_classification(df)
    print_delivery_summary(df)
    print_next_steps(df)

    print("=" * 80)
    print()


if __name__ == '__main__':
    main()
