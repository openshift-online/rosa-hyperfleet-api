# E2E Tests

End-to-end integration and functional tests for the Hyperfleet Platform API.

## Prerequisites

```bash
go install github.com/onsi/ginkgo/v2/ginkgo@latest
```

## Running Tests

```bash
# Run with ginkgo
cd test/e2e-api
ginkgo -v

# Run with go test
cd test/e2e-api
go test -v
```

## Environment Variables

- `E2E_BASE_URL`: Base URL of the API server (default: `http://localhost:8000`)
- `E2E_TOKEN`: Authentication token for API requests
- `E2E_SERVICE_OPERATOR_PROFILE`: Required AWS profile for ManagementCluster operations. Other resource operations retain the default AWS profile.
- `E2E_RHOBS_API_URL`: RHOBS API Gateway URL for observability tests (optional — tests are skipped if unset)

## Local Cedar HTTP / PostgreSQL harness

The existing `TestAuthzHTTP` harness launches the actual local API binary and a
runner-owned disposable PostgreSQL 16 container. It clears all AWS credential
sources, disables rate limiting, binds listeners and PostgreSQL to loopback, and
never uses a remote API/database. Podman is required. An inherited `PGCTL_DSN`
is rejected. Container cleanup uses inspected ownership labels and IDs.

```bash
# Build/artifacts outside the checkout; no staging or cloud operations.
(cd platform-api && go build -o /tmp/rosa-hyperfleet-api-authz ./cmd)
AUTHZ_HTTP_API_BINARY=/tmp/rosa-hyperfleet-api-authz \
  bash scripts/test-e2e-authz.sh /tmp/rosa-hyperfleet-authz-results
```

The extension covers every supported non-label operation, Cluster PUT/PATCH,
NodePool PUT base plus specialized actions, list/Describe filtering before totals
and pagination, late evaluator failures without partial success, duplicate pool
names with optional clusterId, candidate ownership/labels, denied OIDC claims,
service-role-only global ManagementCluster registration, trusted context, and real
FleetDB stale-update/delete conflicts. Existing admission, role/session forbids,
startup snapshots, typed errors, metrics and interrupted-run cleanup tests remain.
Artifacts include process/Postgres logs, a run manifest and JUnit output. Local
injected Gateway headers do not prove SigV4/network isolation or deployed rollout.

## Note

These are integration/functional tests, separate from unit tests in `pkg/`.
