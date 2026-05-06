CREATE TABLE IF NOT EXISTS assets (
	id TEXT PRIMARY KEY,
	name TEXT UNIQUE NOT NULL,
	template_name TEXT,
	labels TEXT NOT NULL DEFAULT '[]',
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_assets_name ON assets(name);
CREATE INDEX IF NOT EXISTS idx_assets_template ON assets(template_name);

CREATE TABLE IF NOT EXISTS asset_relations (
	id TEXT PRIMARY KEY,
	source_asset_id TEXT NOT NULL,
	target_asset_id TEXT NOT NULL,
	relation_type TEXT NOT NULL,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	metadata TEXT,
	FOREIGN KEY (source_asset_id) REFERENCES assets(id) ON DELETE CASCADE,
	FOREIGN KEY (target_asset_id) REFERENCES assets(id) ON DELETE CASCADE,
	UNIQUE (source_asset_id, target_asset_id, relation_type)
);

CREATE INDEX IF NOT EXISTS idx_relations_source ON asset_relations(source_asset_id);
CREATE INDEX IF NOT EXISTS idx_relations_target ON asset_relations(target_asset_id);
CREATE INDEX IF NOT EXISTS idx_relations_type ON asset_relations(relation_type);
