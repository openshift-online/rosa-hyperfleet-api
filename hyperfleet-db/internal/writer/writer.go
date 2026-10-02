package writer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/metrics"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/internal/model"
)

// Doorbell coalesces write notifications so watchers wake up without
// paying a pg_notify round-trip on every single write.
type Doorbell interface {
	Ring(gvk string)
}

type Writer struct {
	conn     *pgx.Conn
	hooks    TxHooks
	metrics  *metrics.WriterMetrics
	doorbell Doorbell
}

func New(conn *pgx.Conn, hooks TxHooks) *Writer {
	return &Writer{conn: conn, hooks: hooks}
}

// WithMetrics attaches Prometheus metrics to the writer.
func (w *Writer) WithMetrics(m *metrics.WriterMetrics) *Writer {
	w.metrics = m
	return w
}

// WithDoorbell attaches a debounced doorbell that coalesces pg_notify calls.
func (w *Writer) WithDoorbell(d Doorbell) *Writer {
	w.doorbell = d
	return w
}

func (w *Writer) Write(ctx context.Context, req model.WriteRequest) (model.WriteResult, error) {
	if w.hooks != nil {
		return w.writeMultiStatement(ctx, req)
	}
	return w.writeStoredProc(ctx, req)
}

// WriteStatus updates only the status sub-resource. The object must already
// exist (ExpectedVersion > 0). Spec, metadata, and deletion_timestamp are not
// touched. The txid_stamp and object_version are bumped so watchers see status
// changes in the same ordered stream as spec changes.
func (w *Writer) WriteStatus(ctx context.Context, req model.StatusWriteRequest) (model.WriteResult, error) {
	if req.ExpectedVersion == 0 {
		return model.WriteResult{}, fmt.Errorf("WriteStatus requires ExpectedVersion > 0: object must exist")
	}

	if w.hooks != nil {
		return w.writeStatusMultiStatement(ctx, req)
	}
	return w.writeStatusStoredProc(ctx, req)
}

// WriteObject updates spec, metadata, and deletion_timestamp without touching
// status. The object must already exist (ExpectedVersion > 0). The txid_stamp
// and object_version are bumped so watchers see changes in the ordered stream.
func (w *Writer) WriteObject(ctx context.Context, req model.ObjectWriteRequest) (model.WriteResult, error) {
	if req.ExpectedVersion == 0 {
		return model.WriteResult{}, fmt.Errorf("WriteObject requires ExpectedVersion > 0: object must exist")
	}

	if w.hooks != nil {
		return w.writeObjectMultiStatement(ctx, req)
	}
	return w.writeObjectStoredProc(ctx, req)
}

// ReadBack resolves an ambiguous commit by checking if the write actually landed.
// Returns the resource if found at the expected txid, nil if not.
func (w *Writer) ReadBack(ctx context.Context, gvk, namespace, name string, txid uint64) (*model.Resource, error) {
	r := &model.Resource{}
	err := w.conn.QueryRow(ctx, `
		SELECT gvk, namespace, name, uid, txid_stamp::text::bigint,
		       object_version, spec, status, metadata, deletion_timestamp,
		       created_at, updated_at
		FROM kubernetes_resources
		WHERE gvk = $1 AND namespace = $2 AND name = $3
		  AND txid_stamp::text::bigint = $4`,
		gvk, namespace, name, txid,
	).Scan(&r.GVK, &r.Namespace, &r.Name, &r.UID,
		&r.TxidStamp, &r.ObjectVersion, &r.Spec, &r.Status,
		&r.Metadata, &r.DeletionTimestamp, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read-back: %w", err)
	}
	return r, nil
}

// --- Stored procedure path (production, hooks==nil) ---

const pgctlWriteSQL = `SELECT * FROM pgctl_write($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`

func (w *Writer) writeStoredProc(ctx context.Context, req model.WriteRequest) (model.WriteResult, error) {
	start := time.Now()

	result, err := w.callStoredProc(ctx, writeParams{
		statusOnly: false, gvk: req.GVK, namespace: req.Namespace, name: req.Name,
		expectedVersion: req.ExpectedVersion, expectedUID: req.ExpectedUID, forceWrite: req.ForceWrite,
		spec: req.Spec, status: req.Status, metadata: req.Metadata,
		deletionTimestamp: req.DeletionTimestamp,
	})

	w.observeResult(start, req.GVK, result, err)
	return result, err
}

