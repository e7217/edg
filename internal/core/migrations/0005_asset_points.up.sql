-- Per-asset point declarations: which address on a field device maps to which
-- tag name, and how to decode it.
--
-- Keyed on asset_id, not on template_name and not on adapter_id.
--
-- Not template_name: template_resources(template_name, name) declares what a
-- KIND of thing reports, so two pumps sharing pump-v1 cannot have different
-- Modbus addresses. That table stays as it is; this one declares where this
-- specific box's copy lives.
--
-- Not adapter_id: adapter identity is volatile in-memory registry state with no
-- persistence (ADR 0008), so keying on it would require adapter declarations to
-- become master data first. asset_id is already a primary key and is already
-- the join key adapters report in their status frames.
CREATE TABLE IF NOT EXISTS asset_point_lists (
	asset_id TEXT PRIMARY KEY,
	-- Opaque to core: 'modbus-tcp', 'opcua', whatever the adapter understands.
	protocol TEXT NOT NULL,
	-- NULL leaves the adapter's own default in place.
	poll_interval_ms INTEGER,
	-- Monotonic, bumped on every write. Phase 2 uses it as the convergence
	-- signal against the config_version ADR 0008 already reserves.
	version INTEGER NOT NULL DEFAULT 1,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	FOREIGN KEY (asset_id) REFERENCES assets(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS asset_points (
	asset_id TEXT NOT NULL,
	-- Matches TagValue.Name on the data plane, which is how a declared point is
	-- resolvable from (asset_id, name) alone.
	name TEXT NOT NULL,
	value_type TEXT NOT NULL CHECK (value_type IN ('NUMBER', 'TEXT', 'FLAG')),
	unit TEXT NOT NULL DEFAULT '',
	-- Opaque to core: "0", "40001", "ns=2;s=Temperature".
	address TEXT NOT NULL,
	-- Protocol-specific decoding as a JSON object: function, type, word_order,
	-- scale for Modbus. Core validates that it is an object and nothing more;
	-- giving protocol types a home in the schema would mean a per-protocol type
	-- system in the core before a second protocol exists.
	encoding TEXT NOT NULL DEFAULT '{}',
	enabled INTEGER NOT NULL DEFAULT 1,
	created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
	PRIMARY KEY (asset_id, name),
	FOREIGN KEY (asset_id) REFERENCES asset_point_lists(asset_id) ON DELETE CASCADE
);

-- Answers "which assets declare a tag by this name", the search an operator
-- runs when reconciling a plant-wide naming convention.
CREATE INDEX IF NOT EXISTS idx_asset_points_name ON asset_points(name);
