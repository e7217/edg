ALTER TABLE assets ADD COLUMN external_ids TEXT NOT NULL DEFAULT '{}';
ALTER TABLE assets ADD COLUMN source TEXT NOT NULL DEFAULT 'manual';
ALTER TABLE assets ADD COLUMN attributes TEXT NOT NULL DEFAULT '{}';
ALTER TABLE assets ADD COLUMN updated_at DATETIME;

UPDATE assets SET updated_at = created_at WHERE updated_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_assets_source ON assets(source);
