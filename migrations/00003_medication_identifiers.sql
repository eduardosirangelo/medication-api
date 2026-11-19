-- +goose Up
-- Nullable columns without defaults are a metadata-only change in PostgreSQL:
-- no table rewrite, a brief lock, and the previous app version (which never
-- reads them) keeps working during a rolling deploy.
ALTER TABLE medications
    ADD COLUMN ndc   text CHECK (ndc ~ '^[0-9]{11}$'),   -- normalized 11-digit 5-4-2, no hyphens
    ADD COLUMN rxcui text CHECK (rxcui ~ '^[0-9]{1,10}$'),
    ADD COLUMN route text CHECK (route IN (
        'oral', 'sublingual', 'topical', 'transdermal', 'inhalation', 'nasal',
        'ophthalmic', 'otic', 'rectal', 'vaginal',
        'intravenous', 'intramuscular', 'subcutaneous'));

-- +goose Down
ALTER TABLE medications
    DROP COLUMN route,
    DROP COLUMN rxcui,
    DROP COLUMN ndc;
