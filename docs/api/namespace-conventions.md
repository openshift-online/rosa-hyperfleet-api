# Namespace Conventions

**Last Updated Date**: 2026-08-19

## Summary

HyperFleet CRD resources use Kubernetes-style namespaces (mapped to pgruntime) as the primary isolation and grouping boundary. There are two namespace patterns, chosen by what the resource is scoped to.

## Resource-Scoped Namespaces

**Pattern**: `cluster-<uuid>`

Used for resources that belong to a specific cluster. The namespace groups all resources for that cluster together, mirroring how Kubernetes namespaces naturally scope related objects.

| Resource | Namespace        | Name                | Example                         |
| -------- | ---------------- | ------------------- | ------------------------------- |
| Cluster  | `cluster-<uuid>` | Human-readable name | `cluster-4610b27e-…/my-cluster` |
| NodePool | `cluster-<uuid>` | Human-readable name | `cluster-4610b27e-…/my-pool`    |
| Manifest | `cluster-<uuid>` | Manifest name       | `cluster-4610b27e-…/kubeconfig` |

Cluster-scoped resources carry a `hyperfleet.io/account-id` label for account-level filtering (e.g. listing all clusters for an account).

**Name length constraint**: HyperShift creates a control plane namespace as `cluster-<uuid>-<name>`, which must fit within the 63-character Kubernetes namespace limit. This caps the human-readable cluster name at 18 characters.

## Service-Scoped Namespaces

**Pattern**: Fixed namespace per resource type (e.g. `managementclusters`)

Used for backend/infrastructure resources that are not tied to any customer or tenant. These are control plane objects managed by the service itself.

| Resource          | Namespace            | Name    | Example                              |
| ----------------- | -------------------- | ------- | ------------------------------------ |
| ManagementCluster | `managementclusters` | MC name | `managementclusters/mc-us-east-2-01` |

## Account-Scoped Namespaces

**Pattern**: `account-<accountID>`

Used for tenant-level resources that are not tied to a specific cluster. The namespace IS the tenancy boundary: all resources for a given account live in the same namespace.

| Resource   | Namespace             | Name     | Example                           |
| ---------- | --------------------- | -------- | --------------------------------- |
| OidcConfig | `account-<accountID>` | configID | `account-123456789012/a1b2c3d4-…` |

### Why account-scoped?

Resources like OidcConfig are shared across clusters within a tenant. Giving each one its own namespace (e.g. `oidc-<configID>`) would be wasteful and miss the natural grouping. Using the account as the namespace:

- **Matches Kubernetes conventions**: namespaces are the standard multi-tenancy boundary.
- **Simplifies List**: scoping to the namespace returns exactly the tenant's resources, no label filter needed.
- **Simplifies Get/Delete**: direct lookup by namespace + name, no list-and-filter.
- **Scales naturally**: future tenant-scoped resources (e.g. identity providers, billing configs) use the same namespace.

### Access control

API Gateway authenticates AWS callers, and the Platform API uses the resulting
identity for request handling. Account labels and namespaces scope existing
data lookups.

## Choosing a Pattern

```
Is this a service/infrastructure object (no customer ownership)?
├─ Yes → fixed namespace (e.g. managementclusters)
└─ No
    └─ Is it specific to a single cluster?
       ├─ Yes → cluster-<uuid>  (label the account)
       └─ No  → account-<accountID>
```
