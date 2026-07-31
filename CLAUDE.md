# CLAUDE.md

## Project Overview

ROSA Hyperfleet API — ROSA HCP regional cluster management.

Three components:
- **platform-api/** — Stateless REST gateway (SigV4 auth, Cedar/AVP authz, ZOA)
- **hyperfleet-operator/** — Kubernetes operator (Cluster, NodePool, Placement, ManagementCluster, Manifest CRDs)
- **hyperfleet-db/** — PostgreSQL-backed controller-runtime library

## Build & Test

```bash
make build              # All components
make test               # All unit tests
make lint               # golangci-lint v2 across all modules
make verify             # go.mod tidiness
make deps               # Download and tidy all modules

make build-api          # Platform API
make build-operator     # Fleet operator (manager + compactor)
make build-hyperfleet-db      # FleetDB library

make test-api           # API unit tests
make test-operator      # Operator unit tests
make test-hyperfleet-db       # FleetDB unit tests
make test-operator-int  # Operator integration tests (Postgres + DynamoDB)

make manifests          # Generate CRDs (controller-gen)
make generate           # Generate deepcopy
```

## Module Layout

```
hyperfleet-db/go.mod              ← standalone
hyperfleet-operator/api/go.mod   ← standalone (CRD types sub-module)
hyperfleet-operator/go.mod       ← requires: hyperfleet-db, hyperfleet-operator/api
platform-api/go.mod         ← requires: hyperfleet-db, hyperfleet-operator/api
```

Cross-module refs use permanent `replace` directives to sibling dirs.

## Key Conventions

- Multi-module monorepo: separate go.mod per component
- Ginkgo/Gomega for testing
- OpenAPI-first API design
- CRD types owned by hyperfleet-operator, imported by platform-api
- golangci-lint v2 with custom logcheck plugin
