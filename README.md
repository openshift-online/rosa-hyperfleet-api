# ROSA Hyperfleet API

ROSA HCP regional cluster management — platform API, operator, and backing database library.

| Directory              | Description                                             |
| ---------------------- | ------------------------------------------------------- |
| `api/`                 | CRD types and API definitions (v1alpha1)                |
| `platform-api/`        | REST gateway (SigV4 auth, Cedar/AVP authz, ZOA)         |
| `hyperfleet-operator/` | Kubernetes operator (Cluster, NodePool, Placement CRDs) |
| `hyperfleet-db/`       | PostgreSQL-backed controller-runtime library            |
| `clientset/`           | Generated typed Kubernetes client for HyperFleet CRDs   |
| `hack/`                | Code generation tools and dev tooling                   |
| `test/`                | E2E tests (API, CLI, monitoring, ZOA)                   |

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

### API

- [OpenAPI spec](api/v1alpha1/public/openapi.yaml)
- [API Management](docs/api/api-management.md)
- [Namespace Conventions](docs/api/namespace-conventions.md)
- [Passthrough Design](docs/api/passthrough-design.md)
- [Public Types Migration](docs/api/public-types-migration.md)
- [Rate Limiting](docs/api/rate-limit.md)
- [V2 SDK Initiative](docs/api/v2-sdk-initiative.md)
- [V2 SDK ROSA Integration](docs/api/v2-sdk-rosa-integration.md)
- [ZOA Trusted Actions](docs/api/zoa-endpoints.md)
- [Authorization](docs/authz.md)

### Operator

- [Architecture](hyperfleet-operator/docs/architecture.md)
- [Cluster Controller](hyperfleet-operator/docs/cluster-controller.md)
- [NodePool Controller](hyperfleet-operator/docs/nodepool-controller.md)
- [Placement Controller](hyperfleet-operator/docs/placement-controller.md)
- [Manifest Controller](hyperfleet-operator/docs/manifest-controller.md)
- [Sharding](hyperfleet-operator/docs/sharding.md)
- [DynamoDB Strategy](hyperfleet-operator/docs/dynamodb-strategy.md)
- [Quickstart](hyperfleet-operator/docs/quickstart.md)

### Database

- [Design](hyperfleet-db/docs/DESIGN.md)
- [Architecture Comparison](hyperfleet-db/docs/ARCHITECTURE_COMPARISON.md)
- [Compatibility](hyperfleet-db/docs/COMPATIBILITY.md)
- [Metrics](hyperfleet-db/docs/METRICS.md)
- [Walkthrough](hyperfleet-db/docs/WALKTHROUGH.md)

### Other

- [Clientset Architecture](clientset/docs/architecture.md)
- [E2E Lifecycle Testing](docs/e2e-lifecycle-testing.md)
- [Konflux / Quay image tags](docs/konflux/quay-image-tags.md)
