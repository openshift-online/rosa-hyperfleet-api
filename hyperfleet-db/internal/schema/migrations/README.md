# Migrations

## Index pattern for global uniqueness

Global uniqueness of values like DNS prefixes or OIDC issuer URLs is enforced
at the application level using the **Index CRD**, not via custom database
indexes.

### How it works

The `kubernetes_resources` table has a composite primary key
`(gvk, namespace, name)`. An `Index` resource reserves a unique name within a
namespace that acts as the **uniqueness domain**:

| Use case             | Index namespace               | Index name                            |
| -------------------- | ----------------------------- | ------------------------------------- |
| DNS prefix per shard | `dns-shard-<id>-reservations` | `<prefix>`                            |
| OIDC issuer URL      | `oidc-issuer-reservations`    | `<sha256-hex(normalized-issuer-url)>` |

A DNS prefix is already a valid Kubernetes object name, so it's used verbatim.
An issuer URL is not (it contains a `://` scheme separator and often `/` path
segments), so `IssuerURLIndexName` in `api/v1alpha1/oidcconfig_types.go`
hex-encodes its SHA-256 digest into a valid name instead; two different URLs
essentially never collide, so this preserves the same uniqueness guarantee.

Creating an Index with a name that already exists in the same namespace returns
`AlreadyExists` (409), which the controller or API handler uses as the
collision signal.

Higher-level resources (e.g. `DNSReservation` in `account-<id>`) carry an
`IndexRef` (namespace + name) pointing to their backing Index. This separates
account-scoped data from the global uniqueness guard.

### When to use this pattern

Use an Index instead of a migration-level unique index when:

- Uniqueness must span multiple namespaces (the PK only constrains within a
  single `(gvk, namespace)` pair)
- The unique value has a natural domain boundary (DNS shard, OIDC issuer pool)
- You need atomic create-or-fail semantics without database-specific SQL

See `api/v1alpha1/index_types.go` for the CRD definition and usage examples.
