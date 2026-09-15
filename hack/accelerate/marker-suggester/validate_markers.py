#!/usr/bin/env python3
"""
Marker Validator & Suggester - Stage 2 of V2 Passthrough Feature Acceleration Pipeline

Validates existing marker assignments and suggests improvements based on:
- Field location and naming patterns
- Write-mode consistency rules
- Hidden flag logic
- Marker conventions across similar fields

Usage:
    python validate_markers.py [--ledger <path>] [--output <report.md>]
"""

import argparse
import re
import sys
from pathlib import Path
from typing import Dict, List, Tuple, Optional

import pandas as pd


class MarkerValidator:
    """Validates and suggests marker improvements."""

    def __init__(self, ledger: pd.DataFrame):
        self.ledger = ledger
        self.issues = []
        self.suggestions = []

    def validate_all(self):
        """Run all validation checks."""
        self._validate_write_mode_consistency()
        self._validate_hidden_flag_logic()
        self._validate_immutable_fields()
        self._validate_service_set_fields()
        self._detect_marker_inconsistencies()
        self._suggest_marker_improvements()

    def _validate_write_mode_consistency(self):
        """Check write_mode is consistent with field location."""
        for idx, row in self.ledger.iterrows():
            field_path = row['field_path']
            write_mode = row['write_mode']
            owner_type = row['owner_type']

            # Rule: Fields in spec.hostedCluster.* (passthrough) should usually be service-set or immutable
            if 'hostedCluster.' in field_path and write_mode == 'mutable':
                # Exception: some hostedCluster fields ARE mutable (e.g., autoNode, deleteProtection)
                # These are typically top-level or in configuration.*
                if not any(pattern in field_path for pattern in [
                    'hostedCluster.autoNode',
                    'hostedCluster.configuration.kubelet.containerLogMax',
                    'hostedCluster.configuration.kubelet.imageGC',
                ]):
                    self.issues.append({
                        'field': field_path,
                        'severity': 'warning',
                        'category': 'write-mode',
                        'issue': f"Mutable field in hostedCluster (usually service-set)",
                        'current': f"write_mode={write_mode}",
                        'suggestion': "Consider write_mode=service-set unless field is user-mutable",
                        'confidence': 0.6
                    })

    def _validate_hidden_flag_logic(self):
        """Check hidden flag is consistent with write_mode."""
        for idx, row in self.ledger.iterrows():
            field_path = row['field_path']
            write_mode = row['write_mode']
            hidden = row['hidden']

            # Rule: service-set fields are often hidden (not always)
            if write_mode == 'service-set' and not hidden:
                # This is actually valid - some service-set fields are public
                # Only flag if it's a field that looks like it should be hidden
                if any(keyword in field_path.lower() for keyword in [
                    'internal', 'arn', 'secret', 'key', 'token', 'credential'
                ]):
                    self.issues.append({
                        'field': field_path,
                        'severity': 'info',
                        'category': 'visibility',
                        'issue': f"service-set field with sensitive name is public",
                        'current': f"hidden={hidden}, write_mode={write_mode}",
                        'suggestion': "Consider hidden=true for service-managed sensitive fields",
                        'confidence': 0.7
                    })

            # Rule: mutable fields should generally be public (hidden=false)
            if write_mode == 'mutable' and hidden:
                self.issues.append({
                    'field': field_path,
                    'severity': 'warning',
                    'category': 'visibility',
                    'issue': f"Mutable field is hidden (users can't modify)",
                    'current': f"hidden={hidden}, write_mode={write_mode}",
                    'suggestion': "Mutable fields should usually be public (hidden=false)",
                    'confidence': 0.8
                })

    def _validate_immutable_fields(self):
        """Check immutable fields are correctly marked."""
        for idx, row in self.ledger.iterrows():
            field_path = row['field_path']
            write_mode = row['write_mode']

            # Rule: Fields with names suggesting immutability
            immutable_patterns = ['id', 'arn', 'name', 'issuerurl', 'installerrole']
            if write_mode != 'immutable':
                field_lower = field_path.lower()
                if any(field_lower.endswith(pattern) for pattern in immutable_patterns):
                    # Check if it's actually set to service-set (which is also reasonable)
                    if write_mode == 'service-set':
                        # service-set is acceptable for IDs/ARNs (service creates them)
                        pass
                    elif write_mode == 'mutable':
                        # Mutable ID/ARN is suspicious
                        self.issues.append({
                            'field': field_path,
                            'severity': 'warning',
                            'category': 'immutability',
                            'issue': f"Field name suggests immutability but marked mutable",
                            'current': f"write_mode={write_mode}",
                            'suggestion': "Consider write_mode=immutable or service-set",
                            'confidence': 0.7
                        })

    def _validate_service_set_fields(self):
        """Check service-set fields are appropriate."""
        for idx, row in self.ledger.iterrows():
            field_path = row['field_path']
            write_mode = row['write_mode']
            hidden = row['hidden']

            # Rule: service-set fields should be in hostedCluster.* (passthrough) or service-managed
            if write_mode == 'service-set':
                # Most service-set fields should be in hostedCluster or platform-specific
                if not any(prefix in field_path for prefix in [
                    'hostedCluster.', 'spec.accountId', 'spec.creatorARN',
                    'spec.installerRoleArn', 'spec.supportRoleArn', 'spec.internalId'
                ]):
                    self.issues.append({
                        'field': field_path,
                        'severity': 'info',
                        'category': 'write-mode',
                        'issue': f"service-set field outside typical locations",
                        'current': f"write_mode={write_mode}",
                        'suggestion': "Verify this field is actually service-managed",
                        'confidence': 0.5
                    })

    def _detect_marker_inconsistencies(self):
        """Find similar fields with different markers."""
        # Group by field name suffix (e.g., all *LogMaxFiles)
        field_groups = {}
        for idx, row in self.ledger.iterrows():
            field_path = row['field_path']
            # Extract last component
            parts = field_path.split('.')
            suffix = parts[-1]

            if suffix not in field_groups:
                field_groups[suffix] = []
            field_groups[suffix].append(row)

        # Check for inconsistencies within groups
        for suffix, fields in field_groups.items():
            if len(fields) < 2:
                continue

            # Check write_mode consistency
            write_modes = set(f['write_mode'] for f in fields)
            if len(write_modes) > 1:
                # Multiple write modes for same field name
                field_paths = [f['field_path'] for f in fields]
                self.issues.append({
                    'field': f"Group: *{suffix}",
                    'severity': 'info',
                    'category': 'consistency',
                    'issue': f"Similar fields have different write_modes: {write_modes}",
                    'current': f"Fields: {', '.join(field_paths[:3])}...",
                    'suggestion': "Review marker consistency across similar fields",
                    'confidence': 0.6
                })

    def _suggest_marker_improvements(self):
        """Suggest marker improvements based on analysis."""
        # Suggest making certain kubelet.* fields consistent
        kubelet_fields = self.ledger[self.ledger['field_path'].str.contains('kubelet.', na=False)]

        # Most kubelet fields should have consistent markers
        kubelet_mutable = kubelet_fields[kubelet_fields['write_mode'] == 'mutable']
        kubelet_service_set = kubelet_fields[kubelet_fields['write_mode'] == 'service-set']

        if len(kubelet_mutable) > 0 and len(kubelet_service_set) > 0:
            self.suggestions.append({
                'category': 'consistency',
                'suggestion': f"Kubelet fields split between mutable ({len(kubelet_mutable)}) and service-set ({len(kubelet_service_set)})",
                'action': "Consider standardizing kubelet configuration field markers",
                'confidence': 0.7
            })

    def get_issues(self) -> List[Dict]:
        """Return all validation issues."""
        return sorted(self.issues, key=lambda x: (
            {'warning': 0, 'info': 1}[x['severity']],
            -x['confidence']
        ))

    def get_suggestions(self) -> List[Dict]:
        """Return all marker suggestions."""
        return sorted(self.suggestions, key=lambda x: -x['confidence'])


