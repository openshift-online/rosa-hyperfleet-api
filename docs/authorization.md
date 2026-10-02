# Platform API authorization operating reference

ROSAENG-67491 is an in-process, configuration-backed Cedar proof of concept: `GET /api/v0/clusters` maps to `ListClusters`, and `GET /api/v0/clusters/{id}` to `DescribeCluster`. It uses no remote policy service and is not deployment-complete authorization.

Cluster writes and other resource routes remain unmapped: they retain existing admission/handler behavior and report no Cedar success.

## Startup inputs

Run `bin/rosa-hyperfleet-api serve`:

| Flag | Environment variable | Default / behavior |
| --- | --- | --- |
| `--authz-resolver` | `AUTHZ_RESOLVER` | `config`, the only accepted resolver. |
| `--authz-config-file` | `AUTHZ_CONFIG_FILE` | Required nonblank, readable bundle path; no default. |
| `--api-port` | None | `8000`. |
| `--health-port` | None | `8080`. |
| `--metrics-port` | None | `9090`, serving `/metrics`. |
| None | `API_BIND_ADDRESS`, `HEALTH_BIND_ADDRESS`, `METRICS_BIND_ADDRESS` | Each defaults to `0.0.0.0`; nonempty values override. |
| `--postgres-dsn` | `POSTGRES_DSN` | Required; a nonempty flag wins. |
| `--dynamodb-region` | AWS SDK region chain, including `AWS_REGION` | Deprecated flag is a fallback only when the SDK finds no region. |

Authorization precedence is explicit flag > present environment variable > flag default, including empty values. Empty resolver/path inputs are invalid. Service region is trusted configuration, never a request header or attachment.

`--allowed-accounts` and `--dynamodb-prefix` are deprecated and ignored; `ALLOWED_ACCOUNTS` does not enroll callers. DynamoDB provides no policy/enrollment fallback. Logging defaults: `--log-format=json`, `--log-level=info`.

The API trusts gateway-provided account and caller ARN headers. Deployed access must not bypass the authenticated gateway: direct listener callers can supply those headers. The local HTTP runner injects fake identities only on loopback.

## Version 1 bundle

`LoadConfig` accepts exactly one YAML document (including JSON objects). All four top-level fields are required; empty arrays are valid and grant no protected reads. Account IDs are quoted 12-digit strings; `formatVersion` is integer `1`.

This example grants Alice collection access and blue-label visibility, grants the readers role regional reads, and forbids one exact session globally. The second account is enrolled without grants.

```yaml
formatVersion: 1
registeredAccounts:
  - "123456789012"
  - "210987654321"
policies:
  - id: list
    ownerAccountID: "123456789012"
    content: |
      permit(principal, action == HyperFleet::Action::"ListClusters", resource is HyperFleet::Collection);
  - id: blue
    ownerAccountID: "123456789012"
    content: |
      permit(principal, action == HyperFleet::Action::"DescribeCluster", resource is HyperFleet::Cluster)
      when { resource.hasTag("example.com/team") && resource.getTag("example.com/team") == "blue" };
  - id: read
    ownerAccountID: "123456789012"
    content: |
      permit(principal, action in HyperFleet::Action::"ReadOnly", resource);
  - id: forbid
    ownerAccountID: "123456789012"
    content: |
      forbid(principal, action in HyperFleet::Action::"ReadOnly", resource);
attachments:
  - id: alice-list
    policyID: list
    principalARN: arn:aws:iam::123456789012:user/alice
    bindingMode: exact-principal
    scope: global
  - id: alice-blue
    policyID: blue
    principalARN: arn:aws:iam::123456789012:user/alice
    bindingMode: exact-principal
    scope: global
  - id: readers-regional
    policyID: read
    principalARN: arn:aws:iam::123456789012:role/platform/readers
    bindingMode: role-membership
    scope: regional
    region: us-east-1
  - id: blocked-session
    policyID: forbid
    principalARN: arn:aws:sts::123456789012:assumed-role/readers/blocked
    bindingMode: exact-principal
    scope: global
```

