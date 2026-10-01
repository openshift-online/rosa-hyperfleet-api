# Cluster Controller

## Naming Convention

The **cluster ID** is `metadata.name` on the Cluster CR. The owning AWS account is `metadata.namespace` (the account ID, e.g., `123456789012`). All related resources (NodePool, Placement) must be in the same namespace as their parent Cluster.

Cluster ID uniqueness is enforced by PostgreSQL's unique constraint on `(gvk, namespace, name)`.

## Creation Flow

```mermaid
sequenceDiagram
    participant API as Platform API
    participant PG as PostgreSQL
    participant PC as Placement Controller
    participant CC as Cluster Controller
    participant DDB as DynamoDB
    participant KA as kube-applier-aws
    participant MC as Management Cluster

    API->>PG: Create Cluster CR
    CC->>PG: Watch detects new Cluster
    CC->>PG: Add finalizer (hyperfleet.io/cluster), requeue
    CC->>PG: Set phase=WaitingForPlacement, requeue
    PC->>PG: Watch detects Cluster without Placement
    PC->>PG: Create Placement CR (mc=mc01, phase=Bound)
    CC->>PG: Detect Bound Placement
    CC->>CC: Generate 7 manifests
    CC->>DDB: Write 7 ApplyDesires (specs table)
    CC->>DDB: Write 1 ReadDesire for HostedCluster status
    CC->>PG: Set phase=Provisioning
    KA->>DDB: Read ApplyDesires (specs table)
    KA->>MC: Apply resources (Namespace, HostedCluster, etc.)
    KA->>DDB: Write status (status table)
    DDB-->>CC: GSI poll event via EventRouter (~15s)
    CC->>DDB: Read status (consistent read)
    CC->>PG: Update Cluster status (conditions, endpoint, version)
```

### Reconcile Steps

1. **Finalizer**: Adds `hyperfleet.io/cluster` finalizer on first reconcile, requeues
2. **Placement lookup**: Waits for a Bound Placement (created by Placement controller)
3. **Manifest generation**: Generates 7 Kubernetes manifests (see below)
4. **ApplyDesires**: Writes one ApplyDesire per manifest to `{mc}-specs-applydesires`
5. **ReadDesire**: Creates a ReadDesire for the HostedCluster to get status feedback
6. **Status propagation**: Reads status from DynamoDB, propagates conditions/endpoint/version to Cluster CR
7. **Requeue**: Requeues every 5 minutes as a fallback; GSI two-speed polling via EventRouter provides the primary notification path (~15s latency)

### Generated Resources

