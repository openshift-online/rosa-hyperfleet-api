# api-codegen

![Coverage](https://img.shields.io/badge/coverage-31.3%25-yellow)

Build-time code generators for the ROSA Hyperfleet API. These tools scan Go types with markers and generate passthrough types, OpenAPI schemas, CRD variants, conversion functions, and field validation metadata.


## Generators

| Command | Purpose |
|---------|---------|
| `passthrough-gen` | Generate passthrough struct types from HyperShift API types |
| `marker-scanner` | Extract `+hyperfleet:` marker metadata from Go types |
| `openapi-gen` | Generate OpenAPI schemas from annotated Go types |
| `conversion-gen` | Generate conversion functions between API versions |
| `crd-variants` | Produce CRD variants filtered by feature gates |
| `featuregate-info` | Emit feature gate metadata for CRD fields |
| `verify-configuration` | Validate marker consistency across types |

## Usage

```bash
make build-api-codegen     # Build all 7 generator binaries
make test-api-codegen      # Run tests
make coverage-api-codegen  # Generate coverage report
```

## Reference

[api-management](./docs/api/api-management.md)
