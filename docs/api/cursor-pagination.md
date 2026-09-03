# Cursor-Based Pagination

Migration from limit/offset to keyset cursor pagination for the three K8s/Postgres-backed list endpoints: `/clusters`, `/nodepools`, `/oidc_configs`.

## Why

Offset pagination fetches all rows up to the requested position and then discards them. As account sizes grow this is expensive, and items shift when records are added or deleted between pages — causing duplicates or skips. Cursor (keyset) pagination fixes both: only the needed rows are fetched, and the cursor marks a stable position in the ordering that isn't affected by concurrent writes.

ZOA/DynamoDB endpoints are out of scope — they require a separate DynamoDB `ExclusiveStartKey` approach.

## API Changes

### Request

| Parameter | Before | After |
|---|---|---|
| Page size | `?limit=N` (default 50, max 100) | `?limit=N` (unchanged) |
| Page position | `?offset=N` | `?continue=<token>` |

The `continue` token is an opaque base64 string returned in `metadata.continue` of the previous response. Omit it to start from the beginning. The old `?offset=` parameter is removed.

### Response

The continue token follows K8s conventions and is placed in `metadata.continue` so the generated typed clientset can read it from `ListMeta.Continue`:

```json
{
  "metadata": {
    "continue": "eyJjdXJzb3IiOiJleUowZUdsa1gzTjBZVzF3..."
  },
  "items": [...],
  "limit": 50
}
```

| Field | Before | After |
|---|---|---|
| `items` | ✓ | ✓ (unchanged) |
| `total` | ✓ count of all records | removed — unreliable across pages |
| `offset` | ✓ | removed |
| `limit` | ✓ | ✓ (unchanged) |
| `metadata.continue` | — | ✓ opaque token to pass on the next request |

A non-empty `metadata.continue` means more pages exist. An empty (or absent) `metadata.continue` means the current page is the last.

## How It Works

The sort key for all list queries is `txid_stamp` (a PostgreSQL `xid8` transaction ID) — monotonically increasing per transaction, never reused.

### Keyset position

The cursor encodes the `txid_stamp` of the last item on the current page. On the next request, the SQL changes from:

```sql
ORDER BY txid_stamp LIMIT $N OFFSET $M
```

to:

```sql
WHERE ... AND txid_stamp > $cursor_min AND txid_stamp <= $watermark
ORDER BY txid_stamp LIMIT $N
```

Only the rows that need to be returned are touched.

### Snapshot watermark

The cursor also carries a **snapshot watermark** (`txid_stamp_max`) set from the `REPEATABLE READ` snapshot taken during page 1's query. All subsequent pages apply `AND txid_stamp <= $watermark`, which excludes any rows written after page 1 was fetched. This makes the result set consistent across pages: inserts that arrive mid-traversal do not appear.

### Lookahead detection

To avoid spurious continue tokens at the end of a result set, each query fetches `limit + 1` rows internally. A continue token is only set when the extra row is present — meaning a real next page exists. The extra row is never returned to the caller.

## Account Filtering

Previously `ListClusters` and `ListNodePools` (without clusterID) fetched all rows of that GVK and filtered by account label in Go memory. With this change, the account filter moves to SQL via a field selector on `metadata.labels`:

```sql
AND metadata->'labels'->>'hyperfleet.io/account-id' = $accountID
```

All three resource types now scope at the DB level — no rows for other accounts are read.

## Security

### Account ID validation

The `Identity` middleware validates the `X-Amz-Account-Id` header against the AWS 12-digit format (`^[0-9]{12}$`) before storing it in context. Malformed account IDs are rejected with HTTP 400 (AUTH-013) before reaching any handler.

### Cursor validation

The continue token is a two-layer structure. The outer (platform-api) layer wraps the inner (hyperfleet-db) layer and embeds the account ID:

```json
{"cursor": "<hyperfleet-db-token>", "account_id": "123456789012"}
```

On decode, the token's `account_id` is compared against the current request's account ID. A mismatch returns HTTP 400. The inner token encodes `txid_stamp` and `txid_stamp_max` (the snapshot watermark). A structurally malformed token at either layer (bad base64, invalid JSON) also returns HTTP 400 — distinct from the HTTP 500 returned for genuine server/DB errors.

