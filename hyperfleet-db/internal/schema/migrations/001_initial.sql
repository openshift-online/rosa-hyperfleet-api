-- Resources: one row per object. Three lifecycle states:
--   live (deletion_timestamp IS NULL),
--   dying (deletion_timestamp set, has finalizers),
--   fully deleted / tombstone (deletion_timestamp set, no finalizers).
CREATE TABLE IF NOT EXISTS kubernetes_resources (
    gvk                TEXT        NOT NULL,
    namespace          TEXT        NOT NULL,
    name               TEXT        NOT NULL,
    uid                UUID        NOT NULL DEFAULT gen_random_uuid(),
    txid_stamp         xid8        NOT NULL,
    object_version     BIGINT      NOT NULL DEFAULT 1,
    spec               JSONB       NOT NULL,
    status             JSONB       NOT NULL,
    metadata           JSONB       NOT NULL,
    deletion_timestamp TIMESTAMPTZ NULL,
    created_at         TIMESTAMPTZ DEFAULT now(),
    updated_at         TIMESTAMPTZ DEFAULT now(),
    PRIMARY KEY (gvk, namespace, name)
);

CREATE INDEX IF NOT EXISTS idx_resources_list
    ON kubernetes_resources (gvk)
    WHERE deletion_timestamp IS NULL;

CREATE INDEX IF NOT EXISTS idx_resources_watch
    ON kubernetes_resources (gvk, txid_stamp);

-- Label selectors: containment (@>) and key-exists (?) on metadata->'labels'.
CREATE INDEX IF NOT EXISTS idx_resources_labels
    ON kubernetes_resources USING GIN ((metadata->'labels'));

-- Compaction horizon per GVK
CREATE TABLE IF NOT EXISTS compaction_horizon (
    gvk           TEXT   NOT NULL PRIMARY KEY,
    compacted_xid BIGINT NOT NULL
);

-- Write stored procedure.
-- Performs optional no-op suppression and upsert in a single server-side call.
-- Uses pg_current_xact_id() as the ordering stamp — no shared counter, no lock
-- contention. Does NOT issue pg_notify — the caller's doorbell debouncer
-- coalesces notifications to avoid per-write round-trip overhead.
-- Returns per-step timings (microseconds) so the caller can emit them as
-- Prometheus histograms without additional round-trips.
DROP FUNCTION IF EXISTS pgctl_write;
CREATE OR REPLACE FUNCTION pgctl_write(
    p_status_only      BOOLEAN,
    p_gvk             TEXT,
    p_namespace        TEXT,
    p_name             TEXT,
    p_expected_version BIGINT,
    p_force_write      BOOLEAN,
    p_spec             JSONB,
    p_status           JSONB,
    p_metadata         JSONB,
    p_deletion_ts      TIMESTAMPTZ DEFAULT NULL
) RETURNS TABLE(out_uid UUID, out_version BIGINT, out_txid xid8, out_changed BOOLEAN,
                out_suppress_us BIGINT, out_upsert_us BIGINT)
LANGUAGE plpgsql AS $$
DECLARE
    v_txid        xid8;
    v_uid         UUID;
    v_version     BIGINT;
    v_existing    RECORD;
    v_t0          TIMESTAMPTZ;
    v_suppress_us BIGINT := 0;
    v_upsert_us   BIGINT := 0;
