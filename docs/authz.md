# Platform API authorization

The API evaluates Cedar in-process (`cedar-go v1.8.0`) against a configuration
bundle loaded at startup. Every implemented resource operation requires an
explicit grant. Enrollment and gateway authentication grant nothing.

Not implemented: label APIs, policy/attachment management, account linking,
AccessEntry, `/authz/check`. Chart delivery and provisioning grants:
`rosa-hyperfleet/docs/platform-api-authorization.md`.

## Startup

| Input                                       | Behavior                                              |
| ------------------------------------------- | ----------------------------------------------------- |
| `--authz-config-file` / `AUTHZ_CONFIG_FILE` | Required bundle path. The flag wins, even when empty. |
| AWS SDK region (`AWS_REGION`)               | Fixed service region. Required.                       |
| `--dynamodb-region`                         | Deprecated region fallback.                           |
| `--allowed-accounts`, `--dynamodb-prefix`   | Deprecated, ignored.                                  |

Startup validates the whole bundle before opening the database or listeners. On
failure it logs `authorization startup failed` and exits nonzero.

The bundle is an immutable snapshot. Changes require a restart and rollout;
old replicas keep old permissions until replaced.

## Bundle

One YAML document, version 1:

```yaml
formatVersion: 1
registeredAccounts: ["123456789012"]
policies:
  - id: read
    ownerAccountID: "123456789012"
    content: permit(principal, action in HyperFleet::Action::"ReadOnly", resource);
attachments:
  - id: readers
    policyID: read
    principalARN: arn:aws:iam::123456789012:role/platform/readers
    scope: regional # or global, with no region
    region: us-east-1
serviceOperatorPolicies: [] # optional; must appear with serviceOperatorAttachments
serviceOperatorAttachments: []
```

- `registeredAccounts`: accounts admitted to the API. Admission only.
- Each policy holds one Cedar statement, strictly validated against the schema.
- An attachment's principal account must equal its policy's `ownerAccountID`.
- `regional` attachments apply only in their region; `global` applies everywhere.
- Service-operator records authorize ManagementClusters only. Customer and service
  records are separate domains: IDs, references, and policies never cross.
- Startup fails on any invalid record, including unattached policies and
  off-region attachments, and on unknown fields, duplicate keys or IDs, YAML
  aliases, dangling references, and ambiguous role aliases.

## Principal matching

| Attachment ARN        | Matches                                                     |
| --------------------- | ----------------------------------------------------------- |
| IAM user, STS session | That exact caller only.                                     |
| IAM role              | Its assumed-role sessions; the role becomes a Cedar parent. |

STS ARNs omit role paths, so roles match on account and final role name. Two
configured roles differing only by path fail startup. Only the `aws` partition
is supported.

Policies are evaluated unchanged: `principal == HyperFleet::Role::"..."` does not
match a session; `principal in` does. Forbid beats permit. Any evaluation error
fails the request, even when Cedar would allow.

## Operations

| Resource          | Operation             | Required actions                                                     |
| ----------------- | --------------------- | -------------------------------------------------------------------- |
| Cluster           | List                  | ListClusters; DescribeCluster per item for details                   |
| Cluster           | Get                   | DescribeCluster                                                      |
| Cluster           | Create                | CreateCluster                                                        |
| Cluster           | PUT / PATCH           | UpdateCluster + changed-field actions                                |
| Cluster           | Delete                | DeleteCluster                                                        |
| NodePool          | List                  | ListNodePools; DescribeNodePool per item for details                 |
| NodePool          | Get                   | DescribeNodePool                                                     |
| NodePool          | Create                | CreateNodePool                                                       |
| NodePool          | PUT                   | UpdateNodePool + changed-field actions                               |
| NodePool          | Delete                | DeleteNodePool                                                       |
| OIDCConfig        | List                  | ListOIDCConfigs; DescribeOIDCConfig per item for details             |
| OIDCConfig        | Get / Create / Delete | DescribeOIDCConfig / CreateOIDCConfig / DeleteOIDCConfig             |
| DNSReservation    | List                  | ListDNSReservations; DescribeDNSReservation per item for details     |
| DNSReservation    | Get / Create / Delete | DescribeDNSReservation / CreateDNSReservation / DeleteDNSReservation |
| ManagementCluster | List                  | ListManagementClusters; DescribeManagementCluster for details        |
| ManagementCluster | Get / Create          | DescribeManagementCluster / CreateManagementCluster                  |

