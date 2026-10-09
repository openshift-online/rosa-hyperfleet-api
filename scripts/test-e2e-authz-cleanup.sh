#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -r "$work"' EXIT
mkdir -p "$work/bin"

cat > "$work/bin/podman" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$CASE_DIR/calls"
case $1 in
    version) echo fake-podman ;;
    run)
        label=
        while (($#)); do
            if [[ $1 == --label ]]; then label=$2; fi
            shift
        done
        if [[ $MODE == collision ]]; then exit 125; fi
        printf '%s' "${label#*=}" > "$CASE_DIR/owner"
        touch "$CASE_DIR/created"
        sleep 30
        ;;
    inspect)
        if [[ $MODE == collision ]]; then
            printf '%s\n' 'foreign-id foreign-owner'
        elif [[ -f $CASE_DIR/created ]]; then
            printf 'owned-id %s\n' "$(< "$CASE_DIR/owner")"
        else
            exit 1
        fi
        ;;
    logs) echo "container logs for ${@: -1}" ;;
    stop) rm -f "$CASE_DIR/created" ;;
    *) exit 2 ;;
esac
SH
cat > "$work/bin/go" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ $1 == version ]]; then echo fake-go; exit 0; fi
if [[ $MODE != collision ]]; then
    printf '%s\n' '<testsuite name="preserved" tests="1" failures="0"/>' > "$AUTHZ_HTTP_OUTPUT_DIR/junit-authz-http.xml"
fi
podman run -d --rm --name pgctl-test-collision -p 49152:5432 postgres:16 || {
    podman logs pgctl-test-collision
    podman stop pgctl-test-collision
    exit 1
}
SH
chmod +x "$work/bin/"*

export PATH="$work/bin:$PATH"
unset PGCTL_DSN
failures=0
# Job control lets the background runner receive INT instead of inheriting SIG_IGN.
set -m
for mode in collision TERM INT; do
    export CASE_DIR="$work/$mode" MODE="$mode"
    mkdir -p "$CASE_DIR"
    bash "$root/scripts/test-e2e-authz.sh" "$CASE_DIR" > "$CASE_DIR/runner.log" 2>&1 &
    runner=$!
    if [[ $mode != collision ]]; then
        for ((attempt=0; attempt<200; attempt++)); do
            if [[ -f $CASE_DIR/created ]]; then break; fi
            if ! kill -0 "$runner" 2>/dev/null; then break; fi
            sleep 0.05
        done
        kill -"$mode" "$runner" 2>/dev/null || true
    fi
    code=0
    wait "$runner" || code=$?
    output=$(find "$CASE_DIR" -maxdepth 1 -type d -name 'authz-http-*' -print)
    # Publishing must be loopback-only at the local runner's Podman boundary.
    if grep -q -- ' -p 127.0.0.1:49152:5432 ' "$CASE_DIR/calls"; then
        echo "PASS loopback-only PostgreSQL publish for $mode"
    else
        echo "FAIL PostgreSQL publish must bind to loopback for $mode"
        failures=$((failures+1))
    fi
    if [[ $mode == collision ]]; then
        if [[ $code == 1 ]] && ! grep -Eq '^(logs|stop) ' "$CASE_DIR/calls" &&
            grep -q 'Runner failed before Go report' "$output/junit-authz-http.xml"; then
            echo 'PASS foreign collision rejected by shim and EXIT cleanup; fallback JUnit preserved'
        else
            echo 'FAIL foreign collision must never reach real Podman logs/stop'
            failures=$((failures+1))
        fi
    else
        expected=143
        if [[ $mode == INT ]]; then expected=130; fi
        if [[ $code == "$expected" ]] && [[ ! -f $CASE_DIR/created ]] &&
            grep -q '^run --label io.hyperfleet.authz-http-owner=' "$CASE_DIR/calls" &&
            grep -q '^inspect ' "$CASE_DIR/calls" &&
            grep -q '^logs owned-id$' "$CASE_DIR/calls" &&
            grep -q '^stop --time 5 owned-id$' "$CASE_DIR/calls" &&
            grep -q 'container logs for owned-id' "$output/pgctl-test-collision.log" &&
            [[ $(< "$output/junit-authz-http.xml") == '<testsuite name="preserved" tests="1" failures="0"/>' ]]; then
            echo "PASS owned interrupted setup $mode; inspected ID logged/stopped; existing JUnit preserved; exit $code"
        else
            echo "FAIL owned interrupted setup $mode; exit $code, want $expected"
            failures=$((failures+1))
        fi
    fi
    printf 'Podman calls for %s\n' "$mode"
    if [[ -f $CASE_DIR/calls ]]; then cat "$CASE_DIR/calls"; fi
    if [[ -f $output/pgctl-test-collision.log ]]; then cat "$output/pgctl-test-collision.log"; fi
    if [[ -f $output/junit-authz-http.xml ]]; then cat "$output/junit-authz-http.xml"; fi
done
exit "$failures"
