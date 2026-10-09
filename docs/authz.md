# Platform API Cedar authorization

The API evaluates Cedar in-process using the pinned `cedar-go v1.8.0` library.
Gateway IAM/SigV4 authentication and account enrollment are admission checks,
not permission grants. Every implemented non-label resource operation requires
an explicit grant. There is no account-wide privileged-principal bypass.

Account linking, policy/attachment management, Red Hat policy administrators,
AccessEntry, labels, and `/authz/check` are future APIs, not implemented routes.
Bootstrap or future policy-admin status does not authorize ManagementClusters.

## Authorizer ownership

`LoadConfig(path, serviceRegion)` returns an authorizer with a validated, immutable
startup snapshot. Startup resolves service region before constructing the
authorizer and before opening database connections or listeners. Service region
identifies the API deployment handling the request, not the caller's location.

Handlers call `Prepare` once with trusted account/caller identity and request
metadata, then use `Prepared.Check` for every required action and resource.
Identity, resource, and parent inputs do not repeat service region. The authorizer
supplies the region in Cedar context, entity attributes, and regional UIDs.
Attachment region and ManagementCluster `registrationRegion` remain distinct.

The private policy source selects applicable, validated attachments. Policy text
is parsed and strictly validated during configuration loading, not during request
preparation. Compiled policies are shared read-only; policy sets, entities, and
provenance maps are request-local. Source errors discard partial material.

The planned DynamoDB source retains per-request reads and validates newly read
material before preparation. It does not expose policy text to handlers or promise
an atomic snapshot across records. `IsAccountRegistered` remains a separate
admission lookup on the authorizer and grants no action permissions.

## Implemented operation contract

Authorization runs in handlers, never by inferring an action from a URL.

| Resource          | HTTP operation | Required action(s)                                                   | Trusted input                                                           |
| ----------------- | -------------- | -------------------------------------------------------------------- | ----------------------------------------------------------------------- |
| Cluster           | GET collection | ListClusters, then DescribeCluster per candidate                     | Account collection, then stored objects                                 |
| Cluster           | GET item       | DescribeCluster                                                      | Stored account-scoped snapshot                                          |
| Cluster           | POST           | CreateCluster                                                        | Validated, server-enriched candidate, before OIDC claim                 |
| Cluster           | PUT / PATCH    | UpdateCluster plus every changed protected field action              | Stored labels/identity; raw submitted spec and locally merged candidate |
| Cluster           | DELETE         | DeleteCluster                                                        | Same checked stored version passed to delete                            |
| NodePool          | GET collection | ListNodePools, then DescribeNodePool per candidate                   | Account collection and each stored parent                               |
| NodePool          | GET item       | DescribeNodePool                                                     | Selected stored pool and its stored parent                              |
| NodePool          | POST           | CreateNodePool                                                       | Validated candidate and account-scoped parent                           |
| NodePool          | PUT            | UpdateNodePool plus every changed protected field action             | Stored labels/parent; raw spec and candidate                            |
| NodePool          | DELETE         | DeleteNodePool                                                       | Same checked stored pool version                                        |
| OIDCConfig        | GET collection | ListOIDCConfigs, then DescribeOIDCConfig per candidate               | Account collection and account-namespace objects                        |
| OIDCConfig        | GET item       | DescribeOIDCConfig                                                   | Stored account-namespace object                                         |
| OIDCConfig        | POST           | CreateOIDCConfig                                                     | Normalized, server-enriched candidate; customer claim label stripped    |
| OIDCConfig        | DELETE         | DeleteOIDCConfig                                                     | Stored snapshot, before in-use check; exact checked version             |
| ManagementCluster | GET collection | ListManagementClusters, then DescribeManagementCluster per candidate | Service collection and global stored registrations                      |
| ManagementCluster | GET item       | DescribeManagementCluster                                            | Global stored registration                                              |
| ManagementCluster | POST           | CreateManagementCluster                                              | Validated global `{id, region, accountId}` candidate                    |

Unsupported methods remain typed HTTP 405. Health/info stay public. Errors retain
`api.APIError` / Kubernetes `metav1.Status` envelopes. Authorization denials and
failures expose no policy contents, labels, bindings, or evaluator diagnostics.
Bounded request-level authorization metrics retain one terminal outcome per
request, including all required list evaluations.