- Policy and attachment IDs are unique within their respective lists and match `[A-Za-z0-9][A-Za-z0-9._-]*`.
- Each policy contains exactly one Cedar statement. Attachments reference existing policies whose owner account matches the principal ARN account.
- `global` applies in every service region and has no region value. `regional` requires a valid region matching the service region to apply.
- The loader rejects unknown fields, duplicate YAML keys, null collections, nonstring record values, aliases, anchors, merge keys, unsupported tags, extra documents, excessive nesting, malformed/schema-invalid policies, templates, dangling references, duplicate IDs, unsupported binding modes, and cross-account attachments.
- Startup validation includes unattached policies and off-region attachments; scope cannot hide invalid material.

## Enrollment and attachment seeding

Enrollment is exact membership in `registeredAccounts`, separate from authorization. Enrolled callers without applicable policies are denied; grants cannot bypass enrollment. Enrollment alone grants neither service-operator access nor ManagementCluster permissions. No account-wide or allow-all fallback exists.

Local/ephemeral seeds must enroll actual caller accounts and attach grants to verified principals. Use complete IAM user ARNs with `exact-principal`, complete IAM role ARNs (including paths) with `role-membership`, and complete STS assumed-role ARNs (including session names) with `exact-principal` for session permits/forbids. Exact role bindings and session membership bindings are invalid.

`test/e2e-api/testdata/authz-http.json` uses example accounts: `111111111111` is enrolled with Alice's list/blue-label grants; `222222222222` is enrolled without grants; `333333333333` has an unusable grant but is not enrolled. It also covers a readers role, an exact blocked session, list-only/describe-only users, and a global forbid beside a regional permit. These are test identities, not verified deployed principals or credentials; ephemeral seeds need environment-specific caller records.

### Binding and role-path limits

The request principal is `HyperFleet::Principal` identified by the complete caller ARN. Matching role attachments add the configured `HyperFleet::Role` as a parent. Binding adds exact-principal equality or role membership while preserving original principal/action/resource scopes and `when`/`unless` conditions.

Binding does not rewrite equality: `principal == HyperFleet::Role::"arn:aws:iam::123456789012:role/platform/readers"` rejects child sessions; `principal in HyperFleet::Role::"arn:aws:iam::123456789012:role/platform/readers"` can match them.

STS session ARNs omit role paths. Matching uses partition, account, and final role name: `role/platform/readers` matches `assumed-role/readers/allowed` in the same partition/account. Configured role ARNs/paths remain in entities/provenance. Two paths with the same partition/account/final name fail startup as ambiguous, even across regions. This static match does not verify role paths through STS/IAM; different partitions, accounts, or final names cannot match.

All applicable global/regional attachments enter one request policy set. Forbid overrides permit; an exact-session forbid affects only that session, even beside a role permit. Any evaluation diagnostic fails authorization, even if native Cedar returns allow. Resolver errors invalidate partial material.

## Resources and stored labels

Schema: `platform-api/pkg/authz/testdata/hyperfleet.cedarschema`. Entity types are Principal, Role, Collection, and Cluster; `ReadOnly` groups `ListClusters` and `DescribeCluster`.

| Input | Cedar representation |
| --- | --- |
| Trusted caller account | Principal/Role `account` attributes. |
| Trusted service region and caller identity | Context `region`, `accountId`, `principalArn`. |
| Collection | ID `<account>/<region>/clusters`; `account`/`region` attributes; no tags. |
| Stored Cluster | ID `<account>/<region>/<stable-cluster-id>`; parent Collection; `account`/`region` attributes. |
| Stored Cluster `metadata.labels` | String-keyed/string-valued entity tags. |

Labels are not finite record attributes, Cluster `spec.tags`, cloud tags, headers, query parameters, or body claims. Keys retain punctuation (e.g. `example.com/team`). Strict validation requires `hasTag` before a `getTag` that could encounter an absent key. A different/absent label in the blue policy is an ordinary deny.