func (w *Writer) writeStatusStoredProc(ctx context.Context, req model.StatusWriteRequest) (model.WriteResult, error) {
	start := time.Now()

	result, err := w.callStoredProc(ctx, writeParams{
		statusOnly: true, gvk: req.GVK, namespace: req.Namespace, name: req.Name,
		expectedVersion: req.ExpectedVersion, expectedUID: req.ExpectedUID, forceWrite: req.ForceWrite,
		spec: nil, status: req.Status, metadata: nil,
		deletionTimestamp: nil,
	})

	w.observeResult(start, req.GVK, result, err)
	return result, err
}

func (w *Writer) writeObjectStoredProc(ctx context.Context, req model.ObjectWriteRequest) (model.WriteResult, error) {
	start := time.Now()

	result, err := w.callStoredProc(ctx, writeParams{
		statusOnly: false, gvk: req.GVK, namespace: req.Namespace, name: req.Name,
		expectedVersion: req.ExpectedVersion, expectedUID: req.ExpectedUID, forceWrite: req.ForceWrite,
		spec: req.Spec, status: nil, metadata: req.Metadata,
		deletionTimestamp: req.DeletionTimestamp,
	})

	w.observeResult(start, req.GVK, result, err)
	return result, err
}

func (w *Writer) callStoredProc(ctx context.Context, p writeParams) (model.WriteResult, error) {
	t0 := time.Now()

	var uid uuid.UUID
	var version int64
	var txid uint64
	var changed bool
	var suppressUs, upsertUs int64
	var expectedUID any
	if p.expectedUID != nil {
		expectedUID = *p.expectedUID
	}

	err := w.conn.QueryRow(ctx, pgctlWriteSQL,
		p.statusOnly, p.gvk, p.namespace, p.name,
		p.expectedVersion, expectedUID, p.forceWrite,
		p.spec, p.status, p.metadata, p.deletionTimestamp,
	).Scan(&uid, &version, &txid, &changed, &suppressUs, &upsertUs)
	w.observeStep("stored_proc", time.Since(t0))

	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			return model.WriteResult{}, mapStoredProcError(err)
		}
		return model.WriteResult{}, &AmbiguousCommitError{
			Cause: err, GVK: p.gvk, Namespace: p.namespace, Name: p.name, Txid: 0,
		}
	}

	w.observeStep("suppression_check", time.Duration(suppressUs)*time.Microsecond)
	w.observeStep("upsert", time.Duration(upsertUs)*time.Microsecond)

	if !changed {
		return model.WriteResult{ObjectVersion: version, UID: uid, Changed: false}, nil
	}

	if w.doorbell != nil {
		w.doorbell.Ring(p.gvk)
	}

	return model.WriteResult{Txid: txid, ObjectVersion: version, UID: uid, Changed: true}, nil
}

func mapStoredProcError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "P0002":
			return ErrConflict
		case "P0003":
			return ErrAlreadyExists
		case "P0004":
			return fmt.Errorf("WriteStatus requires ExpectedVersion > 0: object must exist")
		}
	}
	return fmt.Errorf("stored proc: %w", err)
}

// --- Multi-statement path (test hooks, hooks!=nil) ---

