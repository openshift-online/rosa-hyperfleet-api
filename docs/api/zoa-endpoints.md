# ZOA API — Endpoint Reference

**Last Updated**: 2026-06-14

**Base Path**: `/api/v0/trusted-actions`

**Authentication**: AWS SigV4 (via API Gateway). Caller identity is extracted from the SigV4 signature and recorded with every operation.

## Overview

Zero Operator Access (ZOA) provides a controlled API for executing **Trusted Actions** (TAs) against management clusters and AWS resources. Operators submit actions through the API; each execution is tracked in DynamoDB, dispatched via Maestro, and produces artifacts (output and logs) in S3.

**Typical workflow:**

1. Discover available actions — `GET /` (catalog) or `GET /{action}` (describe)
2. Execute an action — `POST /{action}/run` (returns `202 Accepted` with execution ID)
3. Poll for completion — `GET /runs/{id}` (metadata only by default)
4. Retrieve results — `GET /runs/{id}?include=output,logs` (opt-in content)
5. Audit and reporting — `GET /runs` (filter executions) or `GET /audit` (API call log)

Every mutating and read API call (except catalog and describe) is recorded in the audit log.

## Key Types

| Type              | Values                                                   | Description                                       |
| ----------------- | -------------------------------------------------------- | ------------------------------------------------- |
| `ExecutionStatus` | `pending`, `running`, `succeeded`, `failed`, `timed_out` | Lifecycle state of a TA execution                 |
| `OutputStatus`    | `pending`, `uploaded`, `failed`                          | State of S3 artifact upload                       |
| `ApprovalState`   | `not_required`, `pending`, `approved`, `rejected`        | Approval lifecycle for an execution               |
| `Scope`           | `kube-api`, `aws-api`                                    | Where the TA executes (Kubernetes API or AWS API) |
| `Type`            | `read`, `write`                                          | Whether the TA is read-only or mutating           |

## Endpoints Overview

| Method | Path            | Handler     | Description                           |
| ------ | --------------- | ----------- | ------------------------------------- |
| `POST` | `/{action}/run` | `Create`    | Execute a Trusted Action              |
| `GET`  | `/runs/{id}`    | `Get`       | Retrieve execution details            |
| `GET`  | `/runs`         | `List`      | List executions (filtered, paginated) |
| `GET`  | `/audit`        | `AuditList` | List API call audit log entries       |
| `GET`  | `/`             | `Catalog`   | List all available Trusted Actions    |
| `GET`  | `/{action}`     | `Describe`  | Describe a specific Trusted Action    |

---

## POST /{action}/run

Execute a Trusted Action on a target cluster.

### Path Parameters

| Parameter | Type   | Required | Description                                   |
| --------- | ------ | -------- | --------------------------------------------- |
| `action`  | string | Yes      | TA name (e.g., `get_pods`, `rollout_restart`) |

### Request Body

```json
{
  "target_cluster": "mc-useast1-1",
  "jira": "ROSAENG-1234",
  "params": {
    "namespace": "maestro",
    "name": "maestro-abc-123",
    "label_selector": "app=maestro",
    "verbose": "false"
  },
  "force": false,
  "dry_run": false
}
```

| Field            | Type    | Required | Description                                                                     |
| ---------------- | ------- | -------- | ------------------------------------------------------------------------------- |
| `target_cluster` | string  | Yes      | Target management cluster identifier                                            |
| `jira`           | string  | Yes      | Jira ticket reference; must match `PROJECT-NUMBER` format (e.g. `ROSAENG-1234`) |
| `params`         | object  | No       | Key-value pairs of TA parameters (all values are strings)                       |
| `force`          | boolean | No       | Bypass write cooldown for write TAs (default: `false`)                          |
| `dry_run`        | boolean | No       | Execute the TA's `dry_run_action` instead (preview; default: `false`)           |

### Parameter Validation

Parameters are validated against the TA template definition:

1. **Required params**: If a TA declares a parameter as `required: true`, it must be present and non-empty
2. **Namespace scoping**: If a TA declares both `namespace` (default: `""`) and `all_namespaces` (default: `"false"`) parameters, the API enforces that either `namespace` is provided OR `all_namespaces=true` is set
3. **Single-resource fetch (`name`)**: All `get_*` read TAs accept an optional `name` parameter. When provided, the TA fetches a single resource instead of listing all. Cluster-scoped resources (`get_nodes`, `get_namespaces`, `get_pvs`) also support `name`. Cannot be combined with `all_namespaces=true` (enforced in TA script). Response is normalized to list format (single item in array) for consistency
4. **Write TA resource names**: Write TAs use a standardized `name` parameter (e.g. `rollout_restart`, `delete_pod`)
5. **Unknown parameters**: Parameter names must match the TA template definition exactly