The controller generates 7 Kubernetes manifests, all scoped to the cluster's MC
namespace `cluster-{uid}` (the Cluster's database-minted `metadata.uid`):

| #   | Resource       | Name                   | Purpose                                                                |
| --- | -------------- | ---------------------- | ---------------------------------------------------------------------- |
| 1   | Namespace      | `cluster-{uid}`        | Isolation boundary for all cluster resources                           |
| 2   | ConfigMap      | `cluster-config`       | Cluster ID and display name                                            |
| 3   | ConfigMap      | `aws-iam-auth-config`  | AWS IAM authenticator mapping (creator ARN → system:masters)           |
| 4   | ExternalSecret | `pull-secret`          | Pulls container registry credentials from AWS Parameter Store          |
| 5   | Certificate    | `api-serving-cert`     | TLS cert for `*.{name}.{prefix}.{shard}.{baseDomain}` via cert-manager |
| 6   | HostedCluster  | `{clusterName}`        | HyperShift control plane definition                                    |
| 7   | Secret         | `ssh-key`              | SSH key placeholder                                                    |

### DNS reservation

Before rendering, the controller reserves a base domain and stores it in
`Cluster.Status.BaseDomain`. It is assembled as `{prefix}.{shard}.{baseDomain}`
(e.g. `f7a3.0.rosa.example.com`), where `prefix` is a random hex value, `shard`
is the DNS zone shard (default `0`), and `baseDomain` is the operator's
`--base-domain`. Rendered hostnames layer the cluster name on top, e.g.
`api.{clusterName}.{prefix}.{shard}.{baseDomain}`.

Uniqueness is per shard, not per cluster name: only the `prefix` must be unique
within its shard. It is DB-enforced with a single `Index` claim — `reserveDNS`
creates an `Index` named `{prefix}` in the shard namespace
`dns-shard-{shard}-reservations`, labeled `hyperfleet.io/owner-uid={cluster uid}`.
hyperfleet-db's `(gvk, namespace, name)` primary key rejects a duplicate as
`AlreadyExists`; an existing Index carrying this cluster's uid means the claim is
already held (retries are safe), any other uid means the prefix is taken and the
controller retries with a fresh one. The Index is only the lock: the base domain
lives on the cluster. If the status write is lost, the next reconcile finds the
held Index by its owner-uid label. The cluster's finalizer releases every Index
carrying its uid, and never one carrying someone else's. See the
[hyperfleet-db guidelines](hyperfleet-db-guidelines.md#7-uniqueness).

## Deletion Flow

Deletion follows a strict ordering: NodePools first, then HostedCluster (so HyperShift can clean up workers and load balancers), then the namespace (cascading remaining resources). ApplyDesire specs are always removed before DeleteDesires are written to prevent kube-applier from racing and re-applying resources being deleted.

```mermaid
sequenceDiagram
    participant User as User/API
    participant PG as PostgreSQL
    participant CC as Cluster Controller
    participant NPC as NodePool Controller
    participant DDB as DynamoDB

    User->>PG: Delete Cluster CR (sets DeletionTimestamp)
    CC->>PG: Detect DeletionTimestamp, set phase=Deleting

    Note over CC: Step 1 — Garbage collector deletes the cluster's children
    participant GC as Garbage Collector
    GC->>PG: Delete NodePools and Placement whose owner is being deleted
    NPC->>DDB: NodePool finalizer cleans up ApplyDesire, writes DeleteDesire
    NPC->>PG: Remove NodePool finalizer → CR deleted
    CC->>CC: Requeue until nothing carries hyperfleet.io/cluster-uid={uid}

    Note over CC: Step 2 — Clean up ApplyDesires
    CC->>DDB: Delete all 7 ApplyDesire specs from specs-applydesires

    Note over CC: Step 3 — Delete HostedCluster on MC
    CC->>DDB: Write DeleteDesire for HostedCluster
    CC->>DDB: Poll status table for confirmation
    CC->>CC: Requeue until confirmed

    Note over CC: Step 4 — Delete namespace on MC
    CC->>DDB: Write DeleteDesire for namespace cluster-{uid}
    CC->>DDB: Poll status table for confirmation
    CC->>CC: Requeue until confirmed
    CC->>DDB: Delete ReadDesire spec for HostedCluster

    Note over CC: Step 5 — Remove finalizer
    CC->>PG: Remove finalizer → Cluster CR deleted
```

### Deletion Steps

1. **Children**: The cluster does not delete its children. The garbage collector deletes every object whose controller owner is missing, has a different uid, or is being deleted — here the cluster's NodePools and Placement. The finalizer waits until no NodePool or Placement carries the `hyperfleet.io/cluster-uid` label; a child inserted mid-deletion is collected the same way. The MC is read from `status.placementRef`, which the controller records before rendering anything. Each NodePool has its own finalizer that cleans up its ApplyDesire and writes a DeleteDesire before clearing. Requeues until all NodePools are fully gone.
2. **ApplyDesire cleanup**: Deletes all 7 ApplyDesire specs from DynamoDB. This must happen before writing DeleteDesires to prevent kube-applier from racing and re-applying resources that are being deleted.
3. **HostedCluster DeleteDesire**: Writes a DeleteDesire for the HostedCluster resource and waits for confirmation. Deleting the HostedCluster first allows HyperShift to clean up worker nodes and load balancers before the namespace is removed.
4. **Namespace DeleteDesire**: Writes a DeleteDesire for `cluster-{uid}`, cascading all remaining MC resources. After confirmation, deletes the HostedCluster ReadDesire spec from DynamoDB.
5. **Finalizer removal**: Removes the `hyperfleet.io/cluster` finalizer, allowing Kubernetes to complete the CR deletion.
