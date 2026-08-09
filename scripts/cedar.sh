#!/usr/bin/env bash
# create-cluster-readonly-policy.sh
#
# Creates a Cedar policy granting a principal ARN read-only access to clusters
# (ListClusters + DescribeCluster) with NO access to nodepools.
#
# Usage:
#   ./create-cluster-readonly-policy.sh <API_HOST> <ACCOUNT_ID> [PRINCIPAL_ARN]
#
# Example:
#   ./create-cluster-readonly-policy.sh \
#     https://abc123.execute-api.us-east-1.amazonaws.com \
#     754250776154 \
#     arn:aws:iam::754250776154:role/bff-sigv4-proxy-role

set -euo pipefail

API_HOST="${1:?Usage: $0 <API_HOST> <ACCOUNT_ID> [PRINCIPAL_ARN]}"
ACCOUNT_ID="${2:?Usage: $0 <API_HOST> <ACCOUNT_ID> [PRINCIPAL_ARN]}"
PRINCIPAL_ARN="${3:-arn:aws:iam::${ACCOUNT_ID}:role/bff-sigv4-proxy-role}"
REGION="${AWS_REGION:-us-east-1}"

CEDAR_POLICY='permit(
  ?principal,
  action == ROSA::Action::"ListClusters",
  resource
);

permit(
  ?principal,
  action == ROSA::Action::"DescribeCluster",
  resource
);'

echo "==> Creating Cedar policy for clusters-only read access..."
echo "    API Host:      ${API_HOST}"
echo "    Account ID:    ${ACCOUNT_ID}"
echo "    Principal ARN: ${PRINCIPAL_ARN}"
echo "    Region:        ${REGION}"
echo ""

# Step 1: Create the policy template
echo "--- Step 1: Create policy ---"
POLICY_RESPONSE=$(awscurl --service execute-api \
  --region "${REGION}" \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-Amz-Account-Id: ${ACCOUNT_ID}" \
  -d "$(jq -n \
    --arg name "clusters-read-only" \
    --arg desc "Read-only access to clusters (ListClusters, DescribeCluster). No nodepool access." \
    --arg policy "${CEDAR_POLICY}" \
    '{name: $name, description: $desc, policy: $policy}')" \
  "${API_HOST}/api/v0/authz/policies")

echo "${POLICY_RESPONSE}" | jq .

POLICY_ID=$(echo "${POLICY_RESPONSE}" | jq -r '.policyId')

if [ -z "${POLICY_ID}" ] || [ "${POLICY_ID}" = "null" ]; then
  echo "ERROR: Failed to create policy" >&2
  exit 1
fi

echo ""
echo "--- Step 2: Attach policy to principal ---"
ATTACH_RESPONSE=$(awscurl --service execute-api \
  --region "${REGION}" \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-Amz-Account-Id: ${ACCOUNT_ID}" \
  -d "$(jq -n \
    --arg policyId "${POLICY_ID}" \
    --arg targetType "user" \
    --arg targetId "${PRINCIPAL_ARN}" \
    '{policyId: $policyId, targetType: $targetType, targetId: $targetId}')" \
  "${API_HOST}/api/v0/authz/attachments")

echo "${ATTACH_RESPONSE}" | jq .

echo ""
echo "==> Done. Policy '${POLICY_ID}' attached to ${PRINCIPAL_ARN}"
echo ""
echo "Allowed actions:"
echo "  - ROSA::Action::\"ListClusters\""
echo "  - ROSA::Action::\"DescribeCluster\""
echo ""
echo "Denied (not granted):"
echo "  - All NodePool actions (ListNodePools, DescribeNodePool, CreateNodePool, etc.)"
echo "  - All write operations (CreateCluster, UpdateCluster, DeleteCluster)"
