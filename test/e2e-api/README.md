# E2E Tests

End-to-end integration and functional tests for the Seaplane Platform API.

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
- `E2E_RHOBS_API_URL`: RHOBS API Gateway URL for observability tests (optional — tests are skipped if unset)

## Local authorization test

`make test-e2e-authz` runs `TestAuthzHTTP`: the real API binary against a
disposable PostgreSQL (Podman), with no AWS credentials. It checks every
resource operation over HTTP, plus startup, metrics, and logs. Logs go to
`test-results/authz-http-*`.

## Note

These are integration/functional tests, separate from unit tests in `pkg/`.