func (w *Writer) writeMultiStatement(ctx context.Context, req model.WriteRequest) (model.WriteResult, error) {
	p := writeParams{
		statusOnly: false,
		gvk:        req.GVK, namespace: req.Namespace, name: req.Name,
		expectedVersion: req.ExpectedVersion, expectedUID: req.ExpectedUID,
		forceWrite: req.ForceWrite,
	}

	checker := func(existing *existingRow) bool {
		return JSONEqual(existing.spec, req.Spec) &&
			JSONEqual(existing.status, req.Status) &&
			JSONEqual(existing.metadata, req.Metadata) &&
			timeEqual(existing.deletionTimestamp, req.DeletionTimestamp)
	}

	return w.execWrite(ctx, p, checker, func(ctx context.Context, tx pgx.Tx, txid uint64) (uuid.UUID, int64, error) {
		var uid uuid.UUID
		var version int64

		if req.ExpectedVersion == 0 {
			if _, err := tx.Exec(ctx, "SAVEPOINT tombstone_revival"); err != nil {
				return uuid.Nil, 0, fmt.Errorf("savepoint: %w", err)
			}
			err := tx.QueryRow(ctx, `
				INSERT INTO kubernetes_resources
					(gvk, namespace, name, txid_stamp,
					 object_version, spec, status, metadata, deletion_timestamp)
				VALUES ($1, $2, $3, $4::xid8, 1, $5, $6, $7, $8)
				RETURNING uid, object_version`,
				req.GVK, req.Namespace, req.Name, txid,
				req.Spec, req.Status, req.Metadata, req.DeletionTimestamp,
			).Scan(&uid, &version)
			if err != nil {
				var pgErr *pgconn.PgError
				if errors.As(err, &pgErr) && pgErr.Code == "23505" {
					if _, err2 := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT tombstone_revival"); err2 != nil {
						return uuid.Nil, 0, fmt.Errorf("rollback savepoint: %w", err2)
					}
					err = tx.QueryRow(ctx, `
						UPDATE kubernetes_resources
						   SET uid                = gen_random_uuid(),
						       txid_stamp         = $3::xid8,
						       object_version     = 1,
						       spec               = $4,
						       status             = COALESCE($5, '{}'::jsonb),
						       metadata           = $6,
						       deletion_timestamp = NULL,
						       created_at         = now(),
						       updated_at         = now()
						 WHERE gvk = $1 AND namespace = $2 AND name = $7
						   AND deletion_timestamp IS NOT NULL
						   AND (metadata->'finalizers' IS NULL OR metadata->'finalizers' = '[]'::jsonb) -- tombstone filter: also in list.go, compactor.go, 001_initial.sql
						RETURNING uid, object_version`,
						req.GVK, req.Namespace, txid,
						req.Spec, req.Status, req.Metadata, req.Name,
					).Scan(&uid, &version)
					if err != nil {
						if errors.Is(err, pgx.ErrNoRows) {
							return uuid.Nil, 0, ErrAlreadyExists
						}
						return uuid.Nil, 0, fmt.Errorf("tombstone revival: %w", err)
					}
				} else {
					return uuid.Nil, 0, fmt.Errorf("create resource: %w", err)
				}
			}
		} else {
			err := tx.QueryRow(ctx, `
				UPDATE kubernetes_resources
				SET txid_stamp = $1::xid8,
				    object_version = object_version + 1,
				    spec = $2, status = COALESCE($3, status), metadata = $4,
				    deletion_timestamp = $5, updated_at = now()
				WHERE gvk = $6 AND namespace = $7 AND name = $8
				  AND object_version = $9
				  AND ($10::uuid IS NULL OR uid = $10)
				RETURNING uid, object_version`,
				txid, req.Spec, req.Status, req.Metadata, req.DeletionTimestamp,
				req.GVK, req.Namespace, req.Name, req.ExpectedVersion, req.ExpectedUID,
			).Scan(&uid, &version)
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					return uuid.Nil, 0, ErrConflict
				}
				return uuid.Nil, 0, fmt.Errorf("update resource: %w", err)
			}
		}

		return uid, version, nil
	})
}

func (w *Writer) writeStatusMultiStatement(ctx context.Context, req model.StatusWriteRequest) (model.WriteResult, error) {
	p := writeParams{
		statusOnly: true,
		gvk:        req.GVK, namespace: req.Namespace, name: req.Name,
		expectedVersion: req.ExpectedVersion, expectedUID: req.ExpectedUID,
		forceWrite: req.ForceWrite,
	}

	checker := func(existing *existingRow) bool {
		return JSONEqual(existing.status, req.Status)
	}

	return w.execWrite(ctx, p, checker, func(ctx context.Context, tx pgx.Tx, txid uint64) (uuid.UUID, int64, error) {
		var uid uuid.UUID
		var version int64

		err := tx.QueryRow(ctx, `
			UPDATE kubernetes_resources
			SET txid_stamp = $1::xid8,
			    object_version = object_version + 1,
			    status = $2, updated_at = now()
			WHERE gvk = $3 AND namespace = $4 AND name = $5
			  AND object_version = $6
			  AND ($7::uuid IS NULL OR uid = $7)
			RETURNING uid, object_version`,
			txid, req.Status,
			req.GVK, req.Namespace, req.Name, req.ExpectedVersion, req.ExpectedUID,
		).Scan(&uid, &version)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, 0, ErrConflict
			}
			return uuid.Nil, 0, fmt.Errorf("update status: %w", err)
		}

		return uid, version, nil
	})
}

