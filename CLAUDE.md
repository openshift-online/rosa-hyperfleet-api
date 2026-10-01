# CLAUDE.md

## Project Overview

ROSA Hyperfleet API — ROSA HCP regional cluster management.

Three components:

- **platform-api/** — Stateless REST gateway (SigV4 auth, Cedar/AVP authz)
- **hyperfleet-operator/** — Kubernetes operator (Cluster, ClusterNetworking, Index, ManagementCluster, Manifest, NodePool, OIDCConfig, Placement, UpgradePolicies CRDs)
- **hyperfleet-db/** — PostgreSQL-backed controller-runtime library

## Build & Test

```bash
make build              # All components
make test               # All unit tests
make lint               # golangci-lint v2 across all modules
make verify-mod         # go.mod tidiness
make deps               # Download and tidy all modules

make build-api          # Platform API
make build-operator     # Fleet operator (manager + compactor)
make build-hyperfleet-db      # FleetDB library

make test-api           # API unit tests
make test-operator      # Operator unit tests
make test-hyperfleet-db       # FleetDB unit tests
make test-operator-int  # Operator integration tests (Postgres + DynamoDB)

make generate           # Run all code generators (passthrough, deepcopy, registry, CRDs, conversion, clientset, openapi)
make verify             # Fail if any generated output is out of date

make manifests          # Generate CRDs (controller-gen + CEL strip)
make generate-deepcopy  # Generate deepcopy methods only
make codegen            # Full codegen pipeline (passthrough + registry + verify compile)
make generate-clientset # Regenerate typed clientset from CRD types
make generate-openapi   # Regenerate OpenAPI spec from CRD types

make verify-codegen     # Verify codegen output is up to date
make verify-clientset   # Verify clientset matches committed files
make verify-openapi     # Verify OpenAPI spec is up to date
make verify-mod         # Verify go.mod tidiness

make test-unit          # All unit tests (api, operator, codegen, clientset)
make test-integration   # Integration tests (fleetdb, operator)
make test-api-codegen   # Codegen tool tests
make test-clientset     # Clientset tests
```

## Module Layout

```
hyperfleet-db/go.mod                    ← standalone
api/go.mod                              ← standalone (CRD types, v1alpha1)
clientset/go.mod                        ← generated typed K8s client for HyperFleet CRDs
hyperfleet-operator/go.mod              ← requires: fleetdb, api
platform-api/go.mod                     ← requires: fleetdb, api
hack/api-codegen/go.mod                 ← codegen tools (openapi-gen, crd-variants, conversion-gen)
hack/clientset/cmd/bridge-gen/go.mod      ← bridge and platform generation for clientset
hack/tools/go.mod                       ← dev tooling dependencies
```

Cross-module refs use permanent `replace` directives to sibling dirs.

## Key Conventions

- Multi-module monorepo: separate go.mod per component
- Ginkgo/Gomega for testing
- OpenAPI-first API design
- CRD types in standalone `api/` module, imported by hyperfleet-operator and platform-api
- golangci-lint v2 with custom logcheck plugin

## hyperfleet-db Rules

Full guide: `hyperfleet-operator/docs/hyperfleet-db-guidelines.md`. In short:

1. **Namespace is the account** (`account-<id>`); nothing else is encoded in it.
2. **Name is for humans**: client-chosen, never changed. A cluster's child is `<cluster>.<child>`, the only name built from other names.
3. **UID is for machines**: minted by the database; every stored reference uses the uid, never the name.
4. **Owned objects carry a controller ownerReference and the `hyperfleet.io/cluster-uid` label**, set once at create. Claims (`Index`, `OidcConfig`) carry their holder's uid in a label (`hyperfleet.io/owner-uid`, `hyperfleet.io/claimed-by-cluster-uid`).
5. **The garbage collector deletes objects whose owner is gone**; owners' finalizers wait until nothing carries their uid. Register new owned kinds in `ClusterOwnedKinds()`.
6. **Uniqueness comes from the name, or from an `Index` claim**; keep the data on the owner.
7. **Business rules live in code**: no new DB constraints or special query paths. Validate in platform-api (CRD markers aren't enforced).
8. **Reconcilers are idempotent**: no cross-object transactions; retry on conflict (`Get` + `Update`, no `Patch`); no decisions from local counts across clusters (the cache is sharded by cluster uid).