**Unknown parameter error messages:**

| Condition                      | Message                                                                        |
| ------------------------------ | ------------------------------------------------------------------------------ |
| TA accepts no parameters       | `unknown parameter 'foo'; this action accepts no parameters`                   |
| TA has defined parameters      | `unknown parameter 'foo'; allowed parameters: namespace, name, ...`            |
| Name matches a top-level field | Same as above, plus hint: `('jira' is a top-level request field, not a param)` |

Top-level request fields (`target_cluster`, `jira`, `force`, `dry_run`) must not be placed inside `params`.

### Responses

#### 202 Accepted

Execution created and dispatched to Maestro.

```json
{
  "id": "fa65418c-f4eb-4f5c-8314-baaeb695ba7d",
  "account_id": "123456789012",
  "caller_arn": "arn:aws:sts::123456789012:assumed-role/DevAccess/slopezma",
  "operator": "slopezma",
  "action": "get_pods",
  "target_cluster": "mc-useast1-1",
  "scope": "kube-api",
  "type": "read",
  "jira": "ROSAENG-1234",
  "approval_state": "not_required",
  "status": "pending",
  "output_status": "pending",
  "revision": "a1b2c3d",
  "output_path": "s3://bucket-name/fa65418c-.../output.json",
  "manifest_work_name": "zoa-fa65418c-...",
  "created_at": "2026-06-10T12:00:00Z",
  "updated_at": "2026-06-10T12:00:00Z"
}
```

**Approval state values:**

| Value          | Meaning                                                          |
| -------------- | ---------------------------------------------------------------- |
| `not_required` | TA policy is `authorization.approval: none` — no approval needed |
| `pending`      | Approval required but not yet obtained (execution blocked)       |
| `approved`     | Required approvals received — execution authorized               |
| `rejected`     | Approval explicitly denied — execution will not proceed          |

#### 400 Bad Request

```json
{
  "kind": "Error",
  "code": "invalid-params",
  "reason": "specify namespace or set all_namespaces=true"
}
```

```json
{
  "kind": "Error",
  "code": "invalid-params",
  "reason": "unknown parameter 'jira'; allowed parameters: namespace, name ('jira' is a top-level request field, not a param)"
}
```

```json
{
  "kind": "Error",
  "code": "invalid-jira",
  "reason": "jira does not have correct format; expected PROJECT-NUMBER (e.g. ROSAENG-1234)"
}
```

| Error Code               | Condition                                                                    |
| ------------------------ | ---------------------------------------------------------------------------- |
| `invalid-request`        | Request body is not valid JSON                                               |
| `missing-target-cluster` | `target_cluster` field is empty                                              |
| `missing-jira`           | `jira` field is empty                                                        |
| `invalid-params`         | Required parameter missing, unknown parameter, or namespace scoping violated |
| `invalid-jira`           | `jira` does not match `PROJECT-NUMBER` format                                |

#### 404 Not Found

```json
{
  "kind": "Error",
  "code": "unknown-action",
  "reason": "Trusted action not found: get_secretz"
}
```

#### 500 Internal Server Error

```json
{
  "kind": "Error",
  "code": "store-error",
  "reason": "Failed to create execution"
}
```

#### 502 Bad Gateway

```json
{
  "kind": "Error",
  "code": "maestro-error",
  "reason": "Failed to dispatch trusted action"
}
```

Indicates Maestro gRPC call failed. The execution record exists in DynamoDB with `status: failed`.

#### 429 Too Many Requests

Write cooldown active or max concurrent limit reached.

```json
{
  "kind": "Error",
  "code": "write-cooldown",
  "reason": "action 'rollout_restart' was executed on 'mc-useast1-1' recently (cooldown: 300s); use force=true to bypass"
}
```

```json
{
  "kind": "Error",
  "code": "max-concurrent",
  "reason": "target 'mc-useast1-1' has 10 active executions (max: 10); wait for some to complete"
}
```

| Error Code       | Condition                                                                                             |
| ---------------- | ----------------------------------------------------------------------------------------------------- |
| `write-cooldown` | Write TA executed on same target within cooldown window; use `force: true` to bypass                  |
| `max-concurrent` | Target cluster has reached max concurrent executions (running + pending); use `force: true` to bypass |

---

## GET /runs/{id}

Retrieve an execution's metadata and optionally its output/logs.

### Path Parameters

| Parameter | Type          | Required | Description  |
| --------- | ------------- | -------- | ------------ |
| `id`      | string (UUID) | Yes      | Execution ID |

### Query Parameters

