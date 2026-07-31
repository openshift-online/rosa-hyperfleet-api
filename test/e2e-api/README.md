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

# Run via Make
make test-e2e-api
```

## Environment Variables

- `E2E_BASE_URL`: Base URL of the API server (required)
- `E2E_ACCOUNT_ID`: AWS account ID for rate-limited requests (defaults to STS caller identity)
- `E2E_RHOBS_API_URL`: RHOBS API Gateway URL for observability tests (optional — tests are skipped if unset)

## Note

These are integration/functional tests, separate from unit tests in `pkg/`.
