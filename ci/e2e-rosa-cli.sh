#!/usr/bin/env bash
set -euo pipefail

: "${ROSA_JUNIT_REPORT_PATH:?ROSA_JUNIT_REPORT_PATH must be set by test-e2e-rosa-cli}"
: "${ROSA_REPO_URL:?ROSA_REPO_URL must be set}"
: "${ROSA_REPO_BRANCH:?ROSA_REPO_BRANCH must be set}"
: "${ROSA_BUILD_TARGET:?ROSA_BUILD_TARGET must be set}"
: "${ROSA_GINKGO_EFFECTIVE_LABEL_FILTER:?ROSA_GINKGO_EFFECTIVE_LABEL_FILTER must be set}"

ROSA_TMPDIR=$(mktemp -d)
trap 'rm -rf -- "$ROSA_TMPDIR"' EXIT

ROSA_JUNIT_REPORT_DIR=$(dirname "$ROSA_JUNIT_REPORT_PATH")
ROSA_JUNIT_REPORT_NAME=$(basename "$ROSA_JUNIT_REPORT_PATH")
mkdir -p "$ROSA_JUNIT_REPORT_DIR"
ROSA_JUNIT_REPORT_DIR=$(cd "$ROSA_JUNIT_REPORT_DIR" && pwd)

echo "Temporary directory: $ROSA_TMPDIR"
echo "Cloning rosa repo from $ROSA_REPO_URL@$ROSA_REPO_BRANCH..."
git clone --depth=1 --branch "$ROSA_REPO_BRANCH" "$ROSA_REPO_URL" "$ROSA_TMPDIR"

cd "$ROSA_TMPDIR"
echo "Building rosa CLI..."
read -r -a rosa_build_targets <<< "$ROSA_BUILD_TARGET"
"${MAKE:-make}" "${rosa_build_targets[@]}"
export PATH="$PWD:$PATH"

echo "Running rosa hyperfleet E2E tests..."
name=${CLUSTER_NAME:-hf-e2e-$(date +%s)}
export HYPERFLEET_URL="${HYPERFLEET_URL:-}"
export CLUSTER_NAME="$name"
export OPERATOR_ROLES_PREFIX="${OPERATOR_ROLES_PREFIX:-$name}"
export AWS_DEFAULT_REGION="${AWS_DEFAULT_REGION:-${AWS_REGION:-}}"
export GOTOOLCHAIN=auto
export TEST_PROFILE="${TEST_PROFILE:-rosa-hyperfleet-basic}"
export TEST_PROFILE_DIR="$ROSA_TMPDIR/tests/ci/data/profiles"
export WORKSPACE="$ROSA_TMPDIR"

if [[ -n "${ROSA_GINKGO_FOCUS:-}${ROSA_GINKGO_SKIP:-}${ROSA_GINKGO_LABEL_FILTER:-}" ]]; then
	echo "Running with custom ginkgo filters..."
	echo "✅ ROSA_GINKGO_LABEL_FILTER=${ROSA_GINKGO_LABEL_FILTER}..."
	ginkgo_args=(
		run -v --timeout 3h
		"--junit-report=$ROSA_JUNIT_REPORT_NAME"
		"--output-dir=$ROSA_JUNIT_REPORT_DIR"
		"--label-filter=$ROSA_GINKGO_EFFECTIVE_LABEL_FILTER"
	)
	if [[ -n "${ROSA_GINKGO_FOCUS:-}" ]]; then
		ginkgo_args+=("--focus=$ROSA_GINKGO_FOCUS")
	fi
	if [[ -n "${ROSA_GINKGO_SKIP:-}" ]]; then
		ginkgo_args+=("--skip=$ROSA_GINKGO_SKIP")
	fi
	echo "---"
	echo "${ginkgo_args[@]}"
	ginkgo "${ginkgo_args[@]}" ./tests/e2e/
else
	echo "Running default rosa e2e-hyperfleet target..."
	rosa_ginkgo_labels=(
		day1
		day1-post
		day2
		destructive
		destroy
		destroy-post
	)
	ROSA_JUNIT_TEMP_DIR="$ROSA_TMPDIR/junit-reports"
	mkdir -p "$ROSA_JUNIT_TEMP_DIR"
	junit_reports=()
	ginkgo_status=0

	for label in "${rosa_ginkgo_labels[@]}"; do
		report_name="junit-rosa-cli-${label}.xml"
		label_filter="(${label})&& hyperfleet-validated && !hyperfleet-na && !hyperfleet-deferred"
		ginkgo_args=(
			run -v --timeout 3h
			"--junit-report=$report_name"
			"--output-dir=$ROSA_JUNIT_TEMP_DIR"
			"--label-filter=$label_filter"
		)

		echo "Running Ginkgo label $label with filter: $label_filter"
		if ginkgo "${ginkgo_args[@]}" ./tests/e2e/; then
			:
		else
			status=$?
			if ((ginkgo_status == 0)); then
				ginkgo_status=$status
			fi
		fi
		junit_reports+=("$ROSA_JUNIT_TEMP_DIR/$report_name")
	done

	python3 - "$ROSA_JUNIT_REPORT_DIR/$ROSA_JUNIT_REPORT_NAME" "${junit_reports[@]}" <<'PY'
import os
import sys
import xml.etree.ElementTree as ET

output_path, *report_paths = sys.argv[1:]
if not report_paths:
    raise SystemExit("no per-label JUnit reports were generated")

suites = []
for report_path in report_paths:
    if not os.path.isfile(report_path):
        raise SystemExit(f"missing per-label JUnit report: {report_path}")
    root = ET.parse(report_path).getroot()
    if root.tag == "testsuite":
        suites.append(root)
    elif root.tag == "testsuites":
        suites.extend(root.findall("testsuite"))
    else:
        raise SystemExit(f"unexpected JUnit root element in {report_path}: {root.tag}")

counts = {name: 0 for name in ("tests", "failures", "errors", "skipped", "disabled")}
elapsed = 0.0
for suite in suites:
    for name in counts:
        counts[name] += int(suite.get(name, "0"))
    elapsed += float(suite.get("time", "0"))

combined = ET.Element(
    "testsuites",
    {
        **{name: str(value) for name, value in counts.items()},
        "time": f"{elapsed:.3f}",
    },
)
combined.extend(suites)
temporary_path = output_path + ".tmp"
ET.ElementTree(combined).write(temporary_path, encoding="utf-8", xml_declaration=True)
os.replace(temporary_path, output_path)
PY
	exit "$ginkgo_status"


fi