def generate_report(validator: MarkerValidator, output_path: Path):
    """Generate markdown validation report."""
    issues = validator.get_issues()
    suggestions = validator.get_suggestions()

    md = []
    md.append("# Marker Validation Report - Stage 2\n\n")
    md.append(f"**Date:** {pd.Timestamp.now().strftime('%Y-%m-%d %H:%M:%S')}\n")
    md.append(f"**Total fields analyzed:** {len(validator.ledger)}\n")
    md.append(f"**Issues found:** {len(issues)}\n")
    md.append(f"**Suggestions:** {len(suggestions)}\n\n")

    md.append("---\n\n")

    # Summary by severity
    warnings = [i for i in issues if i['severity'] == 'warning']
    infos = [i for i in issues if i['severity'] == 'info']

    md.append("## Summary\n\n")
    md.append(f"- ⚠️ **Warnings:** {len(warnings)} (should review)\n")
    md.append(f"- ℹ️ **Info:** {len(infos)} (informational)\n\n")

    # Summary by category
    categories = {}
    for issue in issues:
        cat = issue['category']
        categories[cat] = categories.get(cat, 0) + 1

    md.append("### Issues by Category\n\n")
    for cat, count in sorted(categories.items(), key=lambda x: -x[1]):
        md.append(f"- **{cat}:** {count}\n")

    md.append("\n---\n\n")

    # Validation issues
    if issues:
        md.append("## Validation Issues\n\n")

        for issue in issues:
            icon = "⚠️" if issue['severity'] == 'warning' else "ℹ️"
            md.append(f"### {icon} `{issue['field']}`\n\n")
            md.append(f"**Category:** {issue['category']}  \n")
            md.append(f"**Issue:** {issue['issue']}  \n")
            md.append(f"**Current:** {issue['current']}  \n")
            md.append(f"**Suggestion:** {issue['suggestion']}  \n")
            md.append(f"**Confidence:** {issue['confidence']:.0%}\n\n")
            md.append("---\n\n")
    else:
        md.append("## ✅ No validation issues found!\n\n")

    # Marker suggestions
    if suggestions:
        md.append("## Marker Improvement Suggestions\n\n")

        for sug in suggestions:
            md.append(f"### {sug['category'].title()}\n\n")
            md.append(f"**Observation:** {sug['suggestion']}  \n")
            md.append(f"**Recommended action:** {sug['action']}  \n")
            md.append(f"**Confidence:** {sug['confidence']:.0%}\n\n")
            md.append("---\n\n")

    # Conclusion
    md.append("## Conclusion\n\n")
    if len(warnings) == 0:
        md.append("✅ **All critical marker validations passed!**\n\n")
    else:
        md.append(f"⚠️ **{len(warnings)} warnings found** - review recommended.\n\n")

    md.append("### Next Steps\n\n")
    md.append("1. Review warnings and update markers in source code if needed\n")
    md.append("2. Re-run `make codegen-registry` to regenerate field_metadata.json\n")
    md.append("3. Re-run `make accel-validate-markers` to verify fixes\n")
    md.append("4. Proceed to Stage 3 (Test Mapper) once markers are validated\n\n")

    # Write report
    output_path.parent.mkdir(parents=True, exist_ok=True)
    output_path.write_text(''.join(md))


