#!/usr/bin/env python3
"""
Review Helper - Assists with manual JIRA assignment for unmatched fields

Suggests likely JIRA candidates for each unmatched field based on:
- Keyword matching
- Field path analysis
- Similar matched fields

Usage:
    python review_helper.py [--output review-guide.md]
"""

import argparse
import re
from pathlib import Path
from typing import Dict, List, Tuple

import pandas as pd


def load_data(ledger_path: Path, matrix_dir: Path) -> Tuple[pd.DataFrame, pd.DataFrame]:
    """Load mapped ledger and JIRA mapping."""
    ledger = pd.read_csv(ledger_path)
    jira_mapping = pd.read_csv(matrix_dir / 'jira-test-mapping-2.csv')
    return ledger, jira_mapping


def extract_field_keywords(field_path: str) -> List[str]:
    """Extract meaningful keywords from field path."""
    # Remove common prefixes
    path = field_path.replace('spec.', '').replace('hostedCluster.', '').replace('configuration.', '')

    # Split on dots
    parts = path.split('.')

    keywords = []
    for part in parts:
        # Split camelCase
        words = re.findall(r'[A-Z]?[a-z]+|[A-Z]+(?=[A-Z][a-z]|\b)', part)
        keywords.extend([w.lower() for w in words if len(w) > 2])

    return keywords


def find_candidates(field_path: str, keywords: List[str], jira_mapping: pd.DataFrame, top_n: int = 3) -> List[Dict]:
    """Find top N JIRA candidates for a field."""
    candidates = []

    for _, row in jira_mapping.iterrows():
        capability = row.get('Capability', '')
        if pd.isna(capability):
            continue

        capability_lower = capability.lower()
        jira_ticket = row.get('Jira', '')
        complexity = row.get('Complexity', 'unknown')
        tests = row.get('Tests', 0)
        tbd = row.get('TBD', 0)
        join_desc = row.get('Join', '')

        # Calculate match score
        score = 0.0
        matched_keywords = []

        for keyword in keywords:
            if keyword in capability_lower:
                score += 0.5
                matched_keywords.append(keyword)

            # Check in join description too
            if not pd.isna(join_desc) and keyword in str(join_desc).lower():
                score += 0.2

        # Boost score for exact matches
        capability_words = set(re.findall(r'\b\w+\b', capability_lower))
        for keyword in keywords:
            if keyword in capability_words:
                score += 0.3

        # Normalize by keyword count
        if keywords:
            score = score / len(keywords)

        if score > 0:
            candidates.append({
                'jira': jira_ticket,
                'capability': capability,
                'complexity': complexity,
                'tests': tests,
                'tbd': tbd,
                'score': score,
                'matched_keywords': matched_keywords,
                'description': join_desc
            })

    # Sort by score descending
    candidates.sort(key=lambda x: x['score'], reverse=True)

    return candidates[:top_n]


def group_by_prefix(unmatched_df: pd.DataFrame) -> Dict[str, List]:
    """Group unmatched fields by common prefixes."""
    groups = {}

    for _, row in unmatched_df.iterrows():
        field_path = row['field_path']

        # Extract prefix (e.g., "spec.hostedCluster.configuration.kubelet")
        parts = field_path.split('.')
        if len(parts) >= 3:
            prefix = '.'.join(parts[:3])
        else:
            prefix = '.'.join(parts[:2]) if len(parts) >= 2 else parts[0]

        if prefix not in groups:
            groups[prefix] = []
        groups[prefix].append(row)

    return groups