Changed-field actions, required in addition to the base update:

| Changed field                                                | Action                |
| ------------------------------------------------------------ | --------------------- |
| Cluster `hostedCluster.release`, `controlPlaneUpgradePolicy` | UpdateClusterVersion  |
| Cluster `hostedCluster.configuration`                        | UpdateClusterConfig   |
| NodePool `nodePool.replicas`, `nodePool.autoScaling`         | ScaleNodePool         |
| NodePool `nodePool.release`                                  | UpdateNodePoolVersion |

Rules:

- Lists return every item and count it in `total` (EKS style). Items without
  Describe show only `metadata.name`, `namespace`, `uid`, and `status.phase`
  (ManagementCluster: `id`); other fields are zero values. Any Describe error
  fails the whole list.
- Updates compare the stored spec with the merged object to be written. All
  required actions must allow before any write.
- Service-set fields never change on update. An exact echo is accepted;
  changing or clearing one returns 422. An explicit `null` parent keeps its
  service-set children.
- Updates and deletes write the checked `resourceVersion`. On a conflict, updates
  retry (up to 5 attempts), re-reading, re-validating, and re-authorizing each
  attempt; exhausted retries return 409 `*-MGMT-UPDATE-006`. Deletes return 409
  without retrying, except DNSReservation, which keeps upstream's conflict retry
  and re-authorizes each attempt.
- Denials return 403 `AUTHZ-DENIED-001`; authorization failures return 500
  `AUTHZ-FAILED-001`. Responses never include policy details.

## Entities

Namespace `HyperFleet`. The schema is embedded from
`platform-api/pkg/authz/testdata/hyperfleet.cedarschema`.

| Entity            | UID                                                                     | Parents                        |
| ----------------- | ----------------------------------------------------------------------- | ------------------------------ |
| Principal         | caller ARN                                                              | matching Roles                 |
| Collection        | `<account>/<region>/{clusters,nodepools,oidc_configs,dns_reservations}` | none                           |
| Cluster           | `<account>/<region>/<cluster-uid>`                                      | Collection                     |
| NodePool          | `<account>/<region>/<cluster-uid>/<pool-uid>`                           | its stored Cluster, Collection |
| OIDCConfig        | `<account>/<region>/<config-uid>`                                       | Collection                     |
| DNSReservation    | `<account>/<region>/<reservation-uid>`                                  | Collection                     |
| ServiceCollection | `<region>/management_clusters`                                          | none                           |
| ManagementCluster | `<region>/<mc-id>`                                                      | ServiceCollection              |

- Resources carry `account` and `region` attributes (ServiceCollection: `region`
  only; ManagementCluster adds `registrationRegion`). `region` is always the
  service region.
- Stored `metadata.labels` are entity tags. Use `hasTag` before `getTag`.
  Collections have no tags.
- UIDs are FleetDB object UIDs. Create checks use the validated candidate name in
  place of the UID, before any write.
- Ownership comes from storage (`account-<accountID>` namespace, account label),
  never from the request. A NodePool's parent is its stored controller reference
  and `hyperfleet.io/cluster-uid` label, re-checked against the parent's current
  UID; `clusterId` only filters lists.

## Context

Frozen once per request: `region`, `accountId`, `principalArn`, `sourceIp`,
`userAgent`, and `requestTime {unixSeconds, dayOfWeek (Mon=1), hour}` in UTC.
`sourceIp` comes from the gateway. `userAgent` is caller-controlled; do not trust it.

## Groups and examples

