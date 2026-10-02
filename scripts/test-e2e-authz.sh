#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
output=${1:-"$root/test-results"}/authz-http-$(date +%Y%m%dT%H%M%S)-$$
mkdir -p "$output"
output=$(cd "$output" && pwd)
export AUTHZ_HTTP_OUTPUT_DIR="$output"
export AUTHZ_HTTP_REQUIRED=1
export AUTHZ_HTTP_API_BINARY="$root/bin/rosa-hyperfleet-api"

real_podman=$(command -v podman || true)
shim=$(mktemp -d)
export AUTHZ_HTTP_PODMAN="$real_podman"
export AUTHZ_HTTP_CONTAINERS="$shim/containers"
export AUTHZ_HTTP_OWNER="${shim##*/}"
test_pid=

# Keep local ports on loopback and record intent before creation. Labels prove ownership.
cat > "$shim/podman" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
if [[ ${1:-} == run ]]; then
    args=("$@")
    for ((i=0; i<${#args[@]}; i++)); do
        if [[ ${args[i]} == --name ]]; then
            printf '%s\n' "${args[i+1]}" >> "$AUTHZ_HTTP_CONTAINERS"
        elif [[ ${args[i]} == -p ]]; then
            args[i+1]="127.0.0.1:${args[i+1]}"
        fi
    done
    set -- run --label "io.hyperfleet.authz-http-owner=$AUTHZ_HTTP_OWNER" "${args[@]:1}"
fi
if [[ ${1:-} == logs || ${1:-} == stop ]]; then
    container=${@: -1}
    ownership=$("$AUTHZ_HTTP_PODMAN" inspect --type container --format '{{.Id}} {{ index .Config.Labels "io.hyperfleet.authz-http-owner" }}' "$container") || exit 0
    read -r id owner <<< "$ownership"
    [[ -n $id && $owner == "$AUTHZ_HTTP_OWNER" ]] || exit 0
    # Use the inspected ID so a replacement with the same name cannot be touched.
    if [[ $1 == stop ]]; then
        "$AUTHZ_HTTP_PODMAN" logs "$id" > "$AUTHZ_HTTP_OUTPUT_DIR/$container.log" 2>&1 || true
    fi
    set -- "${@:1:$(($#-1))}" "$id"
fi
exec "$AUTHZ_HTTP_PODMAN" "$@"
SH
chmod +x "$shim/podman"
export PATH="$shim:$PATH"

cleanup() {
    code=$?
    trap - EXIT INT TERM
    if [[ -n $test_pid ]]; then
        kill -TERM -- "-$test_pid" 2>/dev/null || true
        wait "$test_pid" 2>/dev/null || true
    fi
    if [[ -f $AUTHZ_HTTP_CONTAINERS ]]; then
        while IFS= read -r container; do
            if [[ ! -s $output/$container.log ]]; then
                "$shim/podman" logs "$container" > "$output/$container.log" 2>&1 || true
            fi
            "$shim/podman" stop --time 5 "$container" >/dev/null 2>&1 || true
        done < "$AUTHZ_HTTP_CONTAINERS"
    fi
    if [[ ! -f $output/junit-authz-http.xml ]]; then
        printf '%s\n' '<testsuite name="TestAuthzHTTP" tests="1" failures="1"><testcase name="runner"><failure message="Runner failed before Go report; see authz-http.log"/></testcase></testsuite>' > "$output/junit-authz-http.xml"
    fi
    rm -r "$shim"
    exit "$code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
echo "Local HTTP artifacts: $output"
if [[ -n ${PGCTL_DSN:-} ]]; then
    echo 'Refusing PGCTL_DSN. This target only uses a runner-owned disposable database.' | tee "$output/authz-http.log" >&2
    exit 1
fi
if [[ -z $real_podman ]] || ! command -v setsid >/dev/null; then
    echo 'Podman and setsid are required for isolated execution and cleanup.' | tee "$output/authz-http.log" >&2
    exit 1
fi
{
    printf 'API and test baseline revision: '
    git -C "$root" rev-parse HEAD
    go version
    "$real_podman" version
    sha256sum "$AUTHZ_HTTP_API_BINARY" "$root/test/e2e-api/authz_http_test.go" "$root/test/e2e-api/testdata/authz-http.json"
    printf '%s\n' 'Selected test: TestAuthzHTTP only' 'PostgreSQL: docker.io/library/postgres:16-alpine, runner-owned' 'API/health/metrics bind: 127.0.0.1, ephemeral ports' 'AUTHZ_RESOLVER=config AWS_REGION=us-east-1 AWS_EC2_METADATA_DISABLED=true' 'No AWS credentials; rate limiting disabled'
} > "$output/run-manifest.txt" 2>&1
cd "$root/test"
setsid go test -count=1 -timeout=10m -run '^TestAuthzHTTP$' -v ./e2e-api > "$output/authz-http.log" 2>&1 &
test_pid=$!
code=0
wait "$test_pid" || code=$?
cat "$output/authz-http.log"
exit "$code"