FleetDB reads are account-scoped before evaluation. Foreign/missing Clusters retain 404 without revealing foreign existence. Ownership, region, stable ID, and any account label must agree.

Lists check collection permission, then `DescribeCluster` on every candidate, including off-page objects. Filtering precedes totals/paging; list permission alone yields no items without describe permission. Any late item failure rejects the whole response, never partial success.

## Snapshot lifecycle and error logs

Startup loads/validates the entire bundle before database setup or API/health/metrics listeners open. The resolver retains an immutable snapshot; requests never reread the file. File replacement requires restart. Invalid replacements fail startup without reusing the old snapshot or serving requests.

`PolicyRevision` and `AttachmentRevision` are lowercase hexadecimal SHA-256 of the complete file bytes, including whitespace. Each attachment has diagnostic ID `attachment/<attachment-id>`, even when sharing a policy.

- Startup logs `authorization startup failed`. Typed failures include `stage`, `cause`, `provenance`, `diagnostics`: missing files use `resolution`, malformed bundles/policies `parsing`, invalid attachment material `binding`. Missing inputs/unsupported resolvers log the input error. Exit is nonzero, without permissive fallback.
- Runtime logs `cluster authorization failed` with operation, trusted identity, region, stage, cause, provenance, diagnostics. Preparation/evaluation failures return structured `AUTHZ-FAILED-001` 500 (`Authorization failed`); ordinary denies return `AUTHZ-DENIED-001` 403. Database failures retain existing API errors. Internal causes/diagnostics stay out of public response text; identity/policy metadata in logs requires restricted access.

In sibling `rosa-hyperfleet`, source values `applications.regional-cluster.platformApi.authz.resolver` and `.config` feed chart values `platformApi.authz`. The chart creates `authz-config`, mounts `config.yaml` read-only at `/etc/platform-api/authz/config.yaml`, and sets `AUTHZ_RESOLVER`/`AUTHZ_CONFIG_FILE`. Mode `0644` permits reads by image UID/GID 65534.

Pod-template annotation `checksum/authz-config` hashes the complete rendered ConfigMap, so account/policy/attachment changes trigger rollout. The rate-limit mount/checksum is separate. Chart defaults contain empty arrays, not example grants. Rendering/checksum tests do not prove deployed rollout.

Old replicas may serve old snapshots until stopped; existing requests may retain prepared inputs. File replacement alone does not revoke access; multi-replica rollout does not guarantee instantaneous revocation.

## Metrics

The existing Prometheus registry serves `/metrics` on the metrics listener (default `9090`). Each started, admitted mapped request attempt records one terminal result and duration; only errors record a failure sample.

| Metric | Type | Labels |
| --- | --- | --- |
| `authz_requests_total` | Counter | `operation`, `outcome` |
| `authz_duration_seconds` | Histogram, seconds | `operation`, `outcome` |
| `authz_failures_total` | Counter, first terminal failure stage | `operation`, `stage` |

Exact label values: `operation` is `ListClusters` or `DescribeCluster`; `outcome` is `allow`, `deny`, or `error`; failure `stage` is `resolution`, `parsing`, `binding`, `entity_validation`, `evaluation`, or `resource_loading`. Allow/deny produce no failure-stage sample.

Labels never contain URLs, IDs, accounts, ARNs, principals, policy text, or error messages. Summing outcomes gives attempts per operation, not policies, attachments, or objects. HTTP status alone does not determine outcome.

Bucket upper bounds (seconds): `0.001`, `0.005`, `0.01`, `0.025`, `0.05`, `0.1`, `0.25`, `0.5`, `1`, automatic `+Inf`. Prometheus emits `_bucket` with `le`, `_sum`, `_count`. The 50 ms bucket is a measurement boundary, not an SLO or demonstrated performance guarantee.