| Group               | Actions                                                                   |
| ------------------- | ------------------------------------------------------------------------- |
| ReadOnly            | Customer List and Describe actions                                        |
| ClusterAdmin        | Create, Update, UpdateClusterConfig, UpdateClusterVersion, Delete Cluster |
| NodePoolAdmin       | Create, Update, ScaleNodePool, UpdateNodePoolVersion, Delete NodePool     |
| OIDCConfigAdmin     | Create, Delete OIDCConfig                                                 |
| DNSReservationAdmin | Create, Delete DNSReservation                                             |
| AllActions          | The five customer groups. Not ServiceOperator.                            |
| ServiceOperator     | Create, List, Describe ManagementCluster                                  |

These examples are validated against the schema in tests.

```cedar
permit(principal, action in HyperFleet::Action::"ReadOnly", resource);
```

```cedar
permit(principal, action in HyperFleet::Action::"AllActions", resource);
```

```cedar
forbid(principal, action == HyperFleet::Action::"DeleteCluster", resource)
when { resource.hasTag("Environment") && resource.getTag("Environment") == "production" };
```

Team-scoped admin (also needs a List grant to discover clusters):

```cedar
permit(principal, action in HyperFleet::Action::"ClusterAdmin", resource)
when { resource.hasTag("Team") && resource.getTag("Team") == "platform-engineering" };
```

Scaling needs the base update too:

```cedar
permit(principal, action in [HyperFleet::Action::"UpdateNodePool", HyperFleet::Action::"ScaleNodePool", HyperFleet::Action::"DescribeNodePool", HyperFleet::Action::"ListNodePools"], resource);
```

```cedar
permit(principal, action in HyperFleet::Action::"NodePoolAdmin", resource)
when { resource in HyperFleet::Cluster::"123456789012/us-east-1/550e8400-e29b-41d4-a716-446655440000" };
```

```cedar
permit(principal, action, resource)
when { context.requestTime.dayOfWeek >= 1 && context.requestTime.dayOfWeek <= 5 && context.requestTime.hour >= 9 && context.requestTime.hour < 17 };
```

```cedar
forbid(principal, action, resource)
unless { ["us-east-1", "us-west-2"].contains(context.region) };
```

Valid only in `serviceOperatorPolicies`:

```cedar
permit(principal, action in HyperFleet::Action::"ServiceOperator", resource);
```

## Logs and metrics

Failures log `request authorization failed` with `operation` (the request),
`action` (the failing check), caller, `stage`, `cause`, `provenance` (policy,
attachment, `PolicyRevision` = SHA-256 of the bundle file), and Cedar
`diagnostics`. Denials log `request authorization denied` at INFO with
`operation`, `action`, caller, and `provenance` (empty when no policy permits;
forbid IDs when one matches). Logs never include policy content.

| Metric                   | Labels                 |
| ------------------------ | ---------------------- |
| `authz_requests_total`   | `operation`, `outcome` |
| `authz_duration_seconds` | `operation`, `outcome` |
| `authz_failures_total`   | `operation`, `stage`   |

- One sample per request, plus one per update conflict retry; `operation` is the
  request action.
- `outcome`: `allow`, `deny`, `error`. `stage`: `resolution`, `parsing`,
  `binding`, `entity_validation`, `evaluation`, `resource_loading`.
- Unauthenticated, unenrolled, rate-limited, and public requests record nothing.

## Verify locally

| Command               | Covers                                                                                             |
| --------------------- | -------------------------------------------------------------------------------------------------- |
| `make test-api`       | Unit tests, including `platform-api/pkg/authz`.                                                    |
| `make test-api-int`   | Handler and server integration tests.                                                              |
| `make test-e2e-authz` | Real binary over HTTP with disposable PostgreSQL (Podman). Results in `test-results/authz-http-*`. |

## Limits

- The API trusts gateway identity headers. The listener must be reachable only
  through the gateway.
- The schema validator uses `cedar-go` experimental packages. Upgrades must keep
  all authz tests passing without weakening validation.
