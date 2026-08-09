#!/usr/bin/env bash
set -euo pipefail

# Local authz E2E test runner with LocalStack
# Prerequisites:
#   podman-compose -f hack/podman-compose.e2e-authz.yaml up -d

BINARY="./rosa-hyperfleet-api"
PIDFILE="$(mktemp)"
LOGFILE="./rosa-hyperfleet-api.log"
BASE_URL="http://localhost:8000"
READY_URL="${BASE_URL}/api/v0/ready"
MAX_WAIT=30

LOCALSTACK_ENDPOINT="${AWS_ENDPOINT_URL:-http://localhost:4566}"
DYNAMODB_ENDPOINT="${DYNAMODB_ENDPOINT:-$LOCALSTACK_ENDPOINT}"
CEDAR_AGENT_ENDPOINT="${CEDAR_AGENT_ENDPOINT:-http://localhost:8181}"
KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:8080}"
POSTGRES_DSN="${POSTGRES_DSN:-postgres://rosa:rosa@localhost:5432/hyperfleet?sslmode=disable}"
CREDENTIALS_FILE="/tmp/e2e-localstack-credentials.env"
KEYCLOAK_CREDENTIALS_FILE="/tmp/e2e-keycloak-credentials.env"

cleanup() {
    echo "Cleaning up..."
    if [[ -f "$PIDFILE" ]] && kill -0 "$(cat "$PIDFILE")" 2>/dev/null; then
        kill "$(cat "$PIDFILE")" 2>/dev/null || true
        wait "$(cat "$PIDFILE")" 2>/dev/null || true
    fi
    rm -f "$PIDFILE" "$CREDENTIALS_FILE" "$KEYCLOAK_CREDENTIALS_FILE"
}
trap cleanup EXIT INT TERM

echo "=== Local Authz E2E Tests ==="
echo "LocalStack:  $LOCALSTACK_ENDPOINT"
echo "DynamoDB:    $DYNAMODB_ENDPOINT"
echo "Cedar Agent: $CEDAR_AGENT_ENDPOINT"
echo "Keycloak:    $KEYCLOAK_URL"
echo "Postgres:    $POSTGRES_DSN"
echo ""

# 1. Initialize DynamoDB tables (must exist before seeding accounts)
echo "Initializing DynamoDB tables..."
DYNAMODB_ENDPOINT="$DYNAMODB_ENDPOINT" bash scripts/e2e-init-dynamodb.sh

# 2. Initialize LocalStack IAM/STS — get supervisor credentials, seed privileged account
echo ""
echo "Initializing LocalStack IAM/STS..."
AWS_ENDPOINT_URL="$LOCALSTACK_ENDPOINT" \
DYNAMODB_ENDPOINT="$DYNAMODB_ENDPOINT" \
CREDENTIALS_FILE="$CREDENTIALS_FILE" \
    bash scripts/e2e-init-localstack.sh

# Source the STS credentials
# shellcheck source=/dev/null
source "$CREDENTIALS_FILE"

# 3. Initialize Keycloak OIDC provider — fetch tokens for test users
echo ""
echo "Initializing Keycloak OIDC provider..."
KEYCLOAK_URL="$KEYCLOAK_URL" \
KEYCLOAK_CREDENTIALS_FILE="$KEYCLOAK_CREDENTIALS_FILE" \
    bash scripts/e2e-init-keycloak.sh

# shellcheck source=/dev/null
source "$KEYCLOAK_CREDENTIALS_FILE"

# 4. Build
echo ""
echo "Building service..."
(cd platform-api && go build -o "../$BINARY" ./cmd)

# 5. Start service with STS credentials from LocalStack
echo "Starting service..."
DYNAMODB_ENDPOINT="$DYNAMODB_ENDPOINT" \
CEDAR_AGENT_ENDPOINT="$CEDAR_AGENT_ENDPOINT" \
AUTHZ_ENABLED=true \
AWS_REGION="${AWS_REGION:-us-east-1}" \
AWS_ACCESS_KEY_ID="$SUPERVISOR_ACCESS_KEY_ID" \
AWS_SECRET_ACCESS_KEY="$SUPERVISOR_SECRET_ACCESS_KEY" \
AWS_SESSION_TOKEN="$SUPERVISOR_SESSION_TOKEN" \
    "$BINARY" serve \
        --postgres-dsn="$POSTGRES_DSN" \
        --health-port=8081 \
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

# 6. Run tests with STS credentials and OIDC tokens
echo ""
echo "Running local authz E2E tests..."
E2E_BASE_URL="$BASE_URL" \
DYNAMODB_ENDPOINT="$DYNAMODB_ENDPOINT" \
KEYCLOAK_ISSUER_URL="$KEYCLOAK_ISSUER_URL" \
SUPERVISOR_OIDC_TOKEN="$SUPERVISOR_OIDC_TOKEN" \
CUSTOMER_ADMIN_OIDC_TOKEN="$CUSTOMER_ADMIN_OIDC_TOKEN" \
CUSTOMER_USER_OIDC_TOKEN="$CUSTOMER_USER_OIDC_TOKEN" \
AWS_REGION="${AWS_REGION:-us-east-1}" \
AWS_ACCESS_KEY_ID="$SUPERVISOR_ACCESS_KEY_ID" \
AWS_SECRET_ACCESS_KEY="$SUPERVISOR_SECRET_ACCESS_KEY" \
AWS_SESSION_TOKEN="$SUPERVISOR_SESSION_TOKEN" \
    ginkgo -v ./test/e2e-authz-local
