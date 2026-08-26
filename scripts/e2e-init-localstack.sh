#!/usr/bin/env bash
set -euo pipefail

# Initialize LocalStack with IAM roles and STS credentials for local e2e testing.
# Creates a supervisor role and a customer role, then verifies STS works.
#
# Exports (via $CREDENTIALS_FILE):
#   SUPERVISOR_ACCESS_KEY_ID, SUPERVISOR_SECRET_ACCESS_KEY
#   so the test runner and platform-api can sign requests with real STS credentials.

ENDPOINT="${AWS_ENDPOINT_URL:-http://localhost:4566}"
REGION="${AWS_REGION:-us-east-1}"
SUPERVISOR_ACCOUNT="${SUPERVISOR_ACCOUNT_ID:-000000000000}"
CUSTOMER_ACCOUNT="${CUSTOMER_ACCOUNT_ID:-111111111111}"

export AWS_ACCESS_KEY_ID="${AWS_ACCESS_KEY_ID:-test}"
export AWS_SECRET_ACCESS_KEY="${AWS_SECRET_ACCESS_KEY:-test}"
export AWS_PAGER=""

CREDENTIALS_FILE="${CREDENTIALS_FILE:-/tmp/e2e-localstack-credentials.env}"

echo "=== Initializing LocalStack IAM/STS ==="
echo "Endpoint: $ENDPOINT"
echo ""

# Wait for LocalStack to be healthy
echo "Waiting for LocalStack..."
for i in {1..30}; do
    if curl -sf "$ENDPOINT/_localstack/health" >/dev/null 2>&1; then
        echo "LocalStack is ready!"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "Timeout waiting for LocalStack"
        exit 1
    fi
    sleep 1
done

# Verify STS is available
echo ""
echo "Verifying STS..."
aws sts get-caller-identity --endpoint-url "$ENDPOINT" --region "$REGION" 2>&1 || true

# Create IAM role for supervisor
echo ""
echo "Creating supervisor IAM role..."
TRUST_POLICY='{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"AWS": "*"},
    "Action": "sts:AssumeRole"
  }]
}'

aws iam create-role \
    --endpoint-url "$ENDPOINT" \
    --region "$REGION" \
    --role-name "SupervisorRole" \
    --assume-role-policy-document "$TRUST_POLICY" \
    --path "/" 2>/dev/null || echo "  (role may already exist)"

aws iam attach-role-policy \
    --endpoint-url "$ENDPOINT" \
    --region "$REGION" \
    --role-name "SupervisorRole" \
    --policy-arn "arn:aws:iam::aws:policy/AdministratorAccess" 2>/dev/null || true

# Create IAM role for customer account
echo "Creating customer IAM role..."
aws iam create-role \
    --endpoint-url "$ENDPOINT" \
    --region "$REGION" \
    --role-name "CustomerAdminRole" \
    --assume-role-policy-document "$TRUST_POLICY" \
    --path "/" 2>/dev/null || echo "  (role may already exist)"

# Assume supervisor role to get STS credentials
echo ""
echo "Assuming supervisor role via STS..."
STS_OUTPUT=$(aws sts assume-role \
    --endpoint-url "$ENDPOINT" \
    --region "$REGION" \
    --role-arn "arn:aws:iam::${SUPERVISOR_ACCOUNT}:role/SupervisorRole" \
    --role-session-name "e2e-supervisor" \
    --output json)

SUPERVISOR_ACCESS_KEY_ID=$(echo "$STS_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Credentials']['AccessKeyId'])")
SUPERVISOR_SECRET_ACCESS_KEY=$(echo "$STS_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Credentials']['SecretAccessKey'])")
SUPERVISOR_SESSION_TOKEN=$(echo "$STS_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Credentials']['SessionToken'])")

echo "Supervisor STS credentials obtained."

# Verify assumed role identity
echo ""
echo "Verifying supervisor identity..."
AWS_ACCESS_KEY_ID="$SUPERVISOR_ACCESS_KEY_ID" \
AWS_SECRET_ACCESS_KEY="$SUPERVISOR_SECRET_ACCESS_KEY" \
AWS_SESSION_TOKEN="$SUPERVISOR_SESSION_TOKEN" \
    aws sts get-caller-identity --endpoint-url "$ENDPOINT" --region "$REGION"

# Seed supervisor as privileged account in DynamoDB accounts table
echo ""
echo "Seeding supervisor as privileged account in DynamoDB..."
DYNAMODB_ENDPOINT="${DYNAMODB_ENDPOINT:-$ENDPOINT}"
if aws dynamodb get-item --endpoint-url "$DYNAMODB_ENDPOINT" --region "$REGION" \
    --table-name "rosa-authz-accounts" \
    --key '{"accountId": {"S": "'"$SUPERVISOR_ACCOUNT"'"}}' \
    --projection-expression "accountId" 2>/dev/null | grep -q "$SUPERVISOR_ACCOUNT"; then
    echo "  Privileged account $SUPERVISOR_ACCOUNT already exists, skipping..."
else
    aws dynamodb put-item --endpoint-url "$DYNAMODB_ENDPOINT" --region "$REGION" \
        --table-name "rosa-authz-accounts" \
        --item '{
            "accountId": {"S": "'"$SUPERVISOR_ACCOUNT"'"},
            "privileged": {"BOOL": true},
            "createdAt": {"S": "'"$(date -u +%Y-%m-%dT%H:%M:%SZ)"'"},
            "createdBy": {"S": "e2e-init-localstack"}
        }'
    echo "  Privileged account $SUPERVISOR_ACCOUNT created."
fi

# Write credentials to file for the run script to source
cat > "$CREDENTIALS_FILE" <<EOF
export SUPERVISOR_ACCESS_KEY_ID="$SUPERVISOR_ACCESS_KEY_ID"
export SUPERVISOR_SECRET_ACCESS_KEY="$SUPERVISOR_SECRET_ACCESS_KEY"
export SUPERVISOR_SESSION_TOKEN="$SUPERVISOR_SESSION_TOKEN"
EOF

echo ""
echo "Credentials written to $CREDENTIALS_FILE"
echo "=== LocalStack IAM/STS initialized ==="
