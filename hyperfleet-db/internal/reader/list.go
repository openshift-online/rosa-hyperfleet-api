package reader

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/model"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/resourceversion"
)

type ListResult struct {
	Resources       []model.Resource
	ResourceVersion resourceversion.RV
}

type ListFilter struct {
	WhereClauses    []string
	WhereArgs       []any
	Limit           int64
	TxidStampCursor uint64 // WHERE txid_stamp > N  (start of page)
	TxidStampMax    uint64 // WHERE txid_stamp <= N (snapshot watermark, carried across pages)
}

// List performs a REPEATABLE READ snapshot read of all live and dying resources
// matching the given GVK. Fully-deleted tombstones (deletion_timestamp set, no
// finalizers) are excluded by the query. Dying objects (deletion_timestamp set,
// has finalizers) are included so controllers can perform cleanup before
// removing their finalizers. The returned RV uses the xmin watermark from the
// same snapshot, so there is no skew between the data and the version.
func List(ctx context.Context, conn *pgx.Conn, gvk string, filter *ListFilter) (*ListResult, error) {
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, fmt.Errorf("list begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var xmin uint64
	err = tx.QueryRow(ctx, `SELECT pg_snapshot_xmin(pg_current_snapshot())::text::bigint`).Scan(&xmin)
	if err != nil {
		return nil, fmt.Errorf("list xmin: %w", err)
	}

	var watermark uint64
	if xmin > 0 {
		watermark = xmin - 1
	}
	rv := resourceversion.RV{Watermark: watermark}

	var qb strings.Builder
	qb.WriteString(`
		SELECT gvk, namespace, name, uid, txid_stamp::text::bigint,
		       object_version, spec, status, metadata,
		       deletion_timestamp, created_at, updated_at
		FROM kubernetes_resources
		WHERE gvk = $1
		  AND (deletion_timestamp IS NULL OR metadata->'finalizers' != '[]'::jsonb)`) // tombstone filter: also in compactor.go, 001_initial.sql, writer.go
	args := []any{gvk}

	if filter != nil {
		for _, clause := range filter.WhereClauses {
			qb.WriteString(" AND ")
			qb.WriteString(clause)
		}
		args = append(args, filter.WhereArgs...)
	}
	if filter != nil && filter.TxidStampCursor > 0 {
		args = append(args, filter.TxidStampCursor)
		fmt.Fprintf(&qb, " AND txid_stamp > $%d", len(args))
	}
	if filter != nil && filter.TxidStampMax > 0 {
		args = append(args, filter.TxidStampMax)
		fmt.Fprintf(&qb, " AND txid_stamp <= $%d", len(args))
	}
	qb.WriteString(" ORDER BY txid_stamp")
	if filter != nil && filter.Limit > 0 {
		args = append(args, filter.Limit)
		fmt.Fprintf(&qb, " LIMIT $%d", len(args))
	}

	resourceRows, err := tx.Query(ctx, qb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	defer resourceRows.Close()

	var resources []model.Resource
	for resourceRows.Next() {
		var r model.Resource
		if err := resourceRows.Scan(
			&r.GVK, &r.Namespace, &r.Name, &r.UID,
			&r.TxidStamp, &r.ObjectVersion, &r.Spec, &r.Status,
			&r.Metadata, &r.DeletionTimestamp, &r.CreatedAt, &r.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("list resource scan: %w", err)
		}
		resources = append(resources, r)
	}
	if err := resourceRows.Err(); err != nil {
		return nil, fmt.Errorf("list resource rows: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("list commit: %w", err)
	}

	return &ListResult{Resources: resources, ResourceVersion: rv}, nil
}