func (w *Writer) writeObjectMultiStatement(ctx context.Context, req model.ObjectWriteRequest) (model.WriteResult, error) {
	p := writeParams{
		statusOnly: false,
		gvk:        req.GVK, namespace: req.Namespace, name: req.Name,
		expectedVersion: req.ExpectedVersion, expectedUID: req.ExpectedUID,
		forceWrite: req.ForceWrite,
	}

	checker := func(existing *existingRow) bool {
		return JSONEqual(existing.spec, req.Spec) &&
			JSONEqual(existing.metadata, req.Metadata) &&
			timeEqual(existing.deletionTimestamp, req.DeletionTimestamp)
	}

	return w.execWrite(ctx, p, checker, func(ctx context.Context, tx pgx.Tx, txid uint64) (uuid.UUID, int64, error) {
		var uid uuid.UUID
		var version int64

		err := tx.QueryRow(ctx, `
			UPDATE kubernetes_resources
			SET txid_stamp = $1::xid8,
			    object_version = object_version + 1,
			    spec = $2, metadata = $3, deletion_timestamp = $4,
			    updated_at = now()
			WHERE gvk = $5 AND namespace = $6 AND name = $7
			  AND object_version = $8
			  AND ($9::uuid IS NULL OR uid = $9)
			RETURNING uid, object_version`,
			txid, req.Spec, req.Metadata, req.DeletionTimestamp,
			req.GVK, req.Namespace, req.Name, req.ExpectedVersion, req.ExpectedUID,
		).Scan(&uid, &version)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.Nil, 0, ErrConflict
			}
			return uuid.Nil, 0, fmt.Errorf("update spec: %w", err)
		}

		return uid, version, nil
	})
}

type upsertFunc func(ctx context.Context, tx pgx.Tx, txid uint64) (uuid.UUID, int64, error)

// contentChecker returns true if the existing row's content matches the request
// (i.e., the write is a no-op). Only called when suppression is active and the
// row exists.
type contentChecker func(existing *existingRow) bool

type existingRow struct {
	uid               uuid.UUID
	objectVersion     int64
	spec              json.RawMessage
	status            json.RawMessage
	metadata          json.RawMessage
	deletionTimestamp *time.Time
}

type writeParams struct {
	statusOnly        bool
	gvk               string
	namespace         string
	name              string
	expectedVersion   int64
	expectedUID       *uuid.UUID
	forceWrite        bool
	spec              json.RawMessage
	status            json.RawMessage
	metadata          json.RawMessage
	deletionTimestamp *time.Time
}

func (w *Writer) execWrite(ctx context.Context, p writeParams, isContentEqual contentChecker, upsert upsertFunc) (model.WriteResult, error) {
	start := time.Now()
	result, err := w.execWriteInner(ctx, p, isContentEqual, upsert)

	w.observeResult(start, p.gvk, result, err)
	return result, err
}

func (w *Writer) observeResult(start time.Time, gvk string, result model.WriteResult, err error) {
	if w.metrics == nil {
		return
	}
	dur := time.Since(start)
	var resultLabel string
	switch {
	case err == nil && !result.Changed:
		resultLabel = "noop"
		w.metrics.NoopSuppressionsTotal.Inc()
	case err == nil:
		resultLabel = "success"
	case errors.Is(err, ErrConflict):
		resultLabel = "conflict"
	case errors.Is(err, ErrAlreadyExists):
		resultLabel = "already_exists"
	default:
		var ambErr *AmbiguousCommitError
		if errors.As(err, &ambErr) {
			resultLabel = "ambiguous_commit"
		} else {
			resultLabel = "error"
		}
	}
	w.metrics.WriteDuration.WithLabelValues(gvk, resultLabel).Observe(dur.Seconds())
	w.metrics.WritesTotal.WithLabelValues(gvk, resultLabel).Inc()

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "57014" {
		w.metrics.StatementTimeoutsTotal.Inc()
		log.Printf("CRITICAL: write transaction hit statement_timeout (%.1fs) for gvk=%s — xmin is pinned, watcher HWM frozen", dur.Seconds(), gvk)
	}
}