def print_summary(validator: MarkerValidator):
    """Print validation summary to console."""
    issues = validator.get_issues()
    warnings = [i for i in issues if i['severity'] == 'warning']
    infos = [i for i in issues if i['severity'] == 'info']

    print("\n=== Marker Validation Summary ===")
    print(f"Total fields analyzed: {len(validator.ledger)}")
    print(f"\nIssues found:")
    print(f"  ⚠️  Warnings: {len(warnings)}")
    print(f"  ℹ️  Info: {len(infos)}")

    if len(issues) > 0:
        print("\nTop issues:")
        for issue in issues[:5]:
            icon = "⚠️" if issue['severity'] == 'warning' else "ℹ️"
            print(f"  {icon} {issue['field']}: {issue['issue']}")
    else:
        print("\n✅ No issues found!")


def main():
    parser = argparse.ArgumentParser(
        description='Validate marker assignments and suggest improvements',
        formatter_class=argparse.RawDescriptionHelpFormatter
    )

    parser.add_argument(
        '--ledger',
        type=Path,
        default=Path('../ledger-builder/output/ledger.csv'),
        help='Path to ledger CSV (default: ../ledger-builder/output/ledger.csv)'
    )

    parser.add_argument(
        '--output',
        type=Path,
        default=Path('./output/marker-validation-report.md'),
        help='Path to output validation report (default: ./output/marker-validation-report.md)'
    )

    args = parser.parse_args()

    ledger_path = args.ledger.resolve()
    output_path = args.output.resolve()

    if not ledger_path.exists():
        print(f"Error: Ledger file not found: {ledger_path}", file=sys.stderr)
        print("\nRun 'make accel-build-ledger' first to generate the ledger.", file=sys.stderr)
        sys.exit(1)

    print(f"Marker Validator - Stage 2")
    print(f"Ledger: {ledger_path}")
    print(f"Output: {output_path}")

    # Load ledger
    ledger = pd.read_csv(ledger_path)
    print(f"\n✓ Loaded {len(ledger)} fields")

    # Validate markers
    print(f"\n✓ Validating markers...")
    validator = MarkerValidator(ledger)
    validator.validate_all()

    # Print summary
    print_summary(validator)

    # Generate report
    print(f"\n✓ Generating validation report...")
    generate_report(validator, output_path)

    print(f"\n✓ Validation report written to: {output_path}")
    print("\n✓ Marker validation complete!")


if __name__ == '__main__':
    main()
