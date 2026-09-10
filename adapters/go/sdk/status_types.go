package sdk

import "time"

// Wire types for the adapter runtime-status plane (ADR 0008).
//
// These mirror internal/core/adapter.go. The SDK is a separate Go module and
// cannot import internal/, so the definitions are duplicated deliberately —
// the same arrangement subjects.go already uses for subject strings. The
// duplication is held honest by a golden JSON fixture that both this module
// and the Python SDK parse, so a field that drifts on one side fails a test
// rather than silently producing frames core cannot read.

// AdapterSchemaVersion is the wire version of AdapterStatusFrame.
const AdapterSchemaVersion = 1

// RunState is the adapter process lifecycle, distinct from the device link.
type RunState string

const (
	RunStateStarting RunState = "starting"
	RunStateRunning  RunState = "running"
	RunStateDegraded RunState = "degraded"
	RunStateStopping RunState = "stopping"
	RunStateStopped  RunState = "stopped"
)

// Frame phases.
const (
	AdapterPhaseOnline    = "online"
	AdapterPhaseHeartbeat = "heartbeat"
	AdapterPhaseAnnounce  = "announce"
	AdapterPhaseProbe     = "probe"
	AdapterPhaseOffline   = "offline"
)

// AdapterCounters are monotonic totals; core differences them.
type AdapterCounters struct {
	PublishedTotal           int64 `json:"published_total"`
	CollectErrorsTotal       int64 `json:"collect_errors_total"`
	DeviceErrorsTotal        int64 `json:"device_errors_total"`
	DeviceReconnectsTotal    int64 `json:"device_reconnects_total"`
	NATSReconnectsTotal      int64 `json:"nats_reconnects_total"`
	ConsecutiveCollectErrors int64 `json:"consecutive_collect_errors"`
}

// AdapterAssetStatus is the per-asset detail.
type AdapterAssetStatus struct {
	AssetID            string     `json:"asset_id"`
	DeviceState        string     `json:"device_state,omitempty"`
	PublishedTotal     int64      `json:"published_total"`
	CollectErrorsTotal int64      `json:"collect_errors_total"`
	LastError          string     `json:"last_error,omitempty"`
	LastErrorAt        *time.Time `json:"last_error_at,omitempty"`
}

// AdapterStatusFrame is published on platform.adapter.status.<adapter_id>.
type AdapterStatusFrame struct {
	SchemaVersion      int      `json:"schema_version"`
	AdapterID          string   `json:"adapter_id"`
	InstanceID         string   `json:"instance_id"`
	Seq                int64    `json:"seq"`
	Phase              string   `json:"phase"`
	RunState           RunState `json:"run_state"`
	DeviceState        string   `json:"device_state,omitempty"`
	HeartbeatIntervalS int      `json:"heartbeat_interval_s"`

	UptimeS   int64     `json:"uptime_s"`
	StartedAt time.Time `json:"started_at"`
	// SentAt is diagnostic only: core judges liveness by its own receive time,
	// so a wrong clock here cannot make a live adapter look dead.
	SentAt time.Time `json:"sent_at"`

	SDK            string `json:"sdk,omitempty"`
	AdapterVersion string `json:"adapter_version,omitempty"`
	Host           string `json:"host,omitempty"`
	PID            int    `json:"pid,omitempty"`

	Capabilities  []string `json:"capabilities,omitempty"`
	ConfigVersion int      `json:"config_version"`

	Assets          []AdapterAssetStatus `json:"assets,omitempty"`
	AssetsTruncated bool                 `json:"assets_truncated,omitempty"`
	DeviceCounts    map[string]int       `json:"device_counts,omitempty"`

	Counters AdapterCounters `json:"counters"`
}
