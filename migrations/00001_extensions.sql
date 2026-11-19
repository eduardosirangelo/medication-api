-- +goose Up
-- pg_trgm powers the case-insensitive substring search on medication names
-- (GIN trigram index, added with the medications table). It is a "trusted"
-- extension since PostgreSQL 13, so the database owner can create it without
-- superuser rights.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- +goose Down
DROP EXTENSION IF EXISTS pg_trgm;
