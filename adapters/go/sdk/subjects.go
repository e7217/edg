package sdk

// NATS subjects exposed by EDG Core. These mirror the constants in
// internal/core/handler.go and internal/core/meta_handler.go and form the
// public wire contract for adapters.
const (
	// Data plane.
	SubjectAssetData = "platform.data.asset"

	// Asset metadata request/reply.
	SubjectAssetCreate  = "platform.meta.asset.create"
	SubjectAssetGet     = "platform.meta.asset.get"
	SubjectAssetList    = "platform.meta.asset.list"
	SubjectAssetUpdate  = "platform.meta.asset.update"
	SubjectAssetDelete  = "platform.meta.asset.delete"
	SubjectTemplateList = "platform.meta.template.list"

	// Asset relation request/reply.
	SubjectRelationCreate = "platform.meta.relation.create"
	SubjectRelationGet    = "platform.meta.relation.get"
	SubjectRelationList   = "platform.meta.relation.list"
	SubjectRelationDelete = "platform.meta.relation.delete"

	// Metadata change events (best-effort plain NATS publishes).
	SubjectAssetChanged    = "platform.meta.asset.changed"
	SubjectRelationChanged = "platform.meta.relation.changed"
	SubjectMetaChangedAll  = "platform.meta.*.changed"
)