| Parameter | Values                                           | Default | Description                                            |
| --------- | ------------------------------------------------ | ------- | ------------------------------------------------------ |
| `include` | `output`, `logs`, or comma-separated combination | (none)  | Opt-in: which S3 content to include alongside metadata |

**Content selection behavior:**

| `include` Value | Metadata | Output | Logs |
| --------------- | -------- | ------ | ---- |
| (empty/omitted) | Yes      | No     | No   |
| `output`        | Yes      | Yes    | No   |
| `logs`          | Yes      | No     | Yes  |
| `output,logs`   | Yes      | Yes    | Yes  |

S3 content (output/logs) is only fetched for terminal executions (`succeeded`, `failed`, `timed_out`). For `pending` or `running` executions, only metadata is returned regardless of `include`.

### Responses

#### 200 OK

```json
{
  "id": "fa65418c-f4eb-4f5c-8314-baaeb695ba7d",
  "account_id": "123456789012",
  "caller_arn": "arn:aws:sts::123456789012:assumed-role/DevAccess/slopezma",
  "operator": "slopezma",
  "action": "get_pods",
  "target_cluster": "mc-useast1-1",
  "scope": "kube-api",
  "type": "read",
  "jira": "ROSAENG-1234",
  "approval_state": "not_required",
  "status": "succeeded",
  "output_status": "uploaded",
  "revision": "a1b2c3d",
  "params": { "namespace": "maestro", "name": "maestro-abc-123" },
  "created_at": "2026-06-10T12:00:00Z",
  "updated_at": "2026-06-10T12:00:29Z",
  "completed_at": "2026-06-10T12:00:29Z",
  "runner_seconds": 5,
  "upload_seconds": 12,
  "duration_seconds": 29,

  "output": [
    {
      "name": "maestro-abc-123",
      "namespace": "maestro",
      "status": "Running",
      "restarts": 0,
      "age": "3d"
    }
  ],

  "logs": "[11:00:01] runner starting\n[zoa] execution_id=fa65418c-... action=get_pods target=mc-useast1-1\n...\n--- upload ---\n[11:00:06] upload starting\n[11:00:09] runner waited (3s)\n[11:00:10] configmap read (1s)\n[11:00:10] decoded (0s), uploading to s3\n"
}
```

**Notes:**

- `output` is the parsed JSON from `/artifacts/output.json` (structure depends on the TA)
- `logs` is the raw text content of `execution.log` (includes both runner and upload timeline)
- `params` records the parameters passed at submission time (audit trail)
- `jira` records the associated Jira ticket
- `updated_at` reflects the last status transition (create, status change, or completion)
- `output` and `logs` are only fetched when `output_status` is `"uploaded"`
- If `output_status` is `"pending"` or `"failed"`, these fields are omitted
- If S3 fetch fails for output/logs, the field is omitted (not an error response)

#### 404 Not Found

```json
{
  "kind": "Error",
  "code": "not-found",
  "reason": "Execution not found"
}
```

---

## GET /runs

List executions for the authenticated account, with filtering and pagination.

### Query Parameters

| Parameter        | Type            | Default | Description                                                                 |
| ---------------- | --------------- | ------- | --------------------------------------------------------------------------- |
| `limit`          | integer (1-100) | 20      | Max results per page                                                        |
| `status`         | string          | —       | Filter: `pending`, `running`, `succeeded`, `failed`, `timed_out`            |
| `action`         | string          | —       | Filter by TA name (exact match)                                             |
| `target`         | string          | —       | Filter by target cluster (exact match)                                      |
| `operator`       | string          | —       | Filter by operator name (exact match)                                       |
| `scope`          | string          | —       | Filter by scope: `kube-api`, `aws-api`                                      |
| `type`           | string          | —       | Filter by type: `read`, `write`                                             |
| `output_status`  | string          | —       | Filter by output status: `pending`, `uploaded`, `failed`                    |
| `approval_state` | string          | —       | Filter by approval state: `not_required`, `pending`, `approved`, `rejected` |
| `dry_run`        | string          | —       | Filter by dry-run flag: `true` or `false`                                   |
| `force`          | string          | —       | Filter by force flag: `true` or `false`                                     |
| `since`          | string          | —       | Time filter (see below)                                                     |

**`since` format:**

- Duration shorthand: `30s`, `5m`, `1h`, `24h`, `7d`
- RFC3339 timestamp: `2026-06-10T00:00:00Z`

Duration values are converted to an absolute RFC3339 timestamp at query time.

### Query Execution

Filters are applied at DynamoDB level:

- `since` is applied as a `KeyConditionExpression` on the `createdAt` sort key (efficient — no scan)
- All other filters are applied as `FilterExpression` (post-read filter)
- Results are scoped to the caller's `account_id` (partition key on the GSI)
- Sorted by `createdAt` descending (most recent first)

