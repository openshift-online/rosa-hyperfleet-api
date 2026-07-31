# ROSA Hyperfleet API

ROSA HCP regional cluster management — platform API, operator, and backing database library.

| Directory | Description |
| --- | --- |
| `platform-api/` | REST gateway (SigV4 auth, Cedar/AVP authz, ZOA) |
| `hyperfleet-operator/` | Kubernetes operator (Cluster, NodePool, Placement CRDs) |
| `hyperfleet-db/` | PostgreSQL-backed controller-runtime library |
| `test/` | E2E tests (API, CLI, monitoring, ZOA) |

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
hyperfleet-operator/api/go.mod   ← standalone (CRD types)
hyperfleet-operator/go.mod       ← requires: hyperfleet-db, hyperfleet-operator/api
platform-api/go.mod              ← requires: hyperfleet-db, hyperfleet-operator/api
```

## Docs

- [OpenAPI spec](platform-api/openapi/openapi.yaml)
- [API Management](docs/api/api-management.md)
- [Rate Limiting](docs/api/rate-limit.md)
- [ZOA Trusted Actions](docs/api/zoa-endpoints.md)
- [V2 SDK Initiative](docs/api/v2-sdk-initiative.md)
- [V2 SDK ROSA Integration](docs/api/v2-sdk-rosa-integration.md)
- [Authorization](docs/authz.md)
- [E2E Lifecycle Testing](docs/e2e-lifecycle-testing.md)
- [Konflux CI/CD](docs/konflux/README.md)
- [Konflux / Quay image tags](docs/konflux/quay-image-tags.md)
- [Rate Limiting Tests](docs/testing/rate-limit.md)
