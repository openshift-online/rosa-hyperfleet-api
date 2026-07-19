# V2 SDK Initiative: HyperFleet Platform API Client SDK

**Last Updated Date**: 2026-07-17

## Summary

Build a **v2 Go SDK** (`hyperfleet-sdk-go`) that exposes the same HCP cluster lifecycle operations currently served by `ocm-sdk-go` (v1), but backed by the new HyperFleet Platform API instead of the OCM API.

The v2 SDK will be validated by running existing FVT/e2e tests against both the v1 and v2 SDK, proving that consumers (rosa CLI, terraform provider) can switch backends without functional regression.

**Out of scope for this initiative**: CI/prow integration. Manual test execution is sufficient, but all identified tests must pass.

## Motivation

The HyperFleet Platform API (see [api-management.md](api-management.md)) introduces a new REST API surface generated from HyperFleet CRD types. Today, consumers like `rosa` CLI and `terraform-provider-rhcs` interact with cluster management through `ocm-sdk-go` against the OCM API (`api.openshift.com`). To complete the HyperFleet story, these consumers need a client SDK that talks to the new regional Platform API endpoints.

## V1 SDK Architecture (ocm-sdk-go)

Understanding the v1 SDK is necessary to design the v2 interface. The v1 SDK has six layers:

### Layer Model

```
┌──────────────────────────────────────────────────────────┐
│  Consumer (rosa CLI, terraform, CAPA)                    │
│  connection.ClustersMgmt().V1().Clusters().Add().Send()  │
├──────────────────────────────────────────────────────────┤
│  Connection (connection.go)                              │
│  Builder pattern, auth config, URL routing               │
│  Provides service accessors: .ClustersMgmt(), etc.       │
├──────────────────────────────────────────────────────────┤
│  Generated Resource Clients (clustersmgmt/v1/)           │
│  ClustersClient → .Add(), .List(), .Cluster(id)          │
│  ClusterClient  → .Get(), .Update(), .Delete(),          │
│                    .NodePools(), .Ingresses(), ...        │
├──────────────────────────────────────────────────────────┤
│  Generated Request/Response types                        │
│  ClustersAddRequest → .Body(cluster).SendContext(ctx)    │
│  Marshals to HTTP, sends, unmarshals response            │
├──────────────────────────────────────────────────────────┤
│  Transport Wrapper Stack                                 │
│  Auth → Retry → Metrics → Logging → Base HTTP            │
├──────────────────────────────────────────────────────────┤
│  Types & Builders (aliased from ocm-api-model/clientapi) │
│  cmv1.NewCluster().Name("x").Region(...).Build()         │
└──────────────────────────────────────────────────────────┘
```

### Generation Pipeline (v1)

The entire SDK (except `connection.go` and the transport wrappers) is **generated** from the OCM API model using a proprietary DSL:

```
ocm-api-model/model (proprietary metamodel DSL)
    ↓ ocm-api-metamodel binary
    ↓ `metamodel generate go --generators=builders-alias,clients,...`
    ↓
Generated output: clients, builders, types, JSON serialization, OpenAPI specs
```

Key facts:
- **Model source**: `github.com/openshift-online/ocm-api-model/model` (proprietary DSL, not OpenAPI)
- **Generator**: `github.com/openshift-online/ocm-api-metamodel/cmd/metamodel`
- **Output**: ~1600 generated files in `clustersmgmt/v1/` alone
- **Precedent**: The `arohcp/` package follows the exact same generated pattern at `v1alpha1`

