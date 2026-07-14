# CLAUDE.md

<!-- Canonical source: AGENTS.md. This file is auto-generated for Claude Code compatibility. -->

This file provides guidance to AI coding assistants when working with this repository.

## Project Overview

ROSA Hyperfleet API — a stateless gateway API for ROSA HCP regional cluster management. Provides REST and gRPC interfaces for managing clusters within a specific cloud region.

## Build & Test Commands

```bash
make build           # Build the API binary
make test            # Run unit tests (Ginkgo)
make fmt             # Format Go source code
make lint            # Run linters
make generate        # Regenerate code (mocks, OpenAPI)
make generate-swagger # Regenerate Swagger/OpenAPI specs
make clean           # Remove build artifacts
```

### Integration & E2E Tests
```bash
make e2e-init-db         # Initialize test database
make e2e-authz-infra-up  # Start authorization test infrastructure
make e2e-authz-infra-down # Stop authorization test infrastructure
```

## Architecture

- **cmd/**: Application entry points
- **pkg/**: Core application code
  - API handlers, services, and data access
  - gRPC and REST server implementations
  - `pkg/zoa/` — ZOA Trusted Actions handlers for FedRAMP-compliant service delivery operations
- **internal/**: Internal packages (not importable by external modules)
- **docs/**: API documentation and design references
- **openapi/**: OpenAPI/Swagger specifications
- **deployment/**: Kubernetes deployment manifests
- **test/**: Integration and E2E test suites
- **hack/**: Development scripts and utilities

## Key Conventions

- Module path: `github.com/openshift/rosa-hyperfleet-api`
- Uses Ginkgo/Gomega for testing
- OpenAPI-first API design
- DynamoDB for data persistence
