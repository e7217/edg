DROP INDEX IF EXISTS idx_assets_source;

ALTER TABLE assets DROP COLUMN updated_at;
ALTER TABLE assets DROP COLUMN attributes;
ALTER TABLE assets DROP COLUMN source;
ALTER TABLE assets DROP COLUMN external_ids;
