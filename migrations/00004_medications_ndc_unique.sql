-- +goose NO TRANSACTION
-- CONCURRENTLY builds the index without blocking writes on a live table, but
-- cannot run inside a transaction, so it lives in its own migration.

-- +goose Up
-- A failed concurrent build leaves an INVALID index behind; dropping first
-- makes a retried migration succeed instead of failing on "already exists".
DROP INDEX CONCURRENTLY IF EXISTS medications_active_ndc_key;
CREATE UNIQUE INDEX CONCURRENTLY medications_active_ndc_key
    ON medications (ndc)
    WHERE deleted_at IS NULL AND ndc IS NOT NULL;

-- +goose Down
DROP INDEX CONCURRENTLY IF EXISTS medications_active_ndc_key;