Collections have **no object labels**. A List grant alone returns an empty list;
Describe alone cannot list. All candidates, including off-page candidates, are
checked before totals/pagination. Any evaluation or parent-loading error aborts
with a typed failure, never a partial-success response. ManagementCluster retains
its existing `kind/items/total` envelope without pagination.

### Changed fields and concurrency

Updates merge only the raw submitted spec into a DeepCopy. Omitted fields remain
unchanged. Duplicate keys and case aliases are rejected; exact typed old/new
comparisons avoid numeric precision loss. Existing immutable, service-set,
write-mode and feature-gate validation always applies, even if an action allows.

- Cluster release and controlPlaneUpgradePolicy: **UpdateClusterVersion**.
- Accepted Cluster configuration changes: **UpdateClusterConfig**.
- NodePool replicas and autoScaling, including resets: **ScaleNodePool**.
- NodePool release: **UpdateNodePoolVersion**.
- Other accepted changes: the mandatory UpdateCluster / UpdateNodePool base.

Every required action must allow before the first write. Echoes and omissions do
not require specialized actions, but still require the base operation. Caller
metadata/resourceVersion cannot replace the loaded snapshot. Updates and deletes
require a positive supported object resourceVersion, including FleetDB composite
versions; empty, zero, negative and malformed versions cannot become unconditional
writes. `ValidateObjectResourceVersion` reuses FleetDB's authoritative parser.
Conflicts return HTTP 409 without automatic retries or unchecked reloads. A caller
retry performs fresh loading, validation, changed-field analysis and authorization.

A NodePool's parent Cluster and child are separate reads. Child CAS does **not**
atomically freeze parent labels. This is trusted-snapshot authorization, not a
cross-object transaction. List checks likewise share a policy/context snapshot,
not one database-wide resource snapshot.

## Entities, ownership, and flat NodePool lookup

All entities use namespace **HyperFleet**. Principal IDs are full caller ARNs;
assumed-role principals have applicable IAM Role parents and an `account` attribute.

| Entity            | UID                                                                  | Attributes / parents                                                                |
| ----------------- | -------------------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| Collection        | `<account>/<service-region>/clusters`, `/nodepools`, `/oidc_configs` | account, region; no tags                                                            |
| Cluster           | `<account>/<service-region>/<cluster-id>`                            | account, region; clusters Collection; stored metadata label tags                    |
| NodePool          | `<account>/<service-region>/<parent-cluster-id>/<pool-name>`         | account, region; actual Cluster and nodepools Collection; stored tags               |
| OIDCConfig        | `<account>/<service-region>/<config-id>`                             | account, region; oidc_configs Collection; stored tags                               |
| ServiceCollection | `<service-region>/management_clusters`                               | region only; no customer account/tags                                               |
| ManagementCluster | `<service-region>/<mc-name>`                                         | hosting account, service region, registrationRegion; ServiceCollection; stored tags |

Storage account scoping precedes inspection. Cluster/NodePool account labels and
stored namespace establish ownership; mutable spec identities/public UIDs do not.
OIDCConfig's account namespace is authoritative. Claim labels are lifecycle state,
not a customer Cluster parent. ManagementClusters stay unscoped, namespace-empty;
their hosting account may differ from the operator caller account.

NodePool routes stay flat. Optional `clusterId` restricts lookup/list only and adds
no ListClusters requirement. Create `metadata.namespace` must be canonical
`cluster-<lowercase UUID>` and resolve an actual account-scoped parent. Other
operations use the selected pool's **stored namespace**, never a query/body parent.
Without clusterId, lookup preserves the first account-scoped matching name in
storage order. Denial does not search for an authorized alternate. Same names in
two clusters have different Cedar UIDs; callers should supply clusterId to avoid
storage-order ambiguity.

## Groups and example policies

ReadOnly contains customer List/Describe actions only. ClusterAdmin contains
Create/Update/Config/Version/DeleteCluster. NodePoolAdmin contains
Create/Update/Scale/Version/DeleteNodePool. OIDCConfigAdmin contains Create/Delete.
AllActions includes those four customer groups, **not** ServiceOperator.
ServiceOperator contains only Create/List/DescribeManagementCluster.

