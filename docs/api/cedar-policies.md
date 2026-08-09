# Cedar Policy Management

This document describes how to provision a non-privileged account with Cedar/AVP authorization and manage fine-grained access policies for clusters, nodepools, and other ROSA resources.

## Overview

ROSA Hyperfleet uses [Amazon Verified Permissions (AVP)](https://docs.aws.amazon.com/verifiedpermissions/latest/userguide/what-is-avp.html) with Cedar policies for fine-grained authorization. Each non-privileged account gets its own AVP policy store with the ROSA Cedar schema. Admins for the account manage Cedar policies that control what actions principals (IAM roles) can perform.

## Account Types


| Type           | Example        | Cedar/AVP            | Admin check                        | Use case                                  |
| -------------- | -------------- | -------------------- | ---------------------------------- | ----------------------------------------- |
| Privileged     | `599476212575` | Bypassed entirely    | Bypassed                           | Platform operations, account provisioning |
| Non-privileged | `754250776154` | Enforced per-request | Enforced via DynamoDB admins table | Customer workloads                        |




## Authorization Flow

```
Request
  -> SigV4 Auth (API Gateway)
  -> Identity Middleware (extracts account ID, caller ARN)
  -> CheckPrivileged (sets privileged flag)
  -> RequireProvisioned (verifies account has a policy store)
  -> RequireAdmin (for authz management endpoints only)
  -> Cedar/AVP IsAuthorized (for resource endpoints)
  -> Handler
```

- **Privileged accounts** bypass `RequireProvisioned`, `RequireAdmin`, and Cedar evaluation.
- **Non-privileged admins** bypass Cedar evaluation but must pass `RequireAdmin` to access authz management endpoints.
- **Non-privileged non-admins** are evaluated against Cedar policies in AVP for every resource operation.



## End-to-End Setup Sequence



### Step 1: Provision the account (privileged caller)

A privileged account creates the non-privileged account. The `adminArn` field is **required** for non-privileged accounts to bootstrap the first admin and avoid a deadlock (no admin = can't add admins).

```bash
awscurl --service execute-api --region us-east-1 \
  -X POST \
  -H "Content-Type: application/json" \
  -d '{
    "accountId": "754250776154",
    "privileged": false,
    "adminArn": "arn:aws:iam::754250776154:role/bff-sigv4-proxy-role"
  }' \
  "${API_URL}/api/v0/accounts"
```

This creates:

- An account record in DynamoDB
- An AVP policy store for the account
- Uploads the ROSA Cedar schema to the policy store
- Adds `adminArn` as the initial admin in the admins table

> **Why** `adminArn` **instead of auto-capturing the caller?** The caller is from the privileged account (599476212575), not the account being created (754250776154). There is no way to infer the correct admin ARN from the cross-account request context.

```bash

# as caller supervisor
# delete the customer account, 754250776154
# redo configuration
awscurl --service execute-api --region us-east-1 \
    -X DELETE \
    "${API_URL}/api/v0/accounts/754250776154"

# as caller supervisor
# add 1st admin for 754250776154
# note the sts -> iam
awscurl --service execute-api --region us-east-1 \
    -X POST \
    -H "Content-Type: application/json" \
    -d '{
      "accountId": "754250776154",
      "privileged": false,
      "adminArn": "arn:aws:iam::754250776154:role/OrganizationAccountAccessRole"
    }' \
    "${API_URL}/api/v0/accounts"
{"kind":"Account","accountId":"754250776154","policyStoreId":"8DFfo9GpNzftPysCb6k92S","privileged":false,"createdAt":"2026-08-09T00:46:19Z","createdBy":"arn:aws:sts::599476212575:assumed-role/OrganizationAccountAccessRole/rrp-dev-53375"}



# as caller 754250776154, customer, add a new policy
✗ awscurl --service execute-api --region us-east-1 \
    -X POST \
    -H "Content-Type: application/json" \
    -d '{
      "name": "clusters-read-only",
      "description": "Read-only access to clusters",
      "policy": "permit(principal == ?principal, action in [ROSA::Action::\"ListClusters\", ROSA::Action::\"DescribeCluster\"], resource);"
    }' \
    "${API_URL}/api/v0/authz/policies"
{"kind":"Policy","policyId":"KkfzMk9Ti8vXXoTPHDZv2K","name":"clusters-read-only","description":"Read-only access to clusters","createdAt":"2026-08-09T00:47:07Z"}


# as caller 154, customer, add a new admin to 154
awscurl --service execute-api --region us-east-1 \
    -X POST \
    -H "Content-Type: application/json" \
    -d '{"principalArn": "arn:aws:iam::754250776154:role/some-new-admin"}' \
    "${API_URL}/api/v0/authz/admins"
{"kind":"Admin","principalArn":"arn:aws:iam::754250776154:role/some-new-admin"}
```



### Step 2: Create a CedaÏter policy (admin caller)

The bootstrapped admin creates Cedar policy templates. Each template defines what actions are allowed on which resource types.

```bash
awscurl --service execute-api --region us-east-1 \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-Amz-Account-Id: 754250776154" \
  -d '{
    "name": "clusters-read-only",
    "description": "Read-only access to clusters",
    "policy": "permit(principal == ?principal, action in [ROSA::Action::\"ListClusters\", ROSA::Action::\"DescribeCluster\"], resource);"
  }' \
  "${API_URL}/api/v0/authz/policies"
```



### Step 3: Attach the policy to a principal

Bind the policy template to a concrete IAM role ARN. This creates a template-linked policy in AVP.

```bash
awscurl --service execute-api --region us-east-1 \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-Amz-Account-Id: 754250776154" \
  -d '{
    "policyId": "<POLICY_ID from step 2>",
    "targetType": "user",
    "targetId": "arn:aws:iam::754250776154:role/some-app-role"
  }' \
  "${API_URL}/api/v0/authz/attachments"
```



### Step 4: (Optional) Add more admins

```bash
awscurl --service execute-api --region us-east-1 \
  -X POST \
  -H "Content-Type: application/json" \
  -H "X-Amz-Account-Id: 754250776154" \
  -d '{"principalArn": "arn:aws:iam::754250776154:role/another-admin-role"}' \
  "${API_URL}/api/v0/authz/admins"
```



### Step 5: (Optional) Remove admin access

If the initial admin role should be governed solely by Cedar policies rather than having full admin access:

```bash
# as caller 754250776154, delete the admin bff-sigv4-proxy-role
awscurl --service execute-api --region us-east-1 \
  -X DELETE \
  -H "X-Amz-Account-Id: 754250776154" \
  "${API_URL}/api/v0/authz/admins/arn:aws:iam::754250776154:role/bff-sigv4-proxy-role"
```



## Flow Summary


| Step                          | Who calls                 | Endpoint                            | What happens                                                                              |
| ----------------------------- | ------------------------- | ----------------------------------- | ----------------------------------------------------------------------------------------- |
| 1. Provision account          | Privileged (599476212575) | `POST /api/v0/accounts`             | Creates account, AVP policy store, uploads Cedar schema, adds `adminArn` as initial admin |
| 2. Create policy              | Admin (754250776154)      | `POST /api/v0/authz/policies`       | Creates Cedar policy template in AVP                                                      |
| 3. Attach policy              | Admin (754250776154)      | `POST /api/v0/authz/attachments`    | Binds policy to a principal ARN via template-linked policy                                |
| 4. (Optional) Add more admins | Admin (754250776154)      | `POST /api/v0/authz/admins`         | Grants another role admin access to manage policies                                       |
| 5. (Optional) Remove admin    | Admin (754250776154)      | `DELETE /api/v0/authz/admins/{arn}` | Removes admin; role is then governed only by Cedar policies                               |




## Key Concepts


| Concept                      | Details                                                                                                                                                                 |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `adminArn`                   | Required for non-privileged accounts; bootstraps the first admin so the account is not deadlocked                                                                       |
| Why not auto-capture caller? | Caller is cross-account (privileged 599476212575), not the account owner (754250776154)                                                                                 |
| Admin vs Cedar               | Admins bypass Cedar and have full authz CRUD; Cedar policies enforce fine-grained access for non-admins                                                                 |
| ARN normalization            | STS assumed-role ARNs (`arn:aws:sts::ACCT:assumed-role/R/session`) are normalized to IAM ARNs (`arn:aws:iam::ACCT:role/R`) for both admin matching and Cedar evaluation |
| Policy template syntax       | Must use `principal == ?principal` (not bare `?principal`); one `permit`/`forbid` statement per template                                                                |
| `X-Amz-Account-Id`           | Injected by API Gateway from SigV4 credentials; identifies the caller's account, not a target account                                                                   |




## Cedar Policy Syntax



### Template slots

Cedar policy templates use `?principal` and `?resource` as placeholders. They must appear in a scope constraint:

```cedar
permit(
  principal == ?principal,
  action == ROSA::Action::"ListClusters",
  resource
);
```



### Multiple actions

Use `action in [...]` to grant multiple actions in a single template:

```cedar
permit(
  principal == ?principal,
  action in [ROSA::Action::"ListClusters", ROSA::Action::"DescribeCluster"],
  resource
);
```



### One statement per template

AVP requires exactly **one** `permit` or `forbid` statement per policy template. To grant unrelated action sets, create separate policy templates.

## Available Cedar Actions



### Cluster actions

- `ROSA::Action::"CreateCluster"`
- `ROSA::Action::"DeleteCluster"`
- `ROSA::Action::"DescribeCluster"`
- `ROSA::Action::"ListClusters"`
- `ROSA::Action::"UpdateCluster"`
- `ROSA::Action::"UpdateClusterConfig"`
- `ROSA::Action::"UpdateClusterVersion"`



### NodePool actions

- `ROSA::Action::"CreateNodePool"`
- `ROSA::Action::"DeleteNodePool"`
- `ROSA::Action::"DescribeNodePool"`
- `ROSA::Action::"ListNodePools"`
- `ROSA::Action::"UpdateNodePool"`
- `ROSA::Action::"ScaleNodePool"`



### AccessEntry actions

- `ROSA::Action::"CreateAccessEntry"`
- `ROSA::Action::"DeleteAccessEntry"`
- `ROSA::Action::"DescribeAccessEntry"`
- `ROSA::Action::"ListAccessEntries"`
- `ROSA::Action::"UpdateAccessEntry"`



### Tagging actions

- `ROSA::Action::"TagResource"`
- `ROSA::Action::"UntagResource"`
- `ROSA::Action::"ListTagsForResource"`



### Other actions

- `ROSA::Action::"ListAccessPolicies"`



## Troubleshooting


| Error                                                          | Cause                                                           | Fix                                                                                  |
| -------------------------------------------------------------- | --------------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| `ValidationException: Invalid input` on `PutSchema`            | Cedar schema contains `additionalAttributes` field              | Remove `additionalAttributes` from all entity types in `rosa.cedarschema.json`       |
| `ValidationException: Invalid input` on `CreatePolicyTemplate` | Invalid Cedar syntax (bare `?principal`, multiple statements)   | Use `principal == ?principal` and one statement per template                         |
| `AccessDeniedException` on `CreatePolicyTemplate`              | Platform API pod role missing IAM permissions                   | Add `verifiedpermissions:CreatePolicyTemplate` and related actions to the IAM policy |
| `not-admin` error on authz endpoints                           | Caller ARN not in admins table for the account                  | Add the caller as admin, or check ARN normalization (STS vs IAM form)                |
| `account-not-provisioned`                                      | Account not created via `POST /api/v0/accounts`                 | Provision the account first with a privileged caller                                 |
| Cedar policy allows but request denied                         | STS assumed-role ARN doesn't match IAM ARN in policy attachment | Ensure ARN normalization is enabled in `buildAVPRequest`                             |