BEGIN
    v_txid := pg_current_xact_id();

    -- 1. Suppression check (skip if force_write)
    v_t0 := clock_timestamp();
    IF NOT p_force_write THEN
        SELECT kr.uid, kr.object_version, kr.spec, kr.status, kr.metadata, kr.deletion_timestamp
          INTO v_existing
          FROM kubernetes_resources kr
         WHERE kr.gvk = p_gvk AND kr.namespace = p_namespace AND kr.name = p_name;

        IF FOUND THEN
            -- Branch: WriteStatus — compare status only
            IF p_status_only THEN
                IF v_existing.status = p_status THEN
                    v_suppress_us := extract(microseconds from clock_timestamp() - v_t0)::BIGINT;
                    RETURN QUERY SELECT v_existing.uid, v_existing.object_version, '0'::xid8, false,
                        v_suppress_us, v_upsert_us;
                    RETURN;
                END IF;
            -- Branch: Write/WriteObject — compare spec+metadata+deletion_ts, and status if non-NULL
            ELSE
                IF v_existing.spec = p_spec
                   AND (p_status IS NULL OR v_existing.status = p_status)
                   AND v_existing.metadata = p_metadata
                   AND v_existing.deletion_timestamp IS NOT DISTINCT FROM p_deletion_ts THEN
                    v_suppress_us := extract(microseconds from clock_timestamp() - v_t0)::BIGINT;
                    RETURN QUERY SELECT v_existing.uid, v_existing.object_version, '0'::xid8, false,
                        v_suppress_us, v_upsert_us;
                    RETURN;
                END IF;
            END IF;
        END IF;
    END IF;
    v_suppress_us := extract(microseconds from clock_timestamp() - v_t0)::BIGINT;

    -- 2. Upsert — three mutually exclusive branches:
    --    a) p_status_only            → WriteStatus (update status only, version > 0)
    --    b) p_expected_version = 0   → Create (with tombstone revival on conflict)
    --    c) else                     → Update via Write/WriteObject
    v_t0 := clock_timestamp();

    -- Branch A: WriteStatus
    IF p_status_only THEN
        IF p_expected_version = 0 THEN
            RAISE EXCEPTION 'WriteStatus requires ExpectedVersion > 0' USING ERRCODE = 'P0004';
        END IF;

        UPDATE kubernetes_resources
           SET txid_stamp      = v_txid,
               object_version  = object_version + 1,
               status          = p_status,
               updated_at      = now()
         WHERE gvk = p_gvk AND namespace = p_namespace AND name = p_name
           AND object_version = p_expected_version
        RETURNING uid, object_version INTO v_uid, v_version;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'conflict' USING ERRCODE = 'P0002';
        END IF;
    -- Branch B: Create (p_expected_version = 0)
    ELSIF p_expected_version = 0 THEN
        BEGIN
            INSERT INTO kubernetes_resources
                (gvk, namespace, name, txid_stamp,
                 object_version, spec, status, metadata, deletion_timestamp)
            VALUES (p_gvk, p_namespace, p_name, v_txid,
                    1, p_spec, p_status, p_metadata, p_deletion_ts)
            RETURNING uid, object_version INTO v_uid, v_version;
        EXCEPTION WHEN unique_violation THEN
            -- Tombstone revival: if the conflicting row is fully deleted
            -- (deletion_timestamp set, no finalizers), overwrite it as a
            -- fresh resource with a new UID. Dying objects (have finalizers)
            -- and live objects fall through to 'already exists'.
            UPDATE kubernetes_resources
               SET uid                = gen_random_uuid(),
                   txid_stamp         = v_txid,
                   object_version     = 1,
                   spec               = p_spec,
                   status             = COALESCE(p_status, '{}'::jsonb),
                   metadata           = p_metadata,
                   deletion_timestamp = NULL,
                   created_at         = now(),
                   updated_at         = now()
             WHERE gvk = p_gvk AND namespace = p_namespace AND name = p_name
               AND deletion_timestamp IS NOT NULL
               AND (metadata->'finalizers' IS NULL OR metadata->'finalizers' = '[]'::jsonb) -- tombstone filter: also in list.go, compactor.go, writer.go
            RETURNING uid, object_version INTO v_uid, v_version;

            IF NOT FOUND THEN
                RAISE EXCEPTION 'already exists' USING ERRCODE = 'P0003';
            END IF;
        END;
    -- Branch C: Update via Write/WriteObject (p_expected_version > 0)
    -- COALESCE(p_status, status) preserves existing status when p_status is NULL (WriteObject)
    ELSE
        UPDATE kubernetes_resources
           SET txid_stamp          = v_txid,
               object_version      = object_version + 1,
               spec                = p_spec,
               status              = COALESCE(p_status, status),
               metadata            = p_metadata,
               deletion_timestamp  = p_deletion_ts,
               updated_at          = now()
         WHERE gvk = p_gvk AND namespace = p_namespace AND name = p_name
           AND object_version = p_expected_version
        RETURNING uid, object_version INTO v_uid, v_version;

        IF NOT FOUND THEN
            RAISE EXCEPTION 'conflict' USING ERRCODE = 'P0002';
        END IF;
    END IF;
    v_upsert_us := extract(microseconds from clock_timestamp() - v_t0)::BIGINT;

    RETURN QUERY SELECT v_uid, v_version, v_txid, true,
        v_suppress_us, v_upsert_us;
END;
$$;