def generate_review_guide(ledger: pd.DataFrame, jira_mapping: pd.DataFrame, output_path: Path):
    """Generate markdown review guide."""
    unmatched = ledger[ledger['status'] == 'unmatched'].copy()

    if len(unmatched) == 0:
        print("✓ No unmatched fields! All fields have been mapped.")
        return

    # Group by common prefixes
    groups = group_by_prefix(unmatched)

    # Build markdown
    md = []
    md.append("# Manual Review Guide - Unmatched Fields\n")
    md.append(f"**Total unmatched fields:** {len(unmatched)}\n")
    md.append(f"**Grouped by prefix:** {len(groups)} groups\n")
    md.append("\n---\n")

    # Add summary table
    md.append("## Summary by Prefix\n")
    md.append("| Prefix | Count | Suggested JIRA |\n")
    md.append("|--------|-------|----------------|\n")

    for prefix, fields in sorted(groups.items(), key=lambda x: len(x[1]), reverse=True):
        # Get suggestion for first field in group
        first_field = fields[0]['field_path']
        keywords = extract_field_keywords(first_field)
        candidates = find_candidates(first_field, keywords, jira_mapping, top_n=1)

        if candidates:
            suggestion = f"{candidates[0]['jira']} ({candidates[0]['capability']})"
        else:
            suggestion = "No suggestion"

        md.append(f"| `{prefix}` | {len(fields)} | {suggestion} |\n")

    md.append("\n---\n")

    # Detailed field-by-field suggestions
    md.append("## Detailed Suggestions\n\n")

    for prefix, fields in sorted(groups.items(), key=lambda x: len(x[1]), reverse=True):
        md.append(f"### {prefix} ({len(fields)} fields)\n\n")

        for field_row in fields:
            field_path = field_row['field_path']
            owner_type = field_row['owner_type']
            write_mode = field_row['write_mode']
            hidden = field_row['hidden']

            keywords = extract_field_keywords(field_path)
            candidates = find_candidates(field_path, keywords, jira_mapping, top_n=3)

            md.append(f"#### `{field_path}`\n\n")
            md.append(f"- **Owner:** {owner_type}\n")
            md.append(f"- **Write mode:** {write_mode}\n")
            md.append(f"- **Hidden:** {hidden}\n")
            md.append(f"- **Keywords:** {', '.join(keywords)}\n\n")

            if candidates:
                md.append("**Suggested JIRA tickets:**\n\n")
                for i, candidate in enumerate(candidates, 1):
                    status = 'passthrough-clean' if candidate['complexity'] == 'passthrough' and candidate['tbd'] == 0 and candidate['tests'] > 0 else (
                        'needs-test' if candidate['complexity'] == 'passthrough' else 'not-passthrough'
                    )

                    md.append(f"{i}. **{candidate['jira']}** - {candidate['capability']}\n")
                    md.append(f"   - Confidence: {candidate['score']:.2f}\n")
                    md.append(f"   - Complexity: `{candidate['complexity']}`\n")
                    md.append(f"   - Tests: {candidate['tests']} ({candidate['tbd']} TBD)\n")
                    md.append(f"   - Suggested status: `{status}`\n")
                    md.append(f"   - Matched keywords: {', '.join(candidate['matched_keywords'])}\n")
                    if candidate['description'] and not pd.isna(candidate['description']):
                        md.append(f"   - Description: {candidate['description']}\n")
                    md.append("\n")
            else:
                md.append("**No JIRA suggestions found** - May need new ticket or is out of scope\n\n")

            md.append("---\n\n")

    # Write to file
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(''.join(md))

    print(f"\n✓ Review guide written to: {output_path}")
    print(f"\nSummary:")
    print(f"  - {len(unmatched)} unmatched fields")
    print(f"  - {len(groups)} prefix groups")
    print(f"\nNext steps:")
    print(f"  1. Review {output_path}")
    print(f"  2. Edit ledger-mapped.csv with JIRA assignments")
    print(f"  3. Update feature_ref, test_ref, status, notes columns")


def interactive_review(ledger: pd.DataFrame, jira_mapping: pd.DataFrame):
    """Interactive CLI review mode."""
    unmatched = ledger[ledger['status'] == 'unmatched'].copy()

    if len(unmatched) == 0:
        print("✓ No unmatched fields! All fields have been mapped.")
        return

    print(f"\n=== Interactive Review Mode ===")
    print(f"Total unmatched fields: {len(unmatched)}\n")

    for idx, row in unmatched.iterrows():
        field_path = row['field_path']
        owner_type = row['owner_type']

        print(f"\n{'='*80}")
        print(f"Field: {field_path}")
        print(f"Owner: {owner_type}")
        print(f"Write mode: {row['write_mode']}, Hidden: {row['hidden']}")

        keywords = extract_field_keywords(field_path)
        print(f"Keywords: {', '.join(keywords)}")

        candidates = find_candidates(field_path, keywords, jira_mapping, top_n=3)

        if candidates:
            print("\nSuggested JIRA tickets:")
            for i, candidate in enumerate(candidates, 1):
                print(f"\n  {i}. {candidate['jira']} - {candidate['capability']}")
                print(f"     Confidence: {candidate['score']:.2f}, Complexity: {candidate['complexity']}")
                print(f"     Tests: {candidate['tests']} ({candidate['tbd']} TBD)")
                if candidate['matched_keywords']:
                    print(f"     Matched: {', '.join(candidate['matched_keywords'])}")
        else:
            print("\nNo suggestions found")

        print(f"\n{'='*80}")

        # Ask user to continue or quit
        response = input("\nPress Enter for next field, 'q' to quit, 's' to skip ahead 10: ")
        if response.lower() == 'q':
            break
        elif response.lower() == 's':
            continue


def main():
    parser = argparse.ArgumentParser(
        description='Review helper for unmatched fields - suggests JIRA candidates',
        formatter_class=argparse.RawDescriptionHelpFormatter
    )

    parser.add_argument(
        '--ledger',
        type=Path,
        default=Path('../ledger-builder/output/ledger-mapped.csv'),
        help='Path to mapped ledger CSV'
    )

    parser.add_argument(
        '--matrix',
        type=Path,
        default=Path('../matrix'),
        help='Path to matrix directory'
    )

    parser.add_argument(
        '--output',
        type=Path,
        default=Path('./output/review-guide.md'),
        help='Path to output review guide (default: ./output/review-guide.md)'
    )

    parser.add_argument(
        '--interactive', '-i',
        action='store_true',
        help='Interactive CLI review mode'
    )

    args = parser.parse_args()

    ledger_path = args.ledger.resolve()
    matrix_dir = args.matrix.resolve()
    output_path = args.output.resolve()

    print(f"Review Helper")
    print(f"Ledger: {ledger_path}")
    print(f"Matrix: {matrix_dir}")

    # Load data
    ledger, jira_mapping = load_data(ledger_path, matrix_dir)

    unmatched_count = len(ledger[ledger['status'] == 'unmatched'])
    print(f"\nUnmatched fields: {unmatched_count}")

    if args.interactive:
        interactive_review(ledger, jira_mapping)
    else:
        generate_review_guide(ledger, jira_mapping, output_path)


if __name__ == '__main__':
    main()
