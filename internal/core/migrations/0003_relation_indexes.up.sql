CREATE INDEX IF NOT EXISTS idx_relations_source_type ON asset_relations(source_asset_id, relation_type);
CREATE INDEX IF NOT EXISTS idx_relations_target_type ON asset_relations(target_asset_id, relation_type);