Timing spans preparation through terminal authorization, including resolution, parsing, binding, entity construction/validation, evaluation, and required resource reads. It excludes identity/enrollment admission, rate limiting, response conversion/serialization, socket writes, request output, and client delivery. This is neither engine-only nor whole-request latency.

- Successful filtered list, including all items denied: one `ListClusters/allow`.
- Collection/object deny: one deny; 403.
- Missing/foreign Cluster after attempt starts: one `DescribeCluster/deny`; 404.
- Resolution/parsing/binding/entity-validation/evaluation failure: one error at its terminal stage.
- Late list failure: one `ListClusters/error`, no prior item allow samples.
- Database read failure: one error at `resource_loading`; existing API error retained.
- Response-write failure after allow: allow unchanged; no additional sample.

Missing/invalid identity, unregistered accounts, and 429 responses start no attempt. Public health/readiness/info/metrics calls add no samples; unmapped routes add no successful Cedar outcome.

## Local verification commands and artifacts

Paths are relative to the API repo root:

| Directory | Command | Scope |
| --- | --- | --- |
| Root | `make test-e2e-authz` | Build real API; run only `TestAuthzHTTP` with disposable PostgreSQL. |
| Root | `make test-e2e-authz TEST_OUTPUT_DIR=/tmp/authz-results` | Same, explicit artifact parent. |
| Root | `make test-api` | API unit tests, race detection. |
| Root | `make test-api-int` | Integration-tagged handler tests, race detection. |
| Root | `make build-api` | API binary. |
| Root | `make lint` | All Makefile-configured modules. |
| `platform-api` | `go test -race -count=1 ./pkg/authz/...` | Resolver, binding, evaluation, strict capabilities, snapshots, metrics. |
| `platform-api` | `../hack/tools/bin/golangci-lint run --config ../.golangci.yml --timeout 5m ./pkg/authz/...` | Authorization lint. |
| `test` | `go test -race -count=1 ./helpers/aws` | Local/signed-client regressions. |
| `test` | `go test -race -count=1 -run '^TestPatchMethodHeaders$' ./e2e-api` | Unsigned-localhost regression. |

The HTTP target requires Go, GNU Make, Git, working Podman (not Docker), `setsid`, and access to `docker.io/library/postgres:16-alpine`. It rejects `PGCTL_DSN`, owns disposable PostgreSQL/API processes, and cleans up on exit/interruption. API/health/metrics bind to `127.0.0.1` on selected ephemeral ports; rate limiting is disabled.

No AWS credentials are needed: the test clears inherited AWS variables, disables EC2 metadata, uses empty AWS config/credential files, and supplies `AWS_REGION=us-east-1`. Requests use unsigned fake gateway identities. It contacts neither a deployed API nor an external policy service and does not prove Gateway SigV4 authentication.

Runs create `test-results/authz-http-<timestamp>-<pid>/`; `TEST_OUTPUT_DIR` or `ARTIFACT_DIR` overrides the parent:

- `authz-http.log`: Go test output.
- `junit-authz-http.xml`: scenario results, or runner failure before test reporting.
- `api-*.log`: API startup/listener/errors.
- Container-named `.log` files: PostgreSQL output.
- `authz-config.json`: run's bundle.
- `run-manifest.txt`: baseline revision, Go/Podman versions, binary/test checksums, DB image, bind settings, selected test.

Git revision alone misses uncommitted changes; checksums identify actual binary/test inputs. Record API/test/platform revisions and image revision if built. Local binary checks do not prove the chart's pinned image includes authorization. `make verify-mod` and generated-output checks may modify files; they are not read-only lint.

## Dependency boundary

Pinned dependency: `github.com/cedar-policy/cedar-go v1.8.0`. Production schema resolution/strict validation uses experimental `x/exp/ast`, `x/exp/schema`, `x/exp/schema/resolved`, `x/exp/schema/validate`, without stable-package compatibility guarantees. Upgrades require passing strict capability, binding, diagnostic, entity-tag, and schema tests, never weakening validation or ignoring forbid diagnostics to accommodate changes.