### Responses

#### 200 OK

```json
{
  "items": [
    {
      "id": "fa65418c-...",
      "action": "get_pods",
      "operator": "slopezma",
      "target_cluster": "mc-useast1-1",
      "scope": "kube-api",
      "type": "read",
      "jira": "ROSAENG-1234",
      "approval_state": "not_required",
      "status": "succeeded",
      "output_status": "uploaded",
      "params": { "namespace": "maestro" },
      "created_at": "2026-06-10T12:00:00Z",
      "updated_at": "2026-06-10T12:00:29Z",
      "completed_at": "2026-06-10T12:00:29Z",
      "runner_seconds": 5,
      "upload_seconds": 12,
      "duration_seconds": 29,
      "dry_run": false,
      "force": false
    }
  ],
  "total": 1,
  "page": 1,
  "limit": 20,
  "has_more": false
}
```

**Notes:**

- List responses do NOT include `output` or `logs` (metadata only)
- Use `GET /runs/{id}` with `include` parameter for full content

---

## GET /audit

List API call audit log entries for the authenticated account.

Every API call is recorded in the DynamoDB audit table. Audited endpoints: `POST /{action}/run`, `GET /runs/{id}`, `GET /runs`, `GET /audit`. Catalog (`GET /`) and describe (`GET /{action}`) are not audited — they expose public metadata only.

All audit entries share a uniform schema. Fields not applicable to a given call type are stored as empty strings.

Audit logging requires `ZOA_AUDIT_TABLE_NAME` to be configured. If not enabled, returns 404 with `audit-disabled`.

### Query Parameters

| Parameter        | Type            | Default | Description                                                                 |
| ---------------- | --------------- | ------- | --------------------------------------------------------------------------- |
| `limit`          | integer (1-200) | 50      | Max results per page                                                        |
| `action`         | string          | —       | Filter by TA name                                                           |
| `target`         | string          | —       | Filter by target cluster                                                    |
| `operator`       | string          | —       | Filter by operator name                                                     |
| `method`         | string          | —       | Filter by HTTP method: `GET`, `POST`                                        |
| `approval_state` | string          | —       | Filter by approval state: `not_required`, `pending`, `approved`, `rejected` |
| `since`          | string          | —       | Time filter (duration shorthand or RFC3339)                                 |

### Responses

#### 200 OK

```json
{
  "kind": "AuditList",
  "items": [
    {
      "id": "e2f91a3b-...",
      "account_id": "123456789012",
      "caller_arn": "arn:aws:sts::123456789012:assumed-role/DevAccess/slopezma",
      "operator": "slopezma",
      "method": "POST",
      "path": "/api/v0/trusted-actions/get_pods/run",
      "action": "get_pods",
      "target_cluster": "mc-useast1-1",
      "execution_id": "1a2cc9ec-fac0-43eb-ba2b-b3f1124f6aea",
      "jira": "ROSAENG-1234",
      "approval_state": "not_required",
      "status_code": 202,
      "timestamp": "2026-06-12T10:00:00.123456789Z"
    },
    {
      "id": "f3a82b4c-...",
      "account_id": "123456789012",
      "caller_arn": "arn:aws:sts::123456789012:assumed-role/DevAccess/slopezma",
      "operator": "slopezma",
      "method": "GET",
      "path": "/api/v0/trusted-actions/runs/1a2cc9ec-fac0-43eb-ba2b-b3f1124f6aea?include=output",
      "action": "",
      "target_cluster": "",
      "execution_id": "1a2cc9ec-fac0-43eb-ba2b-b3f1124f6aea",
      "jira": "",
      "status_code": 200,
      "timestamp": "2026-06-12T10:00:30.456789012Z"
    },
    {
      "id": "a7c42d5e-...",
      "account_id": "123456789012",
      "caller_arn": "arn:aws:sts::123456789012:assumed-role/DevAccess/slopezma",
      "operator": "slopezma",
      "method": "GET",
      "path": "/api/v0/trusted-actions/runs?limit=20&since=1h",
      "action": "",
      "target_cluster": "",
      "execution_id": "",
      "jira": "",
      "status_code": 200,
      "timestamp": "2026-06-12T10:01:00.789012345Z"
    }
  ],
  "total": 3
}
```

**Notes:**

