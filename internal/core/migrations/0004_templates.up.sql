CREATE TABLE IF NOT EXISTS templates (
	name TEXT PRIMARY KEY,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS template_resources (
	template_name TEXT NOT NULL,
	name TEXT NOT NULL,
	value_type TEXT NOT NULL,
	unit TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (template_name, name),
	FOREIGN KEY (template_name) REFERENCES templates(name) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS template_constraints (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	template_name TEXT NOT NULL,
	kind TEXT NOT NULL CHECK (kind IN ('required', 'forbidden')),
	relation_type TEXT NOT NULL,
	target_template TEXT NOT NULL,
	min_count INTEGER,
	max_count INTEGER,
	FOREIGN KEY (template_name) REFERENCES templates(name) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_template_constraints_tmpl ON template_constraints(template_name);
