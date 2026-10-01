# hyperfleet-db Development Guidelines

Rules for any code that stores or reconciles HyperFleet resources through hyperfleet-db:
platform-api, the operator, and anything new. New and reviewed code follows them.

> This describes the **target** model. Items marked _(PR n)_ don't exist yet; the
> [Implementation plan](#implementation-plan) builds them.

## The rules

1. **Namespace is the account.** `account-<id>`. Nothing else is encoded in it.
   Internal namespaces (e.g. `Index` scopes) are operator-only; clients can't reach
   them.
2. **Name is for humans.** Chosen by the client and never changed. A child of a
   cluster is named `<cluster>.<child>`.
3. **UID is for machines.** Minted by the database. Every stored reference uses the
   uid, never the name.
4. **Owned objects carry an ownerReference and the `hyperfleet.io/cluster-uid`
   label.** Claims (`Index`, `OidcConfig`) carry their holder's uid in a label instead.
5. **A garbage collector deletes objects whose owner is gone, and owners wait for it.**
6. **Uniqueness comes from the name, or from an `Index` claim.**
7. **Business rules live in code, not in the database.** No new constraints and no
   special query paths.
8. **Reconcilers are idempotent.** There are no cross-object transactions, so the
   system converges through retries.

---

## 1. What hyperfleet-db is

A controller-runtime client and cache backed by Postgres. It looks like Kubernetes, but
it is **not an apiserver**. Know the differences:

| You might expect…                     | In hyperfleet-db                                                                 |
| ------------------------------------- | -------------------------------------------------------------------------------- |
| admission / CRD schema validation     | **none**. CEL and OpenAPI markers enforce nothing; validate in platform-api      |
| a cached, possibly stale `Get`/`List` | **always a Postgres query**. Never stale, but not free: don't call it in loops   |
| `Patch`, `DeleteAllOf`, `GenerateName`, `DryRun` | **not supported**. Use `Get` + `Update`, and set names explicitly     |
| Kubernetes garbage collection         | **not built in**. Use ours ([§6](#6-ownership-and-cleanup)) _(PR 3)_             |
| unique fields beyond the name         | **only the primary key** `(kind, namespace, name)`. Use an `Index` ([§7](#7-uniqueness)) |
| transactions across objects           | **none**. Each write is one row; converge in reconcile                           |

What works as usual:

- **Updates are optimistic.** A stale `resourceVersion` gets `Conflict`: get the object
  again and retry (`retry.RetryOnConflict`). Status is a separate write
  (`Status().Update`).
- **Writes that change nothing are skipped.** They don't bump the version or wake
  watchers, so writing desired state every reconcile is cheap.
- **Label selectors** are backed by SQL _(PR 1)_. **Field selectors** on `metadata.name`,
  `metadata.namespace`, and `metadata.uid` _(PR 1)_ hit columns. Selectors on `spec`/`status`
  paths scan JSON, so keep them off hot paths.
- **Delete** sets a deletion timestamp while finalizers remain. After that the row is a
  tombstone, and creating the same name again makes a **new object with a new uid**.

## 2. Identity

```yaml
kind: Cluster
metadata:
  namespace: account-123456789012
  name: typeidhcp
  uid: 4610b27e-8f77-4f4c-9661-c11b42e04dec   # the cluster ID
---
kind: NodePool
metadata:
  namespace: account-123456789012
  name: typeidhcp.workers
  uid: 9c2f5e1a-1b2c-4d3e-8f90-a1b2c3d4e5f6
  labels:
    hyperfleet.io/cluster-uid: 4610b27e-8f77-4f4c-9661-c11b42e04dec
  ownerReferences:
    - apiVersion: hyperfleet.io/v1alpha1
      kind: Cluster
      name: typeidhcp
      uid: 4610b27e-8f77-4f4c-9661-c11b42e04dec
      controller: true
```

| Field                       | Set by                  | Rule                                                          |
| --------------------------- | ----------------------- | ------------------------------------------------------------- |
| `namespace`                 | client                  | must be `account-<caller's account>`, else 403                |
| `name`                      | client                  | see [Names](#3-names); unique per kind and namespace; immutable |
| `uid`                       | database                | client value ignored; immutable                               |
| `ownerReferences`           | platform-api / operator | client value ignored; immutable                               |
| `hyperfleet.io/*` labels    | platform-api / operator | client value ignored; kept on every update                    |
| `annotations`               | client                  | free-form; stored as-is; key format and size are validated    |

The caller's account comes from API Gateway, which sets `X-Amz-Account-Id` from the
SigV4-verified identity. It never comes from the request body. platform-api stores
objects unchanged, but it validates them first.

## 3. Names

| Object                           | Name format         | Example             |
| -------------------------------- | ------------------- | ------------------- |
| Cluster                          | `<cluster>`         | `typeidhcp`         |
| Child of a cluster (NodePool, …) | `<cluster>.<child>` | `typeidhcp.workers` |

- **Each part is a DNS label:** lowercase letters, digits, and `-`, starting and ending
  with a letter or digit, and **no dots**. `<cluster>` is at most 18 characters,
  because HyperShift builds `cluster-<uid>-<cluster>` from it (longer names can come
  later as an alias). `<child>` is at most 63.
- **The dot makes child names collision-free.** Neither part contains a dot, so
  `cluster-test.np` and `cluster.test-np` stay distinct. With `-` as the joiner, both
  would be `cluster-test-np`.
- **The client sends the full name.** platform-api checks it, but never rewrites it. The
  SDK has a helper that builds it.
- **On the management cluster**, a cluster's objects live in its namespace
  `cluster-<uid>` under plain names (`workers`, `pull-secret`).
- `<cluster>.<child>` is the **only** name ever built from other names.

## 4. References and lookups

Names can be reused after a delete, but uids can't. Anything that points at another
object stores its **uid**.

| To…                                | Use                                                                   |
| ---------------------------------- | --------------------------------------------------------------------- |
| record an object's owner           | an `ownerReference` **and** the `hyperfleet.io/cluster-uid` label       |
| list a cluster's objects           | label selector on `hyperfleet.io/cluster-uid` (not `InNamespace`)     |
| react to changes in owned objects  | `Owns(&NodePool{})`                                                   |
| hold an `Index` claim              | label `hyperfleet.io/owner-uid` ([§7](#7-uniqueness))                 |
| claim an `OidcConfig`              | label `hyperfleet.io/claimed-by-cluster-uid` (mutable: released and re-taken) |

The ownerReference and the label are set once, at create, from the same parent, so they
can't drift apart. Both are needed because an ownerReference can't be used in a label
selector or the shard key.

**Lookups:**

- **By name:** `GET /clusters/typeidhcp`.
- **By uid:** `GET /clusters?fieldSelector=metadata.uid=<uid>`.
- **Holding both** (e.g. an ownerReference): get by name, then compare the uid. A
  different uid means the name was reused, and the object you meant is gone.
- **Never key cached state by name alone.** A delete followed by a create can reach a
  watcher as a single "update" whose uid changed.

## 5. Lifecycle

- **Create:** the client sends namespace and name. The database mints the uid and
  returns it. A duplicate name gets 409.
- **Create a child:** platform-api gets the parent (404 if it is missing, 409 if it is
  being deleted), sets the ownerReference and label, then inserts.
- **Update:** spec and annotations only. Identity, ownerReferences, and labels never
  change.
- **Delete:** by name. The garbage collector removes children, and the owner's finalizer
  finishes once they are gone.

## 6. Ownership and cleanup

| Piece                  | Job                                                                                 |
| ---------------------- | ----------------------------------------------------------------------------------- |
| **Creator**            | sets the ownerReference and label at create                                         |
| **Garbage collector**  | deletes any object whose owner is missing, has a different uid, or is being deleted |
| **Owner's finalizer**  | waits until nothing carries its uid label, releases its claims, then lets go        |

- **The garbage collector** is one generic controller. It watches every owned kind and
  their owners, gets the owner named in the ownerReference, and compares uids. Deleting
  only sets the child's deletion timestamp; the child's own finalizer still does its
  teardown.
- **The owner's finalizer** also releases what it holds in other namespaces (`Index`
  claims, the `OidcConfig` claim), since ownerReferences can't cross namespaces.
- **A child's finalizer must cope with a missing owner.** Normally the owner waits for
  its children, but an orphan's owner is already gone. Clean up what you can and
  finish; don't block forever waiting on a parent that won't come back.

**Why no locks are needed.** A child can be inserted just after its parent's finalizer
found no children. The garbage collector then sees an owned object whose owner is gone
and deletes it. If the insert lands first, the finalizer sees the child and waits.
Either way, nothing is left behind.

## 7. Uniqueness

| Unique within…          | Tool                   | Example                                                        |
| ----------------------- | ---------------------- | -------------------------------------------------------------- |
| one kind in one account | the name (primary key) | only one cluster `typeidhcp` in the account                    |
| any wider scope         | an `Index` claim       | DNS prefix `f7a3` in shard 0; an OIDC issuer URL in the region |

An `Index` is an empty object. Its namespace is the scope and its name is the value.
The primary key allows one per (scope, value), so **creating it is the claim**.

```yaml
kind: Index
metadata:
  namespace: dns-shard-0-reservations   # the scope
  name: f7a3                 # the claimed value
  labels:
    hyperfleet.io/owner-uid: 4610b27e-8f77-4f4c-9661-c11b42e04dec
spec: {}
```

- **Claim:** create it. On `AlreadyExists`, get it. If `owner-uid` is yours, you already
  hold it (retries are safe). Otherwise it's taken: pick another value or report a
  conflict.
- **Values that aren't valid names** (e.g. URLs): normalize, then hash
  (`IssuerURLIndexName`).
- **Keep the data on the owner** (e.g. `cluster.status.baseDomain`). The Index is only
  the lock; don't add a second object.
- **Release:** the owner's finalizer deletes Indexes carrying its uid, and never ones
  carrying someone else's.

**Why Index.** It relies on the one guarantee fleetdb already has, so a new uniqueness
rule needs no migration. A per-rule DB constraint would put business rules in the
schema. Checking in platform-api (list, then insert) is not atomic. A generic
unique-keys table written in the same transaction as the owner would release keys
automatically, but it adds new write behavior to fleetdb; revisit it only if
uniqueness rules multiply.

## 8. Sharding

Each operator replica reconciles one slice of the rows, keyed by the owning cluster's
uid (or the row's own uid if it has no owner):

```sql
abs(hashtext(COALESCE(metadata->'labels'->>'hyperfleet.io/cluster-uid', uid::text))::bigint) % $mod = ANY($owned)
```

**Why.** Reconcile work (AWS and management-cluster calls, retries, rate limits) grows
with the fleet. Sharding spreads it across replicas and limits the blast radius of one
bad pod. Keying by cluster keeps a cluster and all its objects on one replica, so no
cluster is ever reconciled in two places. Only watches and reconciles are sharded;
reads always see every row. Today it runs as one shard, and scaling out is a config
change (`REPLICA_COUNT`). Details, limits, and the path to lease-based failover are in
[sharding.md](sharding.md).

**What it means for your code:**

- **Carry the label.** An object whose watch triggers a cluster reconcile must carry
  that cluster's `hyperfleet.io/cluster-uid`. Otherwise its events go to the wrong
  replica.
- **Be idempotent.** While the replica count changes, a cluster can briefly be
  reconciled by two pods.
- **No decisions from local counts across clusters.** For something like placing onto
  a management cluster with limited capacity, claim with an `Index` or use an
  optimistic update on a shared object.

## Review checklist

- [ ] References another object? It stores the **uid**.
- [ ] Belongs to a cluster? **ownerReference + `hyperfleet.io/cluster-uid`**.
- [ ] New owned kind? **Registered with the garbage collector**.
- [ ] Lists a cluster's objects? The **label**, not `InNamespace`.
- [ ] Builds a name? Only `<cluster>.<child>`.
- [ ] Per-cluster object on the management cluster? In **`cluster-<uid>`**, plain name.
- [ ] Something unique beyond the name? One **`Index`**, value stored on the owner.
- [ ] Validation? In **platform-api**; CRD markers aren't enforced.
- [ ] Update? **Retries on conflict**; no `Patch`.
- [ ] Reconciler safe to run twice? No **local counts** for cross-cluster decisions.
- [ ] New database constraint or special query? Use code instead.

---

## Implementation plan

Each PR keeps `main` green and updates the docs it affects. No migrations and no new
database constraints.

### PR 1 — fleetdb: label and uid selectors in SQL

- Filter label selectors in SQL (`metadata->'labels' @> …`) with a GIN index on labels.
  Today they are filtered in Go (`pgclient.go`, `pgcache.go`).
- Map the field selector `metadata.uid` to the `uid` column (`fieldselector.go`). Today
  it falls through to JSON.
- Tests: create returns a uid, a client-sent uid is ignored, recreating a deleted name
  gives a new uid, and label selectors filter in SQL.

### PR 2 — Identity switch (all modules at once)

- **api:** name validation markers and doc comments.
- **platform-api:**
  - Delete the namespace computation, `generateID()`, and the uid overrides in
    `convert.go`.
  - Validate the namespace and names; routes take the name.
  - Child create: get the parent, then set the ownerReference and label.
  - Ignore client-sent ownerReferences and labels; keep the stored ones on update.
  - `clusterId` filters on the uid label.
  - Update `openapi.yaml`.
- **operator:**
  - The management-cluster namespace becomes `cluster-<uid>`.
  - Replace `InNamespace` with the uid label.
  - `Placement` gets an ownerReference and the label; use `Owns()` in place of its map
    functions.
  - DNS: claim a single `Index` (owner-uid = cluster uid) and keep the base domain in
    `cluster.status.baseDomain`. Delete `DNSReservation`, which duplicates the Index and
    is created in a second, non-atomic step.
  - OIDC issuer `Index`: owner-uid = the OidcConfig's uid. The `OidcConfig` claim uses
    `hyperfleet.io/claimed-by-cluster-uid`. This replaces the name-based `hyperfleet.io/cluster-namespace`
    and OidcConfig-name labels, which are open to ABA.
  - Cluster finalizer: still deletes children by label (moves to the garbage collector
    in PR 3).
  - Render only the child part of the NodePool name.
- **clientset:** stop sending the namespace as `X-Amz-Account-Id` (`sigv4.go`) and
  remove `adaptNodePoolScope` (`bridge.go`).
- **Tests:** two clusters in one account can both have `workers`, and deleting one
  leaves the other's objects alone; a recreated cluster inherits nothing.

### PR 3 — Garbage collector

- One generic controller, registered for each owned kind (`NodePool`, `Placement`), that
  also watches owners.
- Cluster finalizer: stop deleting children; wait until the label list is empty.
- Tests: a child inserted during its cluster's deletion is cleaned up; a child pointing
  at an old uid under a reused name is deleted.

### PR 4 — Public labels and SDK

- Return labels and ownerReferences on reads; support `labelSelector` on list.
- SDK helper for `<cluster>.<child>`. SDK waiters get by name and treat a uid change as
  "gone".

### PR 5 — Sharding

- Switch `hyperfleet-db/internal/reader/shard.go` to the expression above.
- Rewrite `sharding.md`:
  - It still describes namespace hashing, and it credits "fenced writes" (which don't
    exist) with making the rescale overlap safe.
  - Add the rationale and the known limits: the rescale overlap, no failover while a
    pod is down, and cross-cluster decisions.
  - Add the upgrade path: fixed buckets (e.g. `Mod = 256`) owned through Postgres
    advisory locks, which gives failover and no overlap without a schema change.
- Add these rules to `CLAUDE.md`.
- Tests: a cluster and all its objects share a replica.