- Audit entries are scoped to the caller's account (same as runs)
- Sorted by `timestamp` descending (most recent first)
- Timestamps use nanosecond precision (`2006-01-02T15:04:05.000000000Z`) as the DynamoDB sort key, ensuring unique keys per account under rapid successive calls
- The `since` filter uses a key condition on `timestamp` for efficient range queries
- All entries share the same schema; fields not applicable to a call type are empty strings
- `execution_id` is populated for `POST /run` (created ID) and `GET /runs/{id}` (accessed ID)
- `jira`, `action`, `target_cluster`, and `approval_state` are populated for `POST /run` calls
- `path` includes the full request URI (path + query parameters) for GET requests
- Rejected POST requests (400/429) are also audited with available context
- TTL: entries auto-expire after a configurable retention period (default 365 days, set via `dynamodb_ttl_days` in `zoa-job-config` ConfigMap)

#### 404 Not Found

```json
{
  "kind": "Error",
  "code": "audit-disabled",
  "reason": "Audit logging is not enabled"
}
```

Returned when `ZOA_AUDIT_TABLE_NAME` is not configured in the deployment.

---

## GET /

List all available Trusted Actions (catalog).

### Responses

#### 200 OK

```json
{
  "items": [
    {
      "name": "get_pods",
      "scope": "kube-api",
      "type": "read",
      "description": "List pods with status, restarts, age, and node placement"
    },
    {
      "name": "get_nodes",
      "scope": "kube-api",
      "type": "read",
      "description": "List all nodes in the target cluster"
    },
    {
      "name": "rollout_restart",
      "scope": "kube-api",
      "type": "write",
      "description": "Perform a rolling restart of a deployment"
    },
    {
      "name": "list_eks_clusters",
      "scope": "aws-api",
      "type": "read",
      "description": "List all EKS clusters in the target account region"
    },
    {
      "name": "list_vpc_endpoints",
      "scope": "aws-api",
      "type": "read",
      "description": "List all VPC endpoints in the target account region (compact summary)"
    }
  ],
  "total": 22
}
```

**Notes:**

- Parameters are NOT included in catalog responses (use Describe for full details)
- Items are sorted alphabetically by name

---

## GET /{action}

Describe a specific Trusted Action — includes full parameter definitions.

### Path Parameters

| Parameter | Type   | Required | Description |
| --------- | ------ | -------- | ----------- |
| `action`  | string | Yes      | TA name     |

### Responses

#### 200 OK

```json
{
  "name": "get_pods",
  "scope": "kube-api",
  "type": "read",
  "description": "List pods with status, restarts, age, and node placement",
  "authorization": {
    "approval": "none"
  },
  "write_cooldown_seconds": 0,
  "dry_run_action": "",
  "params": [
    {
      "name": "namespace",
      "required": false,
      "default": "",
      "description": "Target namespace (required unless all_namespaces=true)"
    },
    {
      "name": "all_namespaces",
      "required": false,
      "default": "false",
      "description": "List pods across all namespaces"
    },
    {
      "name": "name",
      "required": false,
      "default": "",
      "description": "Specific pod name (omit to list all; cannot combine with all_namespaces)"
    },
    {
      "name": "label_selector",
      "required": false,
      "default": "",
      "description": "Label selector to filter pods (e.g. app=maestro)"
    },
    {
      "name": "verbose",
      "required": false,
      "default": "false",
      "description": "Return full JSON output instead of compact summary"
    }
  ]
}
```

**Template metadata fields:**

| Field                    | Type    | Default                   | Description                                                                                                                                                                                     |
| ------------------------ | ------- | ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `authorization`          | object  | `{"approval": "none"}`    | Authorization policy for this TA. `approval` is `"none"` (no approval needed) or a structured object defining approval requirements (min_count, ttl, approver rules). See authorization design. |
| `write_cooldown_seconds` | integer | `0` (uses global default) | Per-TA write cooldown override in seconds                                                                                                                                                       |
| `dry_run_action`         | string  | `""`                      | Name of a read TA to execute when `dry_run: true` is set in the request                                                                                                                         |

#### 404 Not Found

```json
{
  "kind": "Error",
  "code": "unknown-action",
  "reason": "Trusted action not found: get_secretz"
}
```

---

## Error Response Format

All errors follow a consistent structure:

```json
{
  "kind": "Error",
  "code": "<error-code>",
  "reason": "<human-readable message>"
}
```

### Error Codes Reference

