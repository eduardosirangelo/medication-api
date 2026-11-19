-- +goose NO TRANSACTION
-- Built CONCURRENTLY so live writes are never blocked; each is dropped first
-- because a failed concurrent build leaves an INVALID index behind.
-- All are partial on active rows: soft-deleted rows are never listed, so
-- indexing them would only cost space and write time.

-- +goose Up
-- Unfiltered listing, newest first: walked backwards for ORDER BY id DESC.
DROP INDEX CONCURRENTLY IF EXISTS medications_active_id_idx;
CREATE INDEX CONCURRENTLY medications_active_id_idx
    ON medications (id)
    WHERE deleted_at IS NULL;

-- form filter: equality first, then the keyset column, so one range scan
-- serves both the filter and the cursor.
DROP INDEX CONCURRENTLY IF EXISTS medications_active_form_id_idx;
CREATE INDEX CONCURRENTLY medications_active_form_id_idx
    ON medications (form, id)
    WHERE deleted_at IS NULL;

-- Case-insensitive substring search (name ILIKE '%term%'): a B-tree can only
-- serve prefixes; trigrams make infix matches indexable.
DROP INDEX CONCURRENTLY IF EXISTS medications_active_name_trgm_idx;
CREATE INDEX CONCURRENTLY medications_active_name_trgm_idx
    ON medications USING gin (name gin_trgm_ops)
    WHERE deleted_at IS NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS medications_active_name_trgm_idx;
DROP INDEX CONCURRENTLY IF EXISTS medications_active_form_id_idx;
DROP INDEX CONCURRENTLY IF EXISTS medications_active_id_idx;