Policy records each contain one statement. Attachments select policies for the
fixed caller of one request; policies are evaluated unchanged. Exact user/session
ARNs match themselves, while role attachments supply explicit role-parent edges.
The unsupported bare `?principal` shorthand is not used. These examples are parsed
and strictly validated against the runtime schema in tests.

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

A team-scoped object grant needs a separate collection List grant:

```cedar
permit(principal, action in HyperFleet::Action::"ClusterAdmin", resource)
when { resource.hasTag("Team") && resource.getTag("Team") == "platform-engineering" };
```

NodePool scaling requires the base update as well as ScaleNodePool. Include Describe
and List for discovery without granting creation/deletion. The base update also
permits ordinary mutable fields; this is not a scaling-field-only role:

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

This policy grants service access **only when placed in the service domain**:

```cedar
permit(principal, action in HyperFleet::Action::"ServiceOperator", resource);
```

## Frozen request context

Each admitted request captures one context for all item/multi-action checks:
`region`, `accountId`, `principalArn`, `sourceIp`, `userAgent`, and
`requestTime: {unixSeconds, dayOfWeek, hour}`. Time is server-clock UTC; weekdays
are ISO Monday=1 through Sunday=7. SourceIP comes only from trusted gateway
middleware context, not X-Forwarded-For, query, or body. UserAgent is descriptive
caller-controlled text, never identity proof. Missing IP/agent are empty strings.
Validated create labels are candidate entity tags, not a requestLabels map.

## Startup configuration and service-operator authority

Format version 1 requires `formatVersion`, `registeredAccounts`, `policies`, and
`attachments`. It optionally accepts paired **serviceOperatorPolicies** and
**serviceOperatorAttachments** arrays, using the same policy/attachment shapes:

```yaml
serviceOperatorPolicies:
  - id: operator
    ownerAccountID: "123456789012"
    content: 'permit(principal, action in HyperFleet::Action::"ServiceOperator", resource);'
serviceOperatorAttachments:
  - id: operator-role
    policyID: operator
    principalARN: arn:aws:iam::123456789012:role/platform/Operator
    scope: regional
    region: us-east-1
```

Both optional arrays absent means **no service grant**. One absent, null/scalar
lists, aliases, unknown fields, duplicate IDs within a domain, bad references or
invalid unattached/off-region records fail startup. References are domain-local.
Customer wildcard permits/AllActions/forbids cannot enter ManagementCluster
evaluation; service policies cannot enter customer evaluation. Provenance uses
`attachment/<id>` versus `service-operator/attachment/<id>`.

The mounted file is one complete version-1 bundle. Version-2 wrappers are rejected;
the API does not merge configuration fragments or infer enrollment or grants.
Unknown fields, duplicate keys, aliases, coercions, extra documents, duplicate
accounts or domain-local IDs, dangling references, and ambiguous role aliases
fail startup. Multiple distinct attachments may grant or forbid operations for
the same principal.

Enrollment admits operator accounts but does not authorize them. The sibling
`rosa-hyperfleet/docs/platform-api-authorization.md` defines explicit provisioning
grants, deployment identity substitution, registration credentials, and the
ROSAENG-67493 trust and rollout prerequisites.

## Principal matching and deployment limits

Exact IAM user and STS session ARNs match only themselves. IAM role attachments
match assumed-role parents by commercial AWS partition, account and final role
name. STS omits IAM role paths; paths are **not isolation boundaries**. Ambiguous
configured paths sharing the same STS role alias fail startup across both domains.
An exact-session forbid overrides its role permit within the same authority set.
Only the currently supported commercial `aws` partition is accepted.

Bundles are immutable startup snapshots; changes require restart/replica rollout,
not instantaneous revocation. The listener must be reachable only through trusted
Gateway networking: direct callers could otherwise spoof forwarded gateway
identity headers. Local injected-header HTTP tests do not prove SigV4/network
isolation or deployment. No remote mutation is needed for local verification:
run the existing local HTTP/Postgres harness described in
[`test/e2e-api/README.md`](../test/e2e-api/README.md).