The v2 SDK intentionally drops this proprietary DSL. Instead, it generates directly from the OpenAPI spec that the HyperFleet codegen pipeline already produces (see [V2 SDK Design](#v2-sdk-design)).

### Consumer Usage Pattern

```go
// 1. Build connection (configures auth, retries, metrics)
connection, _ := sdk.NewConnectionBuilder().
    Tokens(token).
    URL("https://api.openshift.com").
    Build()

// 2. Navigate to resource via fluent chain
collection := connection.ClustersMgmt().V1().Clusters()

// 3. Build typed payload
cluster, _ := cmv1.NewCluster().
    Name("my-cluster").
    Region(cmv1.NewCloudRegion().ID("us-east-1")).
    AWS(cmv1.NewAWS().AccountID("123456")).
    Build()

// 4. Create and send request
response, _ := collection.Add().Body(cluster).SendContext(ctx)
created := response.Body()  // typed *Cluster
```

### HCP Cluster Lifecycle Operations in V1

The minimum surface the v2 SDK must cover:

| Operation | V1 SDK call | HTTP |
|-----------|-------------|------|
| Create cluster | `Clusters().Add().Body(c)` | `POST /api/clusters_mgmt/v1/clusters` |
| Get cluster | `Clusters().Cluster(id).Get()` | `GET /api/clusters_mgmt/v1/clusters/{id}` |
| List clusters | `Clusters().List()` | `GET /api/clusters_mgmt/v1/clusters` |
| Update cluster | `Clusters().Cluster(id).Update().Body(c)` | `PATCH /api/clusters_mgmt/v1/clusters/{id}` |
| Delete cluster | `Clusters().Cluster(id).Delete()` | `DELETE /api/clusters_mgmt/v1/clusters/{id}` |
| Create node pool | `Cluster(id).NodePools().Add().Body(np)` | `POST .../clusters/{id}/node_pools` |
| Get node pool | `Cluster(id).NodePools().NodePool(npId).Get()` | `GET .../node_pools/{id}` |
| List node pools | `Cluster(id).NodePools().List()` | `GET .../clusters/{id}/node_pools` |
| Update node pool | `Cluster(id).NodePools().NodePool(npId).Update().Body(np)` | `PATCH .../node_pools/{id}` |
| Delete node pool | `Cluster(id).NodePools().NodePool(npId).Delete()` | `DELETE .../node_pools/{id}` |
| Delete protection | `Cluster(id).DeleteProtection()` | `POST .../delete_protection` |
| Hibernate | `Cluster(id).Hibernate()` | `POST .../hibernate` |
| Resume | `Cluster(id).Resume()` | `POST .../resume` |

## HyperFleet Platform API Surface

The v1 SDK exposes 12 service areas through the OCM API. The HyperFleet Platform API replaces a subset of these with a smaller, focused surface. The v2 SDK only needs to cover the HyperFleet equivalents.

| V1 OCM Service | V1 Path | HyperFleet Equivalent | Phase |
|---|---|---|---|
| ClustersMgmt (clusters) | `/api/clusters_mgmt` | **Cluster** CRD | Immediate |
| ClustersMgmt (node pools) | `/api/clusters_mgmt` | **NodePool** CRD | Immediate |
| AccountsMgmt | `/api/accounts_mgmt` | Authz: accounts, policies, attachments (see [authz.md](../authz.md)) | Future |
| Authorizations | `/api/authorizations` | Authz: check endpoint (see [authz.md](../authz.md)) | Future |
| AccessTransparency | `/api/access_transparency` | TBD — likely needed | Future |
| ServiceLogs | `/api/service_logs` | Per-cluster logs — mechanism TBD | Future |
| ServiceMgmt | `/api/service_mgmt` | TBD | TBD |
| AddonsMgmt | `/api/addons_mgmt` | No equivalent | N/A |
| AroHCP | `/api/aro_hcp` | No equivalent | N/A |
| OSDFleetMgmt | `/api/osd_fleet_mgmt` | No equivalent | N/A |
| JobQueue | `/api/job_queue` | No equivalent | N/A |
| StatusBoard | `/api/status-board` | No equivalent | N/A |
| WebRCA | `/api/web-rca` | No equivalent | N/A |

The v2 SDK architecture must accommodate adding new resource types (authz, logs, etc.) as the Platform API grows, but the initial implementation covers only Cluster and NodePool.

## V2 SDK Design

### Approach: OpenAPI-Generated SDK

The v1 SDK is generated from a proprietary metamodel DSL (`ocm-api-model`). The v2 SDK drops this DSL entirely and generates directly from the OpenAPI spec that the HyperFleet codegen pipeline already produces.

The v2 SDK exposes its OpenAPI-generated interface directly — there is no v1-compatibility adapter. Consumers migrate to the new interface (see [Interface Decision](#interface-decision)).

**Generated core**: Auto-generated from the HyperFleet Platform API OpenAPI spec (produced by the codegen pipeline described in [api-management.md](api-management.md)). This gives us typed client, request, response, and model types that match the Platform API exactly.

```
┌────────────────────────────────────────────┐
│  Consumer (rosa CLI, terraform)            │
├────────────────────────────────────────────┤
│  Generated Core (from OpenAPI)             │
│  Typed clients, models, serialization      │
│  Generated by oapi-codegen or similar      │
├────────────────────────────────────────────┤
│  Connection / Auth / Transport             │
│  AWS SigV4 auth, retry, logging            │
└────────────────────────────────────────────┘
```

### Generation from OpenAPI

The HyperFleet codegen pipeline (api-management.md) already produces an OpenAPI spec:

```
Go types (api/v1alpha1/*.go) → kube-openapi → OpenAPI spec → oapi-codegen → REST types + handlers
                                                            ↘ oapi-codegen → Client SDK (v2)
```

The same OpenAPI spec feeds both the server-side handlers and the client SDK. One source of truth, no proprietary DSL.

Candidate generators for the client SDK:

| Generator | Pros | Cons |
|-----------|------|------|
| **oapi-codegen** | Already in our pipeline, well-maintained, Go-native | Different interface style than v1 |
| **openapi-generator** | Many customization options, template-based | Heavier, Java dependency |

**Decision**: Use **oapi-codegen** for the generated core. This keeps the generation pipeline unified and avoids introducing a new toolchain.

### Dynamic Generation

Goal: the v2 SDK generation is fully automated as part of the same `make generate` pipeline that produces CRDs and OpenAPI specs. When a developer adds or modifies a field in the Go types, runs `make generate`, and gets updated CRDs, OpenAPI spec, **and** v2 SDK client code in one pass. This is achievable because the entire chain is OpenAPI-driven — no manual model definitions to maintain.

### Interface Decision

The v2 SDK adopts a **Kubernetes-style interface**, modeled on `client-go`. Consumers (rosa CLI, terraform provider) migrate to it directly; a migration guide + helper functions assist in switching. This is more work upfront than a drop-in v1-compatible layer, but cleaner long-term.

This shape is a natural fit: the HyperFleet resources are already CRD-backed types (`ObjectMeta` / `Spec` / `Status`), so the SDK simply exposes them the same way you'd work with them in Kubernetes. There are no fluent builders to generate or maintain — callers construct a typed struct literal and pass it to a verb method on a typed client.

**Resource construction** — a plain struct literal, not a builder chain:

```go
cluster := &v1alpha1.Cluster{
    ObjectMeta: metav1.ObjectMeta{
        Name:   "my-cluster",
        Labels: map[string]string{"env": "dev"},
    },
    Spec: v1alpha1.ClusterSpec{
        Region:  "us-east-1",
        Version: "4.19.0",
        AWS:     v1alpha1.AWSSpec{AccountID: "123456789012"},
    },
}
```

**Client verbs** — resource-scoped accessors exposing `Create` / `Get` / `List` / `Update` / `Patch` / `Delete`, each taking a `context.Context` and typed options, exactly like a `client-go` clientset:

```go
created, err := client.Clusters().Create(ctx, cluster, metav1.CreateOptions{})

got, err := client.Clusters().Get(ctx, "my-cluster", metav1.GetOptions{})

list, err := client.Clusters().List(ctx, metav1.ListOptions{})

np := &v1alpha1.NodePool{ /* ObjectMeta + Spec */ }
_, err = client.Clusters().NodePools("my-cluster").Create(ctx, np, metav1.CreateOptions{})
```

Contrast with the v1 fluent chain (`cmv1.NewCluster().Name("x").Region(...).Build()` then `Clusters().Add().Body(c).SendContext(ctx)`): v2 replaces the builder + send pattern with typed struct literals and verb methods.

Because the generated core already produces the typed `Spec`/`Status` models from OpenAPI, this interface is a thin, mechanical layer over them rather than a hand-maintained adapter.

## Scope

### Target Clients

| Client | Priority | Rationale |
|--------|----------|-----------|
| **rosa CLI** (`rosa-hyperfleet-cli` / `rosactl`) | P0 | Primary user-facing tool, exercises full lifecycle |
| **terraform-provider-rhcs** | P1 | Key IaC consumer, many enterprise customers depend on it |
| **CAPA** (Cluster API Provider AWS) | P2 | Lower priority for initial initiative; evaluate after rosa + terraform |

### In Scope

1. V2 SDK generated from OpenAPI with authentication (AWS SigV4) and basic HCP cluster + node pool lifecycle
2. Identify and catalog specific FVT/e2e tests that exercise cluster lifecycle through v1 SDK
3. Design the v2 SDK interface
4. Hook v2 SDK into rosa CLI and terraform provider
5. Run identified tests against v2 SDK and verify they pass
6. Evaluate and prototype dynamic generation from OpenAPI (integrated into `make generate`)

### Out of Scope

- **Tenancy and authorization** — account linking, policy CRUD, attachment CRUD, and the authorization check surface (see [authz.md](../authz.md)) are deferred to a future iteration. This first iteration covers authentication (AWS SigV4) only, not authz.
- CI/prow integration (manual testing is sufficient)
- Non-HCP cluster types (classic ROSA)
- Full OCM API surface coverage (only cluster + node pool lifecycle)
- Add-ons, ingresses, identity providers, and other sub-resources beyond cluster/nodepool (future iterations)
- Access transparency, service logs, service management (future phases)

## Test Identification

### Rosa CLI E2E Tests (rosa repo)

These tests in `rosa/tests/e2e/` exercise HCP cluster lifecycle through the CLI, which uses `ocm-sdk-go` under the hood:

| Test file | What it exercises | Labels |
|-----------|-------------------|--------|
| `hcp_cluster_test.go` | HCP cluster create/describe/edit/delete, log forwarders | `Feature.Cluster`, `Runtime.Day2` |
| `hcp_machine_pool_test.go` | HCP node pool (machine pool) CRUD | `Feature.MachinePool` |
| `hcp_external_auth_test.go` | External auth configuration | `Feature.ExternalAuth` |
| `hcp_tuning_config_test.go` | Tuning configs on HCP clusters | `Feature.TuningConfig` |
| `e2e_setup_test.go` | Cluster provisioning (precondition) | setup |
| `e2e_tear_down_test.go` | Cluster deletion (cleanup) | cleanup |

### HyperFleet CLI E2E Tests (this repo)

These tests in `test/e2e-cli/` exercise the full lifecycle through `rosactl`:

| Test area | Labels | What it exercises |
|-----------|--------|-------------------|
| VPC setup | `vpc-create`, `vpc-list` | AWS VPC creation for cluster |
| IAM setup | `iam-create`, `iam-list` | IAM roles and OIDC |
| Cluster create | `hcp-create` | `rosactl cluster create` |
| Cluster status | `cluster-status` | Poll until cluster is ready |
| Node pools | `nodepools-wait` | Node pool readiness |
| Cluster update | `hcp-patch` | `rosactl cluster patch` |
| Cleanup | `bundles-delete`, `oidc-delete`, `iam-delete`, `vpc-delete` | Full teardown |

### HyperFleet Platform API E2E Tests (this repo)

The `test/e2e-api/` tests exercise the Platform API directly:

| Test file | What it exercises |
|-----------|-------------------|
| `e2e_test.go` | Basic API connectivity |
| `authz_e2e_test.go` | Authorization policies |

### Acceptance Criteria for V2 SDK

The following tests must pass against the v2 SDK to consider the initiative complete:

1. **Rosa CLI HCP tests** (`hcp_cluster_test.go`, `hcp_machine_pool_test.go`): create HCP cluster, CRUD node pools, delete cluster — using rosa CLI backed by v2 SDK
2. **HyperFleet CLI lifecycle** (`test/e2e-cli/cluster_test.go` labels: `setup`, `create`, `monitor`, `cleanup`): full VPC → IAM → cluster → node pool → teardown cycle through rosactl
3. **Terraform basic lifecycle**: `terraform apply` + `terraform destroy` of an HCP cluster with node pools using provider backed by v2 SDK

## Work Breakdown

### Epic: V2 SDK — HyperFleet Platform API Client

#### Story 1: V2 SDK Skeleton and Authentication

Set up the `hyperfleet-sdk-go` module with:
- Connection builder with AWS SigV4 authentication (the HyperFleet API uses IAM auth, not OCM SSO tokens)
- Regional endpoint configuration (each region has its own Platform API endpoint)
- Transport wrapper stack (SigV4 signing, retry, logging)
- Basic HTTP round-tripper that talks to the HyperFleet Platform API

**Acceptance**: Can authenticate via AWS credentials and make raw HTTP calls to the Platform API.

#### Story 2: Generated Core from OpenAPI

Set up the generation pipeline:
- Take the OpenAPI spec produced by the HyperFleet codegen pipeline
- Feed it to oapi-codegen to produce typed client, models, and request/response types
- Wire the generated client into the SDK's transport layer

**Acceptance**: Generated types exist for Cluster and NodePool resources. Can create a cluster via the generated client.

#### Story 3: Integrate V2 SDK into Rosa CLI

The rosa CLI supports **both** SDKs side by side — v1 (`ocm-sdk-go`) remains the default; the v2 SDK is selected per-invocation via a flag (e.g. `--hyperfleet` or `--v2`; exact name TBD).

- Fork or branch the rosa CLI (or rosactl)
- Add the v2 SDK as an additional backend for cluster/node pool operations, alongside the existing `ocm-sdk-go` path (do not remove v1)
- Route to v1 or v2 based on the backend-selection flag
- Preserve all CLI flags and output formats identically across both backends

**Acceptance**: `rosa create cluster --hosted-cp --hyperfleet` (or `rosactl cluster create --hyperfleet`) works against the HyperFleet Platform API via the v2 SDK, while the same command without the flag continues to use the v1 SDK unchanged.

#### Story 4: Integrate V2 SDK into Terraform Provider

- Fork or branch `terraform-provider-rhcs`
- Replace cluster/node pool resource implementations to use v2 SDK
- Preserve terraform state compatibility

**Acceptance**: `terraform apply` of an HCP cluster + node pool plan works against HyperFleet Platform API.

#### Story 5: FVT Test Execution and Validation

- Run identified rosa CLI HCP e2e tests against v2 SDK-backed CLI
- Run HyperFleet CLI lifecycle tests
- Run terraform lifecycle test
- Document results and any behavior differences

**Acceptance**: All tests in the "Acceptance Criteria" section pass.

#### Story 6: Evaluate Dynamic SDK Generation Pipeline

- Prototype end-to-end flow: Go types → markers → OpenAPI spec → SDK client code
- Evaluate whether `make generate` can produce the v2 SDK alongside CRDs and OpenAPI
- Document gaps and requirements for full automation

**Acceptance**: Document with feasibility assessment, prototype code, and recommendation for full automation.

## Decisions Made

1. **Generation approach**: OpenAPI-first. Drop the proprietary OCM metamodel DSL. Generate the v2 SDK directly from the OpenAPI spec using oapi-codegen.
2. **Auth model**: AWS SigV4 (IAM auth), not OCM SSO tokens. The HyperFleet API authenticates all requests via AWS IAM credentials.
3. **Initial surface**: Cluster + NodePool only. Tenancy and authz (account linking, policies, attachments, authorization check) are deferred to a future iteration, along with access transparency, service logs, etc.
4. **Interface style**: Kubernetes-style, modeled on `client-go` — typed resource structs (`ObjectMeta`/`Spec`/`Status`) constructed as struct literals, and a typed client exposing `Create`/`Get`/`List`/`Update`/`Patch`/`Delete` verbs. No fluent builders.

## Typed vs. Dynamic Client

This initiative deliberately builds a **typed** SDK (generated Go structs, verb
methods — see [Interface Decision](#interface-decision)), not a **dynamic** one
(a generic client that fetches the schema at runtime and operates on
`unstructured` / `map[string]any`, the way `kubectl` does).

The rationale: a typed client suits consumers whose *own code names individual
fields*. `rosa` CLI (each flag maps to a field), terraform-provider-rhcs (each
HCL attribute maps to a field), and CAPA (reconcile logic maps field to field)
are all in this category — they hand-write field names in source, so compile-time
type safety is a direct benefit and the "new field ⇒ regenerate" cost is
negligible (they must add a flag/attribute/mapping to use the field anyway).

A dynamic client only pays off for pass-through consumers that forward a whole
user-supplied resource without naming its fields (e.g. a future
`rosactl apply -f cluster.yaml`), where new fields flow through with no client
release. If/when `rosactl` grows such a surface, a dynamic client may be picked
for it (TBD). It is out of scope here.

(Note: distinct from the build-time [Dynamic Generation](#dynamic-generation)
above, which is about automating codegen in `make generate` — not a runtime
dynamic client.)

## Open Questions

1. **Module location**: Separate repo (`hyperfleet-sdk-go`) or a package within `rosa-hyperfleet-api`?
2. **CAPA timing**: Should CAPA integration be part of this initiative or deferred?
3. **Type compatibility**: How much do the HyperFleet CRD types (Cluster, NodePool) diverge from the OCM API model types? The migration effort for consumers depends on this.
4. **Service logs**: Logs appear to be per-cluster in HyperFleet. How will the SDK expose them — as a sub-resource of Cluster, or as a separate top-level surface?