| HTTP Status | Code                     | When                                                                      |
| ----------- | ------------------------ | ------------------------------------------------------------------------- |
| 400         | `invalid-request`        | Request body is not valid JSON                                            |
| 400         | `missing-target-cluster` | `target_cluster` not provided                                             |
| 400         | `missing-jira`           | `jira` not provided                                                       |
| 400         | `invalid-jira`           | `jira` format invalid (expected `PROJECT-NUMBER`, e.g. `ROSAENG-1234`)    |
| 400         | `invalid-params`         | Parameter validation failed                                               |
| 404         | `unknown-action`         | TA name not found in registry                                             |
| 404         | `not-found`              | Execution ID not found in DynamoDB                                        |
| 429         | `write-cooldown`         | Write TA cooldown active on target (use `force: true` to bypass)          |
| 429         | `max-concurrent`         | Target cluster at max concurrent executions (use `force: true` to bypass) |
| 500         | `store-error`            | DynamoDB operation failed                                                 |
| 500         | `render-error`           | ManifestWork generation failed                                            |
| 500         | `dry-run-error`          | `dry_run_action` references unknown TA                                    |
| 502         | `maestro-error`          | Maestro gRPC call failed                                                  |
| 404         | `audit-disabled`         | Audit logging not configured (GET /audit only)                            |

---

## Execution Lifecycle

### Status Transitions

```
pending → running → succeeded
                  → failed
                  → timed_out
```

### Output Status Transitions

```
pending → uploaded    (uploader Job succeeded)
        → failed     (uploader Job failed or timed out)
```

- `pending`: DynamoDB record created, ManifestWork dispatched to Maestro
- `running`: ManifestWork applied on MC, runner Job pod started
- `succeeded`: Runner Job completed with exit code 0
- `failed`: Runner Job completed with non-zero exit code
- `timed_out`: Execution exceeded timeout, cleaned up by reconciler

**Output status:**

- `pending`: Uploader Job not yet completed
- `uploaded`: Uploader Job succeeded, artifacts available in S3
- `failed`: Uploader Job failed (logs still available via execution metadata)

### Timing Fields

| Field              | Set When                   | Meaning                                                                                    |
| ------------------ | -------------------------- | ------------------------------------------------------------------------------------------ |
| `created_at`       | On POST (submission)       | When the execution was requested                                                           |
| `updated_at`       | On every status transition | Last time the execution record changed (create, pending→running, completion)               |
| `completed_at`     | On overall completion      | When the reconciler detected both Jobs done                                                |
| `runner_seconds`   | On overall completion      | Runner Job wall-clock time (from K8s `.status.startTime` to `.status.completionTime`)      |
| `upload_seconds`   | On overall completion      | Time from runner completion to uploader completion (wait + configmap + decode + S3 upload) |
| `duration_seconds` | On overall completion      | Total wall-clock: `completed_at - created_at` (includes Maestro dispatch overhead)         |

**Derived metric** (not stored): `dispatch_overhead = duration_seconds - runner_seconds - upload_seconds`

---

## Rate Limiting and Safety Controls

### Write Cooldown

Write TAs enforce a cooldown period between executions of the same action on the same target cluster:

- **Global default**: 300 seconds (configured via `write_cooldown_seconds` in `zoa-job-config` ConfigMap)
- **Per-TA override**: `write_cooldown_seconds` in the TA template YAML (e.g. `delete_pod` uses 60s)
- **Bypass**: Set `force: true` in the request body
- **Scope**: Checks recent successful/pending/running executions of the same action on the same target within the cooldown window
- **Dry-run**: Cooldown is not enforced when `dry_run: true`

Returns HTTP 429 with code `write-cooldown` when active.

### Max Concurrent Per Target

Limits the number of in-flight executions per target cluster:

- **Global default**: 10 (configured via `max_concurrent_per_target` in `zoa-job-config` ConfigMap)
- **Counts**: Running + pending executions for the target cluster (scoped to caller's account)
- **Excludes**: Dry-run executions (`dry_run: true` skips this check entirely)
- **Bypass**: Set `force: true` in the request body (same as write cooldown)

Returns HTTP 429 with code `max-concurrent` when the limit is reached.

### Dry-Run Preview

Write TAs can specify a `dry_run_action` (name of a read TA) for preview:

- Request body: `"dry_run": true`
- Executes the referenced read TA instead (e.g. `get_deployments` before `rollout_restart`)
- The execution record stores: original `action` (what was requested), `executed_action` (what actually ran), and `dry_run: true`
- Write cooldown and max-concurrent checks are bypassed for dry-run requests

### Force Bypass

The `force: true` flag bypasses both safety controls:

- **Write cooldown**: Skipped entirely
- **Max concurrent**: Skipped entirely
- The `force` flag is recorded in the execution record for audit purposes
- Queryable via `GET /runs?force=true` to find all forced executions

---

## DynamoDB Schema

### Table: `<env>-regional-zoa-executions`

| Attribute          | Type                   | Key | Description                                                                                                         |
| ------------------ | ---------------------- | --- | ------------------------------------------------------------------------------------------------------------------- |
| `executionId`      | String                 | PK  | UUID v4                                                                                                             |
| `accountId`        | String                 | —   | AWS account ID of caller                                                                                            |
| `callerArn`        | String                 | —   | Full ARN of STS caller                                                                                              |
| `operator`         | String                 | —   | Extracted operator name                                                                                             |
| `action`           | String                 | —   | TA name                                                                                                             |
| `targetCluster`    | String                 | —   | Target MC identifier                                                                                                |
| `scope`            | String                 | —   | `kube-api` or `aws-api`                                                                                             |
| `type`             | String                 | —   | `read` or `write`                                                                                                   |
| `params`           | Map                    | —   | Execution parameters (audit trail)                                                                                  |
| `jira`             | String                 | —   | Associated Jira ticket                                                                                              |
| `approvalState`    | String                 | —   | Approval lifecycle state                                                                                            |
| `status`           | String                 | —   | Current status                                                                                                      |
| `outputStatus`     | String                 | —   | `pending`, `uploaded`, or `failed`                                                                                  |
| `revision`         | String                 | —   | Git SHA of TA definition                                                                                            |
| `outputPath`       | String                 | —   | S3 URI for output.json                                                                                              |
| `executedAction`   | String                 | —   | Substituted action name (dry-run only)                                                                              |
| `dryRun`           | Boolean                | —   | Whether this was a dry-run execution                                                                                |
| `force`            | Boolean                | —   | Whether safety checks were bypassed                                                                                 |
| `manifestWorkName` | String                 | —   | Maestro RB name                                                                                                     |
| `createdAt`        | String (RFC3339)       | —   | Submission timestamp                                                                                                |
| `updatedAt`        | String (RFC3339)       | —   | Last status transition timestamp                                                                                    |
| `completedAt`      | String (RFC3339)       | —   | Overall completion timestamp                                                                                        |
| `runnerSeconds`    | Number                 | —   | Runner Job duration (startTime → completionTime)                                                                    |
| `uploadSeconds`    | Number                 | —   | Upload duration (runner completion → uploader completion)                                                           |
| `durationSeconds`  | Number                 | —   | Total wall-clock (created → reconciler detected completion)                                                         |
| `ttl`              | Number (epoch seconds) | —   | DynamoDB TTL for auto-expiry (configurable via `dynamodb_ttl_days`, default 365 days; not exposed in API responses) |

### GSI: `account-index`

| Key | Attribute   | Purpose                                    |
| --- | ----------- | ------------------------------------------ |
| PK  | `accountId` | Scope queries to caller's account          |
| SK  | `createdAt` | Enable time-range queries (`since` filter) |

Projection: ALL

### GSI: `status-index`

| Key | Attribute   | Purpose                                       |
| --- | ----------- | --------------------------------------------- |
| PK  | `status`    | Reconciler queries pending/running executions |
| SK  | `createdAt` | Order by time                                 |

Projection: ALL

**TTL**: Execution records auto-expire via DynamoDB TTL on the `ttl` attribute (set at creation). Retention is configurable via `dynamodb_ttl_days` in the `zoa-job-config` ConfigMap (default: 365 days). The `ttl` field is internal and not returned in API responses.

### Table: `<env>-regional-zoa-audit-log`

| Attribute       | Type                        | Key | Description                                                       |
| --------------- | --------------------------- | --- | ----------------------------------------------------------------- |
| `accountId`     | String                      | PK  | AWS account ID of caller                                          |
| `timestamp`     | String (nanosecond RFC3339) | SK  | When the API call was made (`2006-01-02T15:04:05.000000000Z`)     |
| `approvalState` | String                      | —   | Approval state at time of POST (populated for POST /run)          |
| `id`            | String (UUID)               | —   | Unique audit entry ID                                             |
| `callerArn`     | String                      | —   | Full ARN of STS caller                                            |
| `operator`      | String                      | —   | Extracted operator name                                           |
| `method`        | String                      | —   | HTTP method (`GET`, `POST`)                                       |
| `path`          | String                      | —   | Full request URI (path + query string)                            |
| `action`        | String                      | —   | TA name (populated for POST /run)                                 |
| `targetCluster` | String                      | —   | Target cluster (populated for POST /run)                          |
| `executionId`   | String                      | —   | Execution ID (POST /run: created ID; GET /runs/{id}: accessed ID) |
| `jira`          | String                      | —   | Jira ticket (populated for POST /run)                             |
| `statusCode`    | Number                      | —   | HTTP response status code                                         |
| `ttl`           | Number (epoch seconds)      | —   | DynamoDB TTL for auto-expiry (configurable, default 365 days)     |

**Key design**: Uses `accountId` as PK and nanosecond-precision `timestamp` as SK, enabling efficient time-range queries per account without a GSI. The `since` filter applies as a key condition on the sort key.

**Field consistency**: All entries have the same fields. Fields not applicable to a given endpoint are stored as empty strings, ensuring uniform schema for querying and reporting.

**Audited endpoints and field population**:

| Endpoint             | `action` | `target_cluster` | `execution_id`   | `jira` | `approval_state` |
| -------------------- | -------- | ---------------- | ---------------- | ------ | ---------------- |
| `POST /{action}/run` | TA name  | target cluster   | created exec ID  | ticket | approval state   |
| `GET /runs/{id}`     | —        | —                | accessed exec ID | —      | —                |
| `GET /runs`          | —        | —                | —                | —      | —                |
| `GET /audit`         | —        | —                | —                | —      | —                |

**Rejected requests**: POST requests rejected by validation (400) or rate limits (429) are also audited with whatever context is available at the point of rejection.

**TTL**: Audit entries auto-expire via DynamoDB TTL. Retention is configurable via `dynamodb_ttl_days` in the `zoa-job-config` ConfigMap (default: 365 days).

---

## Usage Examples

### Execute a Trusted Action (synchronous via CLI)

```bash
# CLI wraps: POST + poll GET /runs/{id} (metadata) + final GET /runs/{id}?include=output
$ zoa run get_pods -t mc-useast1-1 -n maestro --jira ROSAENG-1234
```

### Execute (raw curl)

```bash
curl -X POST "$ZOA_API/api/v0/trusted-actions/get_pods/run" \
  --aws-sigv4 "aws:amz:us-east-1:execute-api" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" \
  -H "x-amz-security-token: $AWS_SESSION_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"target_cluster": "mc-useast1-1", "jira": "ROSAENG-1234", "params": {"namespace": "maestro"}}'
```

### Dry-run preview before a write action

```bash
curl -X POST "$ZOA_API/api/v0/trusted-actions/rollout_restart/run" \
  --aws-sigv4 "aws:amz:us-east-1:execute-api" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" \
  -H "x-amz-security-token: $AWS_SESSION_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"target_cluster": "mc-useast1-1", "jira": "ROSAENG-1234", "dry_run": true, "params": {"namespace": "maestro", "name": "maestro"}}'
```

### Poll execution status

```bash
curl "$ZOA_API/api/v0/trusted-actions/runs/fa65418c-..." \
  --aws-sigv4 "aws:amz:us-east-1:execute-api" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" \
  -H "x-amz-security-token: $AWS_SESSION_TOKEN"
```

### Retrieve output

```bash
curl "$ZOA_API/api/v0/trusted-actions/runs/fa65418c-...?include=output" \
  --aws-sigv4 "aws:amz:us-east-1:execute-api" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" \
  -H "x-amz-security-token: $AWS_SESSION_TOKEN"
```

### List failed executions in the last 24h

```bash
curl "$ZOA_API/api/v0/trusted-actions/runs?status=failed&since=24h&limit=50" \
  --aws-sigv4 "aws:amz:us-east-1:execute-api" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" \
  -H "x-amz-security-token: $AWS_SESSION_TOKEN"
```

### List forced executions in the last 7 days

```bash
curl "$ZOA_API/api/v0/trusted-actions/runs?force=true&since=7d" \
  --aws-sigv4 "aws:amz:us-east-1:execute-api" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" \
  -H "x-amz-security-token: $AWS_SESSION_TOKEN"
```

### List audit entries for a specific action in the last hour

```bash
curl "$ZOA_API/api/v0/trusted-actions/audit?action=get_pods&since=1h" \
  --aws-sigv4 "aws:amz:us-east-1:execute-api" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" \
  -H "x-amz-security-token: $AWS_SESSION_TOKEN"
```

### List dry-run executions

```bash
curl "$ZOA_API/api/v0/trusted-actions/runs?dry_run=true&since=24h" \
  --aws-sigv4 "aws:amz:us-east-1:execute-api" \
  --user "$AWS_ACCESS_KEY_ID:$AWS_SECRET_ACCESS_KEY" \
  -H "x-amz-security-token: $AWS_SESSION_TOKEN"
```

---

## Related Documentation

- [ZOA Architecture](https://github.com/openshift-online/rosa-regional-platform/blob/main/docs/design/zoa-architecture.md) — System architecture and network flows
- [ZOA Security Model](https://github.com/openshift-online/rosa-regional-platform/blob/main/docs/design/zoa-security-model.md) — SA isolation and RBAC
- [ZOA Trusted Actions](https://github.com/openshift-online/rosa-regional-platform/blob/main/docs/design/zoa-trusted-actions.md) — TA template format and CLI design