func (w *Writer) observeStep(step string, d time.Duration) {
	if w.metrics != nil {
		w.metrics.WriteStepDuration.WithLabelValues(step).Observe(d.Seconds())
	}
}

func (w *Writer) execWriteInner(ctx context.Context, p writeParams, isContentEqual contentChecker, upsert upsertFunc) (model.WriteResult, error) {
	t0 := time.Now()
	tx, err := w.conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return model.WriteResult{}, fmt.Errorf("begin: %w", err)
	}
	w.observeStep("begin_tx", time.Since(t0))
	defer tx.Rollback(ctx) //nolint:errcheck

	t0 = time.Now()
	if !p.forceWrite {
		var existing *existingRow
		if p.expectedVersion > 0 {
			var err error
			existing, err = readExisting(ctx, tx, p.gvk, p.namespace, p.name)
			if err != nil {
				return model.WriteResult{}, err
			}
		}
		w.observeStep("suppression_check", time.Since(t0))

		if existing != nil && p.expectedVersion > 0 &&
			(existing.objectVersion != p.expectedVersion ||
				(p.expectedUID != nil && existing.uid != *p.expectedUID)) {
			if err := w.hooks.AfterSuppressionCheck(ctx, tx, false); err != nil {
				return model.WriteResult{}, err
			}
			return model.WriteResult{}, ErrConflict
		}

		suppressed := existing != nil && p.expectedVersion > 0 && isContentEqual(existing)
		if err := w.hooks.AfterSuppressionCheck(ctx, tx, suppressed); err != nil {
			return model.WriteResult{}, err
		}

		if suppressed {
			t0 = time.Now()
			if err := tx.Commit(ctx); err != nil {
				return model.WriteResult{}, fmt.Errorf("commit (suppressed): %w", err)
			}
			w.observeStep("commit", time.Since(t0))
			return model.WriteResult{
				ObjectVersion: existing.objectVersion,
				UID:           existing.uid,
				Changed:       false,
			}, nil
		}
	} else {
		w.observeStep("suppression_check", time.Since(t0))
		if err := w.hooks.AfterSuppressionCheck(ctx, tx, false); err != nil {
			return model.WriteResult{}, err
		}
	}

	t0 = time.Now()
	var txid uint64
	err = tx.QueryRow(ctx, `SELECT pg_current_xact_id()::text::bigint`).Scan(&txid)
	w.observeStep("txid_acquire", time.Since(t0))
	if err != nil {
		return model.WriteResult{}, fmt.Errorf("txid acquire: %w", err)
	}

	if err := w.hooks.AfterTxidAcquire(ctx, tx, txid); err != nil {
		return model.WriteResult{}, err
	}

	t0 = time.Now()
	uid, version, err := upsert(ctx, tx, txid)
	w.observeStep("upsert", time.Since(t0))
	if err != nil {
		return model.WriteResult{}, err
	}

	if err := w.hooks.BeforeCommit(ctx, tx); err != nil {
		return model.WriteResult{}, err
	}

	t0 = time.Now()
	if err := tx.Commit(ctx); err != nil {
		return model.WriteResult{}, &AmbiguousCommitError{
			Cause:     err,
			GVK:       p.gvk,
			Namespace: p.namespace,
			Name:      p.name,
			Txid:      txid,
		}
	}
	w.observeStep("commit", time.Since(t0))

	if w.doorbell != nil {
		w.doorbell.Ring(p.gvk)
	}

	return model.WriteResult{Txid: txid, ObjectVersion: version, UID: uid, Changed: true}, nil
}

func readExisting(ctx context.Context, tx pgx.Tx, gvk, namespace, name string) (*existingRow, error) {
	row := &existingRow{}
	err := tx.QueryRow(ctx, `
		SELECT uid, object_version, spec, status, metadata, deletion_timestamp
		FROM kubernetes_resources
		WHERE gvk = $1 AND namespace = $2 AND name = $3`,
		gvk, namespace, name,
	).Scan(&row.uid, &row.objectVersion, &row.spec, &row.status, &row.metadata, &row.deletionTimestamp)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read existing: %w", err)
	}
	return row, nil
}

func JSONEqual(a, b json.RawMessage) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	ra, _ := json.Marshal(va)
	rb, _ := json.Marshal(vb)
	return string(ra) == string(rb)
}

func timeEqual(a, b *time.Time) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Equal(*b)
}