## Limitations and Trade-offs

Cursor pagination is optimised for sequential traversal. It intentionally does not support arbitrary position jumps, which means:

- **No skipping to a specific position.** There is no equivalent of `?offset=N`. A caller who wants items 51–100 must walk the first 50 items to obtain the cursor, then request the next page. This is by design — offset-based skipping is O(N) at the database and produces inconsistent results when rows are inserted or deleted between requests.

- **No total count.** The response does not include a `total` field. Because the cursor traversal is bounded by a snapshot watermark, the count of items visible to a given traversal may differ from a concurrent `SELECT COUNT(*)`. Callers that need an approximate count should track how many items they have received across pages.

- **No random access by page number.** There is no `?page=3` parameter. Navigation is always forward from a cursor.

### Narrowing results

The practical alternative to offset-based skipping is server-side filtering, which reduces the result set before pagination begins. Future search options may include filtering by creation or last-updated timestamp, name, status, and other attributes — allowing callers to scope a traversal to only the records they care about rather than paging past unrelated ones.

## Frontend / Console Integration

Because the API uses cursor-based pagination, UI engineers have two strategies depending on the expected dataset size.

### Fetch-all then paginate client-side (recommended for typical accounts)

For most accounts the number of clusters and nodepools is small (tens to low hundreds). In this case the frontend can collect all pages upfront and render a standard numbered table:

1. On component mount, walk the cursor chain until `metadata.continue` is empty.
2. Hold the full result array in component state.
3. Render numbered pages, search boxes, and sort controls entirely client-side against the buffered array.

This delivers a familiar numbered-page UX with no backend round-trips after the initial load. The total page count and "jump to page N" are trivially computed from the buffered length.

```typescript
async function fetchAll(client: HyperfleetClient, limit = 50): Promise<Cluster[]> {
  const items: Cluster[] = [];
  let continueToken: string | undefined;
  do {
    const page = await client.clusters.list({ limit, continue: continueToken });
    items.push(...page.items);
    continueToken = page.metadata?.continue;
  } while (continueToken);
  return items;
}
```

### Next / Previous only (for large accounts or real-time data)

If an account could hold thousands of clusters, or if the data changes frequently enough that a stale buffer would mislead users, do not attempt to fetch all pages. Instead:

- Show only **Next** and **Previous** controls — no numbered pages, no "jump to page N".
- Store the cursor token in component state (or the URL) and fetch the next page on demand.
- Disable **Previous** on the first page (no cursor available to go back; cursors are forward-only).
- Disable **Next** when `metadata.continue` is empty.

There is no total count in the response, so numbered-page navigation ("Page 3 of 12") is not possible without fetching all pages first.

### Which strategy to use

| Signal | Strategy |
|---|---|
| Account cluster count is typically < 500 | Fetch-all, client-side pagination |
| Account cluster count can be > 500 | Next/Previous cursor navigation |
| Table has real-time status that changes rapidly | Next/Previous (stale buffer misleads) |
| Table needs sort, search, or filter client-side | Fetch-all (sorts/searches across full set) |

When in doubt, start with fetch-all and add a threshold (e.g. > 200 items on first page) to switch to cursor-only navigation automatically.

## Clientset Changes

The `platform.ListOptions` struct in the `clientset` module changes:

```go
// Before
type ListOptions struct {
    Limit  int64
    Offset int64
}

// After
type ListOptions struct {
    Limit    int64
    Continue string // opaque token from a prior list response; empty = first page
}
```

The transport adapter (`clientset/transport/bridge.go`) previously rewrote numeric `?continue=N` to `?offset=N`. That rewrite is removed — the opaque token is passed through as-is.

## Paginating Through Results

The typed `List()` return value is a `*ClusterList` (or `NodePoolList`, `OidcConfigList`). The continue token is available via `list.Continue` (promoted from `metav1.ListMeta`):

```go
var allClusters []*v1alpha1.Cluster
opts := platform.ListOptions{Limit: 50}

for {
    list, err := cs.HyperfleetV1alpha1().Clusters().List(ctx, opts)
    if err != nil {
        return err
    }
    allClusters = append(allClusters, list.Items...)
    if list.Continue == "" {
        break
    }
    opts.Continue = list.Continue
}
```
