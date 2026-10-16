# NodePool Controller

## Creation Flow

```mermaid
sequenceDiagram
    participant API as Platform API
    participant PG as PostgreSQL
    participant NPC as NodePool Controller
    participant DDB as DynamoDB

    participant KA as kube-applier-aws
    participant MC as Management Cluster

    API->>PG: Create NodePool CR (clusterRef=clusterID)
    NPC->>PG: Watch detects new NodePool
    NPC->>PG: Add finalizer (hyperfleet.io/nodepool), requeue
    NPC->>PG: Get parent Cluster → get PlacementRef
    alt Cluster not found or no PlacementRef
        NPC->>PG: Set phase=WaitingForCluster, requeue
    else Cluster has Bound Placement
        NPC->>NPC: Generate NodePool manifest
        NPC->>DDB: Write ApplyDesire for the rendered management NodePool name
        NPC->>DDB: Write ReadDesire for NodePool status
        NPC->>PG: Set phase=Provisioning
        KA->>DDB: Read ApplyDesires
        KA->>MC: Apply NodePool resource
        KA->>DDB: Write status (status table)
        DDB-->>NPC: GSI poll event via EventRouter (~15s)
        NPC->>DDB: Read status (consistent read)
        NPC->>PG: Update NodePool status (conditions, phase)
    end
```

### Reconcile Steps

1. **Finalizer**: Adds `hyperfleet.io/nodepool` finalizer on first reconcile, requeues
2. **Parent Cluster lookup**: Gets Cluster CR by `spec.clusterRef` in the same namespace (account ID), waits if not found or no `PlacementRef`
3. **Manifest generation**: Generates a HyperShift NodePool manifest
4. **ApplyDesire**: Writes one ApplyDesire to `{mc}-specs-applydesires`
5. **ReadDesire**: Creates a ReadDesire for the NodePool to get status feedback (extracts "Ready" condition from the remote NodePool)
6. **Status propagation**: Reads status from DynamoDB, updates NodePool CR conditions (Synced, Ready) and phase
7. **Requeue**: Requeues every 5 minutes as a fallback; GSI two-speed polling via EventRouter provides the primary notification path (~15s latency)

### Generated Resource

The public NodePool name is `{clusterName}.{childName}`. Non-Spot NodePools retain
the child name on the management cluster. AWS Spot NodePools use
`{clusterName}-{childName}`. All NodePools live in namespace `clusters-{clusterID}`.

| Resource              | Name                           | Purpose                                   |
| --------------------- | ------------------------------ | ----------------------------------------- |
| NodePool (HyperShift) | `{childName}` (non-Spot); `{clusterName}-{childName}` (Spot) | Worker node set on the management cluster |

## Deletion Flow

When a NodePool is deleted (either standalone or as part of Cluster cascade deletion):

```mermaid
sequenceDiagram
    participant User as User/API
    participant PG as PostgreSQL
    participant NPC as NodePool Controller
    participant DDB as DynamoDB

    User->>PG: Delete NodePool CR (sets DeletionTimestamp)
    NPC->>PG: Detect DeletionTimestamp, set phase=Deleting
    NPC->>PG: Look up parent Cluster → get PlacementRef
    NPC->>DDB: Delete ApplyDesire spec for the rendered management NodePool name
    NPC->>DDB: Write DeleteDesire for the rendered management NodePool name
    NPC->>DDB: Poll status table for deletion confirmation
    NPC->>NPC: Requeue until confirmed
    NPC->>DDB: Delete ReadDesire spec
    NPC->>PG: Remove finalizer → CR deleted
```

### Deletion Steps

1. **PlacementRef lookup**: Gets the parent Cluster's PlacementRef to determine the target MC
2. **ApplyDesire cleanup**: Deletes the ApplyDesire spec from DynamoDB before writing the DeleteDesire, preventing kube-applier from racing and re-applying the resource being deleted
3. **DeleteDesire**: Writes a DeleteDesire for the NodePool resource on the MC
4. **Confirmation**: Polls `{mc}-status-deletedesires` until kube-applier-aws confirms the deletion
5. **ReadDesire cleanup**: Deletes the ReadDesire spec from DynamoDB
6. **Finalizer removal**: Removes finalizer, allowing the CR to be garbage-collected

The parent Cluster and Placement are unaffected by standalone NodePool deletion.
