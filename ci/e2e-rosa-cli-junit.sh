#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT
unset JUNIT_REPORT_PATH ARTIFACT_DIR LABEL_FILTER
unset ROSA_FOCUS ROSA_SKIP ROSA_LABEL_FILTER ROSA_GINKGO_FOCUS ROSA_GINKGO_SKIP ROSA_GINKGO_LABEL_FILTER

fixture_repo="$tmpdir/rosa"
fake_bin="$tmpdir/bin"
working_dir="$tmpdir/work"
mkdir -p "$fixture_repo" "$fake_bin" "$working_dir"
git -C "$fixture_repo" init -q
git -C "$fixture_repo" checkout -qb hyperfleet-v2
cat > "$fixture_repo/Makefile" <<'MAKEFILE'
.PHONY: install

install:
	@printf 'built\n' >> "$$BUILD_LOG"
MAKEFILE
git -C "$fixture_repo" add Makefile
git -C "$fixture_repo" -c user.name=validation -c user.email=validation@example.invalid commit -qm fixture

cat > "$fake_bin/ginkgo" <<'GINKGO'
#!/bin/sh
set -eu

printf 'BEGIN\n' >> "$GINKGO_ARGS_FILE"
printf '%s\n' "$@" >> "$GINKGO_ARGS_FILE"
printf '%s\n' "${HYPERFLEET_URL:-}" > "$GINKGO_ENV_FILE"
junit=
output_dir=
for arg do
	case "$arg" in
		--junit-report=*) junit=${arg#*=} ;;
		--output-dir=*) output_dir=${arg#*=} ;;
	esac
done
[ -n "$junit" ] && [ -n "$output_dir" ] || exit 90
[ -d "$output_dir" ] || exit 91
failures=0
if [ "${GINKGO_EXIT_STATUS:-0}" -ne 0 ]; then
	failures=1
fi
printf '<testsuite name="%s" tests="1" failures="%s" errors="0" skipped="0" time="0.1"/>\n' \
	"$junit" "$failures" > "$output_dir/$junit"
exit "${GINKGO_EXIT_STATUS:-0}"
GINKGO
chmod +x "$fake_bin/ginkgo"

args_file="$tmpdir/ginkgo-args"
env_file="$tmpdir/ginkgo-env"
run_target() {
	local expected_status=$1
	local output_log=$2
	shift 2
	local status=0
	local ginkgo_status=13
	if [ "$expected_status" -eq 0 ]; then
		ginkgo_status=0
	fi
	: > "$args_file"

	if env MAKEFLAGS= MAKEOVERRIDES= PATH="$fake_bin:$PATH" GINKGO_ARGS_FILE="$args_file" GINKGO_ENV_FILE="$env_file" GINKGO_EXIT_STATUS="$ginkgo_status" \
		BUILD_LOG="$tmpdir/build.log" \
		make --no-print-directory -s -C "$working_dir" -f "$repo_root/Makefile" \
		test-e2e-rosa-cli "ROSA_REPO_URL=file://$fixture_repo" ROSA_REPO_BRANCH=hyperfleet-v2 \
		ROSA_BUILD_TARGET=install "$@" >"$output_log" 2>&1; then
		status=0
	else
		status=$?
	fi
	if [ "$status" -ne "$expected_status" ]; then
		cat "$output_log" >&2
		echo "expected make status $expected_status, got $status" >&2
		return 1
	fi
}

assert_arg() {
	if ! grep -Fxq -- "$1" "$args_file"; then
		echo "expected Ginkgo argument not found: $1" >&2
		cat "$args_file" >&2
		return 1
	fi
}

assert_file() {
	if [ ! -f "$1" ]; then
		cat "$2" >&2
		echo "expected report file not found: $1" >&2
		return 1
	fi
}

assert_combined_report() {
	python3 - "$1" "$2" "$3" <<'PY'
import sys
import xml.etree.ElementTree as ET

path, expected_suites, expected_failures = sys.argv[1:]
root = ET.parse(path).getroot()
assert root.tag == "testsuites", f"expected testsuites root, got {root.tag}"
suites = root.findall("testsuite")
assert len(suites) == int(expected_suites), f"expected {expected_suites} suites, got {len(suites)}"
assert root.get("tests") == expected_suites, f"unexpected test count: {root.get('tests')}"
assert root.get("failures") == expected_failures, f"unexpected failure count: {root.get('failures')}"
PY
}

assert_default_runs() {
	python3 - "$args_file" <<'PY'
import sys

args_path = sys.argv[1]
labels = ("day1-post", "day2", "destructive", "destroy", "destroy-post")
calls = []
for line in open(args_path, encoding="utf-8"):
    line = line.rstrip("\n")
    if line == "BEGIN":
        calls.append([])
    elif calls:
        calls[-1].append(line)
assert len(calls) == len(labels), f"expected {len(labels)} Ginkgo runs, got {len(calls)}"
for label, args in zip(labels, calls):
    expected_filter = f"--label-filter=({label}) && !hyperfleet-na && !hyperfleet-deferred"
    assert args[:4] == ["run", "-v", "--timeout", "3h"], f"unexpected base args for {label}: {args}"
    assert args[4] == f"--junit-report=junit-rosa-cli-{label}.xml", f"unexpected report for {label}: {args}"
    assert args[5].startswith("--output-dir="), f"missing output dir for {label}: {args}"
    assert args[6] == expected_filter, f"unexpected label filter for {label}: {args}"
    assert args[7] == "./tests/e2e/", f"unexpected spec path for {label}: {args}"
PY
}

filtered_default_label_filter='(hyperfleet-sanity) && !hyperfleet-na && !hyperfleet-deferred'
filtered_report="$tmpdir/filtered reports/custom.xml"
run_target 2 "$tmpdir/filtered.log" \
	"JUNIT_REPORT_PATH=$filtered_report" \
	ROSA_GINKGO_FOCUS=focus-only \
	HYPERFLEET_URL=https://hyperfleet.example
assert_file "$filtered_report" "$tmpdir/filtered.log"
if [ "$(cat "$env_file")" != "https://hyperfleet.example" ]; then
	echo "HYPERFLEET_URL was not passed through to Ginkgo" >&2
	cat "$env_file" >&2
	exit 1
fi
assert_arg "--junit-report=custom.xml"
assert_arg "--output-dir=$(dirname "$filtered_report")"
assert_arg '--focus=focus-only'
assert_arg "--label-filter=$filtered_default_label_filter"
if grep -Eq '^--skip=' "$args_file"; then
	echo "focus-only run unexpectedly received a skip filter" >&2
	cat "$args_file" >&2
	exit 1
fi

skip_report="$tmpdir/skip/junit.xml"
run_target 2 "$tmpdir/skip.log" "JUNIT_REPORT_PATH=$skip_report" ROSA_GINKGO_SKIP=skip-only
assert_file "$skip_report" "$tmpdir/skip.log"
assert_arg '--skip=skip-only'
assert_arg "--label-filter=$filtered_default_label_filter"

label_report="$tmpdir/label/junit.xml"
run_target 2 "$tmpdir/label.log" "JUNIT_REPORT_PATH=$label_report" 'ROSA_GINKGO_LABEL_FILTER=day1 && hyperfleet-validated'
assert_file "$label_report" "$tmpdir/label.log"
assert_arg '--label-filter=(day1 && hyperfleet-validated) && !hyperfleet-na && !hyperfleet-deferred'

artifact_dir="$tmpdir/artifact reports"
run_target 2 "$tmpdir/artifact-default.log" "ARTIFACT_DIR=$artifact_dir"
artifact_report="$artifact_dir/junit-rosa-cli.xml"
assert_file "$artifact_report" "$tmpdir/artifact-default.log"
assert_default_runs
assert_combined_report "$artifact_report" 5 5

run_target 0 "$tmpdir/fallback-default.log"
fallback_report="$working_dir/test-results/junit-rosa-cli.xml"
assert_file "$fallback_report" "$tmpdir/fallback-default.log"
assert_default_runs
assert_combined_report "$fallback_report" 5 0

build_count=$(wc -l < "$tmpdir/build.log" | tr -d ' ')
[ "$build_count" -eq 5 ]

echo "Filtered and unfiltered ROSA CLI JUnit paths validated."
