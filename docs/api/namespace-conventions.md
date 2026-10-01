# Namespace Conventions

**Last Updated Date**: 2026-10-01

## Summary

HyperFleet CRD resources use Kubernetes-style namespaces (mapped to hyperfleet-db) as the tenancy boundary. A customer resource's namespace is its account; its name is chosen by the client; its uid is minted by the database. The full rules are in [hyperfleet-db guidelines](../../hyperfleet-operator/docs/hyperfleet-db-guidelines.md).

## Account-Scoped Namespaces

**Pattern**: `account-<accountID>`

Every customer resource lives in its account's namespace. Nothing else is encoded in the namespace.

| Resource   | Namespace             | Name                | Example                                     |
| ---------- | --------------------- | ------------------- | ------------------------------------------- |
| Cluster    | `account-<accountID>` | `<cluster>`         | `account-123456789012/typeidhcp`            |
| NodePool   | `account-<accountID>` | `<cluster>.<child>` | `account-123456789012/typeidhcp.workers`    |
| Placement  | `account-<accountID>` | `<cluster>.placement` | `account-123456789012/typeidhcp.placement` |
| OidcConfig | `account-<accountID>` | configID            | `account-123456789012/a1b2c3d4-…`           |

- **Names are for humans.** Each part is a DNS label with no dots. `<cluster>` is at most 18 characters (HyperShift builds `cluster-<uid>-<cluster>`, which must fit a 63-character namespace name); `<child>` is at most 63. The dot makes child names collision-free.
- **UIDs are for machines.** A cluster's ID is its `metadata.uid`. Every stored reference uses the uid: a cluster's children carry an ownerReference and the `hyperfleet.io/cluster-uid` label.
- **On the management cluster**, a cluster's objects live in its namespace `cluster-<uid>` under plain names (`workers`, `pull-secret`).

### Access control

API Gateway authenticates AWS callers and sets `X-Amz-Account-Id` from the SigV4-verified identity. Cedar/AVP makes the access-control decision in the authz middleware before a request reaches the data layer. platform-api then only accepts the caller's own account namespace (another namespace gets 403), so namespace + name lookups are scoped to the tenant.

## Service-Scoped Namespaces

**Pattern**: Fixed namespace per resource type (e.g. `managementclusters`)

Used for backend/infrastructure resources that are not tied to any customer or tenant. These are control plane objects managed by the service itself.

| Resource          | Namespace            | Name    | Example                              |
| ----------------- | -------------------- | ------- | ------------------------------------ |
| ManagementCluster | `managementclusters` | MC name | `managementclusters/mc-us-east-2-01` |

## Choosing a Pattern

```
Is this a service/infrastructure object (no customer ownership)?
├─ Yes → fixed namespace (e.g. managementclusters)
└─ No  → account-<accountID>
         └─ Belongs to a cluster? Name it <cluster>.<child>, set an
            ownerReference and the hyperfleet.io/cluster-uid label
```
