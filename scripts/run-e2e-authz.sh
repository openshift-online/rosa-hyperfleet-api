#!/usr/bin/env bash
set -euo pipefail

BINARY="./rosa-hyperfleet-api"
PIDFILE="$(mktemp)"
LOGFILE="./rosa-hyperfleet-api.log"
BASE_URL="http://localhost:8000"
READY_URL="${BASE_URL}/api/v0/ready"
MAX_WAIT=30

cleanup() {
    echo "Cleaning up..."
    if [[ -f "$PIDFILE" ]] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        kill "$(cat "$PIDFILE")" 2>/dev/null || true
        wait "$(cat "$PIDFILE")" 2>/dev/null || true
    fi
    rm -f "$PIDFILE"
}
trap cleanup EXIT INT TERM

echo "Building service..."
go build -o "$BINARY" ./cmd/rosa-hyperfleet-api

echo "Starting service..."
DYNAMODB_ENDPOINT=http://localhost:8180 \
CEDAR_AGENT_ENDPOINT=http://localhost:8181 \
AUTHZ_ENABLED=true \
    "$BINARY" serve \
        --log-level=debug \
        --log-format=text > "$LOGFILE" 2>&1 &
echo $! > "$PIDFILE"

echo "Waiting for service to be ready..."
for i in $(seq 1 $MAX_WAIT); do
    if curl -sf "$READY_URL" > /dev/null 2>&1; then
        echo "Service ready after ${i}s"
        break
    fi
    if ! kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        echo "Service exited unexpectedly. Log output:"
        cat "$LOGFILE"
        exit 1
    fi
    sleep 1
done

if ! curl -sf "$READY_URL" > /dev/null 2>&1; then
    echo "Service failed to become ready after ${MAX_WAIT}s. Log output:"
    tail -50 "$LOGFILE"
    exit 1
fi

echo "Running authz E2E tests..."
E2E_BASE_URL="$BASE_URL" ginkgo -v --focus="Authz" ./test/e2e
