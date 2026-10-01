# Cluster-Keyed Sharding

The hyperfleet operator scales horizontally by giving each replica one slice of
the rows to watch and reconcile. Rows are keyed by their **owning cluster's
uid**, so a cluster and all its objects are always handled by the same replica.

## Why shard

Reconcile work grows with the fleet: every cluster means AWS calls,
management-cluster desires in DynamoDB, retries and rate limits. Sharding
spreads that work across replicas and limits the blast radius of one bad pod
(a crash loop or a stuck worker only affects its slice).

Keying by cluster means no cluster is ever reconciled in two places (outside a
rescale, see [Limits](#limits)), and a reconciler that looks at a cluster's
children sees them in its own cache.

Only watches and reconciles are sharded. Reads (`GetClient()`,
`GetAPIReader()`, and platform-api's `hyperfleetdb.NewClient()`) always see
every row.

Today the operator runs as one shard (`REPLICA_COUNT=1`); scaling out is a
configuration change.

## How it works

### Shard key

The hyperfleet-db cache filters its List/Watch streams with:

```sql
abs(hashtext(COALESCE(metadata->'labels'->>'hyperfleet.io/cluster-uid', uid::text))::bigint) % $mod = ANY($owned)
```

- An object that belongs to a cluster (NodePool, Placement) carries the
  cluster's uid in the `hyperfleet.io/cluster-uid` label and hashes on it.
- A Cluster has no such label and hashes on its own uid — the same string.
- Anything else (OidcConfig, Index, Manifest) hashes on its own uid.

The label is set once, at create, and never changes; this is what keeps an
object on one shard for its whole life. The namespace is not used: all of an
account's clusters share one namespace, and would otherwise share one replica.

The operator configures it through `hyperfleetdb.ShardConfig`:

```go
hyperfleetdb.ShardConfig{
    Mod:      replicaCount,
    Owned:    []int{ordinal},
    KeyLabel: v1alpha1.ClusterUIDLabel,
    UnshardedGVKs: []schema.GroupVersionKind{
        v1alpha1.SchemeGroupVersion.WithKind("ManagementCluster"),
    },
}
```

- **Mod**: the number of shards (`REPLICA_COUNT`)
- **Owned**: the shard indices this replica owns (`[ordinal]`)
- **KeyLabel**: the label holding the shard key; without it, hyperfleet-db
  falls back to hashing the namespace
- **UnshardedGVKs**: kinds every replica watches in full

### Replica assignment

The operator runs as a **StatefulSet**. Each pod owns the shard equal to its
ordinal, parsed from the hostname (`hyperfleet-operator-2` → 2), with
`REPLICA_COUNT` (set by Helm from `replicaCount`) as the modulus.

### ManagementCluster visibility

ManagementCluster is listed in `UnshardedGVKs`, so every pod sees every
management cluster and the PlacementReconciler on each pod has the full MC
registry.

## What it means for controller code

- **Carry the label.** An object whose events should trigger a cluster's
  reconcile must carry that cluster's `hyperfleet.io/cluster-uid`, or its events
  go to the wrong replica.
- **Be idempotent.** During a rescale a cluster can briefly be reconciled by
  two pods.
- **No decisions from local counts across clusters.** A replica's cache holds
  only its slice. For something like placing onto a management cluster with
  limited capacity, claim with an `Index` or use an optimistic update on a
  shared object.

See [hyperfleet-db guidelines §8](hyperfleet-db-guidelines.md#8-sharding).

## Configuration

| Variable        | Default | Description                                 |
| --------------- | ------- | ------------------------------------------- |
| `REPLICA_COUNT` | `1`     | Number of operator replicas (= shard count) |
| `POSTGRES_DSN`  | --      | PostgreSQL connection string (required)     |

```yaml
# Helm values (operator)
replicaCount: 4
```

The chart sets `REPLICA_COUNT` to match `replicaCount`. Changing it rolls the
StatefulSet, and each pod derives its shard again on startup.

## Limits

- **Rescale overlap.** While the StatefulSet rolls to a new `REPLICA_COUNT`,
  old and new pods disagree on the modulus, so a cluster can be reconciled by
  two pods (or by none) for a short time. Nothing fences this: writes to
  hyperfleet-db are optimistic (a stale write gets `Conflict`), which keeps the
  database consistent, but side effects outside it — DynamoDB desires, AWS
  calls — can run twice. Reconcilers must be idempotent for this to be safe.
- **No failover.** A shard belongs to exactly one pod. While that pod is down
  or restarting, its clusters are not reconciled; the work resumes when it is
  back. There is no takeover by another pod.
- **Cross-cluster decisions.** A replica only sees its own clusters, so any
  decision that depends on a global count must use a shared claim (see above).
- **PostgreSQL major upgrades.** `hashtext()` may change across major
  versions, reshuffling assignments once. All replicas restart during such an
  upgrade, so the effect is a burst of duplicate reconciles.

## Upgrade path: fixed buckets with lease ownership

The limits above come from tying shards to pod ordinals. The planned next step
keeps the schema as is:

1. Hash into a fixed number of buckets (e.g. `Mod = 256`) that never changes
   with the replica count.
2. Each pod owns a set of buckets by holding a Postgres **advisory lock** per
   bucket (`pg_try_advisory_lock`), released automatically when its session
   ends.
3. Pods rebalance by releasing and acquiring buckets; a bucket whose holder
   dies is free as soon as its session drops and is taken by another pod.

A bucket then has one owner at a time (no rescale overlap, provided a pod stops
reconciling a bucket before giving up its lock), a dead pod's buckets fail over
without waiting for it, and scaling no longer reshuffles every cluster.
