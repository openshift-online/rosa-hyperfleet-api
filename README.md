# ROSA Hyperfleet API

ROSA HCP regional cluster management — platform API, operator, and backing database library.

| Directory              | Description                                             |
| ---------------------- | ------------------------------------------------------- |
| `api/`                 | CRD types and API definitions (v1alpha1)                |
| `platform-api/`        | REST gateway for API Gateway SigV4-authenticated AWS callers       |
| `hyperfleet-operator/` | Kubernetes operator (Cluster, NodePool, Placement CRDs) |
| `hyperfleet-db/`       | PostgreSQL-backed controller-runtime library            |
| `clientset/`           | Generated typed Kubernetes client for HyperFleet CRDs   |
| `hack/`                | Code generation tools and dev tooling                   |
| `test/`                | E2E tests (API, CLI, monitoring)                        |

## Quick Start

```bash
make build   # all components → bin/
make test    # all unit tests
make lint    # golangci-lint
make help    # full target list
```

## Module Layout

```
hyperfleet-db/go.mod             ← standalone
api/go.mod                       ← standalone (CRD types)
hyperfleet-operator/go.mod       ← requires: hyperfleet-db, api
platform-api/go.mod              ← requires: hyperfleet-db, api
```

## Docs

- [OpenAPI spec](api/v1alpha1/public/openapi.yaml)
- [Authorization](docs/authz.md)
- [Konflux / Quay image tags](docs/konflux/quay-image-tags.md)
