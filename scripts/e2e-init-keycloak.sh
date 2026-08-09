#!/usr/bin/env bash
set -euo pipefail

# Initialize and verify Keycloak OIDC provider for local e2e authz testing.
# The realm, client, and users are imported from the realm JSON at container start.
# This script waits for Keycloak to be ready, then fetches OIDC tokens
# for each test user and writes them to $CREDENTIALS_FILE.
#
# Exports (via $CREDENTIALS_FILE):
#   KEYCLOAK_ISSUER_URL          — OIDC issuer URL for the rosa-e2e realm
#   SUPERVISOR_OIDC_TOKEN        — Access token for the supervisor user
#   CUSTOMER_ADMIN_OIDC_TOKEN    — Access token for the customer-admin user
#   CUSTOMER_USER_OIDC_TOKEN     — Access token for the customer-user user

KEYCLOAK_URL="${KEYCLOAK_URL:-http://localhost:8080}"
KEYCLOAK_MGMT_URL="${KEYCLOAK_MGMT_URL:-http://localhost:9000}"
REALM="rosa-e2e"
CLIENT_ID="rosa-api"
CLIENT_SECRET="rosa-e2e-secret"

CREDENTIALS_FILE="${KEYCLOAK_CREDENTIALS_FILE:-/tmp/e2e-keycloak-credentials.env}"

echo "=== Initializing Keycloak OIDC Provider ==="
echo "Keycloak URL: $KEYCLOAK_URL"
echo "Realm:        $REALM"
echo ""

# Wait for Keycloak to be healthy (health endpoint is on management port 9000)
echo "Waiting for Keycloak..."
for i in {1..60}; do
    if curl -sf "$KEYCLOAK_MGMT_URL/health/ready" >/dev/null 2>&1; then
        echo "Keycloak is ready!"
        break
    fi
    if [ "$i" -eq 60 ]; then
        echo "Timeout waiting for Keycloak after 60s"
        exit 1
    fi
    sleep 1
done

ISSUER_URL="$KEYCLOAK_URL/realms/$REALM"
TOKEN_URL="$ISSUER_URL/protocol/openid-connect/token"

# Verify OIDC discovery endpoint
echo ""
echo "Verifying OIDC discovery..."
DISCOVERY=$(curl -sf "$ISSUER_URL/.well-known/openid-configuration")
echo "  issuer: $(echo "$DISCOVERY" | python3 -c "import sys,json; print(json.load(sys.stdin)['issuer'])")"
echo "  token_endpoint: $(echo "$DISCOVERY" | python3 -c "import sys,json; print(json.load(sys.stdin)['token_endpoint'])")"
echo "  jwks_uri: $(echo "$DISCOVERY" | python3 -c "import sys,json; print(json.load(sys.stdin)['jwks_uri'])")"

# Fetch OIDC token for a user via Resource Owner Password Credentials grant
fetch_token() {
    local username="$1"
    local password="$2"
    local label="$3"

    echo "  Fetching token for $label ($username)..."
    TOKEN_RESPONSE=$(curl -sf -X POST "$TOKEN_URL" \
        -H "Content-Type: application/x-www-form-urlencoded" \
        -d "grant_type=password" \
        -d "client_id=$CLIENT_ID" \
        -d "client_secret=$CLIENT_SECRET" \
        -d "username=$username" \
        -d "password=$password" \
        -d "scope=openid")

    ACCESS_TOKEN=$(echo "$TOKEN_RESPONSE" | python3 -c "import sys,json; print(json.load(sys.stdin)['access_token'])")
    echo "    token length: ${#ACCESS_TOKEN} chars"
    echo "$ACCESS_TOKEN"
}

echo ""
echo "Fetching OIDC tokens..."

SUPERVISOR_TOKEN=$(fetch_token "supervisor" "supervisor-password" "supervisor")
ADMIN_TOKEN=$(fetch_token "customer-admin" "admin-password" "customer-admin")
USER_TOKEN=$(fetch_token "customer-user" "user-password" "customer-user")

# Write credentials to file
cat > "$CREDENTIALS_FILE" <<EOF
export KEYCLOAK_ISSUER_URL="$ISSUER_URL"
export SUPERVISOR_OIDC_TOKEN="$SUPERVISOR_TOKEN"
export CUSTOMER_ADMIN_OIDC_TOKEN="$ADMIN_TOKEN"
export CUSTOMER_USER_OIDC_TOKEN="$USER_TOKEN"
EOF

echo ""
echo "OIDC credentials written to $CREDENTIALS_FILE"
echo "=== Keycloak OIDC Provider initialized ==="
