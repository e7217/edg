package core

import (
	"regexp"
	"time"
)

// AdapterSchemaVersion is the wire version of AdapterStatusFrame. It follows
// the MetaChangeEvent precedent (events.go): the field is always present so a
// consumer can branch on it without guessing.
const AdapterSchemaVersion = 1

// Availability is the only axis edg-core derives itself. Adapters report what
// they believe about themselves (RunState, DeviceState); whether anyone can
// still hear them is a judgement only the receiver can make, so an adapter
// cannot assert it.
type Availability string

const (
	// AvailabilityOnline means a status frame arrived within the deadline.
	AvailabilityOnline Availability = "online"
	// AvailabilityStale means the deadline passed and, if probing is enabled,
	// an active probe also went unanswered.
	AvailabilityStale Availability = "stale"
	// AvailabilityOffline means the adapter said goodbye (phase "offline").
	AvailabilityOffline Availability = "offline"
	// AvailabilityUnknown is reserved for the declaration layer (P2): an
	// adapter that is declared but has never been heard from. Never published
	// in this phase.
	AvailabilityUnknown Availability = "unknown"
)

// RunState is the adapter process lifecycle, corresponding to Neuron's
// running_state. Distinct from DeviceState (its link to the equipment): an
// adapter can be running while its device is disconnected, and degraded while
// its device still reports connected.
type RunState string

const (
	RunStateStarting RunState = "starting"
	RunStateRunning  RunState = "running"
	// RunStateDegraded is derived by the SDK from consecutive collect errors.
	// It is deliberately separate from the device link: a PLC that answers but
	// returns garbage leaves device_state connected.
	RunStateDegraded RunState = "degraded"
	RunStateStopping RunState = "stopping"
	RunStateStopped  RunState = "stopped"
)

// Frame phases. The phase says why a frame was sent, which lets the registry
// distinguish a graceful shutdown from silence.
const (
	AdapterPhaseOnline    = "online"
	AdapterPhaseHeartbeat = "heartbeat"
	AdapterPhaseAnnounce  = "announce"
	AdapterPhaseProbe     = "probe"
	AdapterPhaseOffline   = "offline"
)

// Change event kinds published on SubjectAdapterChanged.
const (
	AdapterChangeOnline    = "online"
	AdapterChangeUpdated   = "updated"
	AdapterChangeStale     = "stale"
	AdapterChangeOffline   = "offline"
	AdapterChangeReplaced  = "replaced"
	AdapterChangeForgotten = "forgotten"
)

// adapterIDPattern constrains an adapter_id to a single NATS subject token:
// it is the last token of platform.adapter.status.<id>, so a dot would silently
// reshape the subject and let one adapter impersonate another's namespace.
var adapterIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_:\-]{0,63}$`)

// reservedAdapterIDs would collide with sibling subjects or route segments.
var reservedAdapterIDs = map[string]bool{
	"drift": true, "list": true, "hello": true,
}

// IsValidAdapterID reports whether id is usable as a subject token and a URL
// path segment.
func IsValidAdapterID(id string) bool {
	if reservedAdapterIDs[id] {
		return false
	}
	return adapterIDPattern.MatchString(id)
}

// IsValidRunState reports whether s is a known run state.
func IsValidRunState(s RunState) bool {
	switch s {
	case RunStateStarting, RunStateRunning, RunStateDegraded, RunStateStopping, RunStateStopped:
		return true
	default:
		return false
	}
}

// IsValidAvailability reports whether a is a known availability.
func IsValidAvailability(a Availability) bool {
	switch a {
	case AvailabilityOnline, AvailabilityStale, AvailabilityOffline, AvailabilityUnknown:
		return true
	default:
		return false
	}
}

// AdapterCounters are monotonic totals the adapter maintains. The registry
// differences consecutive observations rather than reading them absolutely, so
// a restart (detected via instance_id or a seq regression) resets cleanly.
type AdapterCounters struct {
	PublishedTotal           int64 `json:"published_total"`
	CollectErrorsTotal       int64 `json:"collect_errors_total"`
	DeviceErrorsTotal        int64 `json:"device_errors_total"`
	DeviceReconnectsTotal    int64 `json:"device_reconnects_total"`
	NATSReconnectsTotal      int64 `json:"nats_reconnects_total"`
	ConsecutiveCollectErrors int64 `json:"consecutive_collect_errors"`
}

// AdapterAssetStatus is the per-asset detail an adapter reports. Omitted when
// AssetsTruncated is set on the frame.
type AdapterAssetStatus struct {
	AssetID            string     `json:"asset_id"`
	DeviceState        string     `json:"device_state,omitempty"`
	PublishedTotal     int64      `json:"published_total"`
	CollectErrorsTotal int64      `json:"collect_errors_total"`
	LastError          string     `json:"last_error,omitempty"`
	LastErrorAt        *time.Time `json:"last_error_at,omitempty"`
}

// AdapterStatusFrame is what an adapter publishes on
// platform.adapter.status.<adapter_id>.
//
// The subject's last token is authoritative for identity: a frame whose
// AdapterID disagrees with it is dropped, so an adapter cannot claim to be
// another by lying in the body.
type AdapterStatusFrame struct {
	SchemaVersion int    `json:"schema_version"`
	AdapterID     string `json:"adapter_id"`
	// InstanceID is fresh per process run. It detects restarts and, when two
	// processes share an adapter_id, split brain.
	InstanceID string `json:"instance_id"`
	// Seq increments per frame within an instance. A regression means the
	// adapter restarted without a new InstanceID, or frames were reordered.
	Seq   int64  `json:"seq"`
	Phase string `json:"phase"`

	RunState    RunState `json:"run_state"`
	DeviceState string   `json:"device_state,omitempty"`

	// HeartbeatIntervalS is announced in band so the registry can derive a
	// deadline that fits a 15-minute batch collector and a 1-second poller
	// without central configuration.
	HeartbeatIntervalS int `json:"heartbeat_interval_s"`

	UptimeS   int64     `json:"uptime_s"`
	StartedAt time.Time `json:"started_at"`
	// SentAt is diagnostic only. It must never appear in a branch condition:
	// expiry is judged solely by the registry's own clock, so a wrong adapter
	// clock cannot make a live adapter look dead.
	SentAt time.Time `json:"sent_at"`

	SDK            string `json:"sdk,omitempty"`
	AdapterVersion string `json:"adapter_version,omitempty"`
	Host           string `json:"host,omitempty"`
	PID            int    `json:"pid,omitempty"`

	// Capabilities gates future features (e.g. "ping") without a schema bump.
	Capabilities []string `json:"capabilities,omitempty"`
	// ConfigVersion is always 0 in this phase. Reserved for the declaration
	// layer's desired-vs-applied convergence check.
	ConfigVersion int `json:"config_version"`

	Assets          []AdapterAssetStatus `json:"assets,omitempty"`
	AssetsTruncated bool                 `json:"assets_truncated,omitempty"`
	DeviceCounts    map[string]int       `json:"device_counts,omitempty"`

	Counters AdapterCounters `json:"counters"`
}

// AssetIDs returns the assets named in the frame.
func (f *AdapterStatusFrame) AssetIDs() []string {
	ids := make([]string, 0, len(f.Assets))
	for _, a := range f.Assets {
		ids = append(ids, a.AssetID)
	}
	return ids
}

// HasCapability reports whether the adapter advertised a capability.
func (f *AdapterStatusFrame) HasCapability(name string) bool {
	for _, c := range f.Capabilities {
		if c == name {
			return true
		}
	}
	return false
}

// AdapterEntry is the registry's view of one adapter: the last frame plus what
// the registry derived from observing it.
type AdapterEntry struct {
	AdapterID    string       `json:"adapter_id"`
	InstanceID   string       `json:"instance_id"`
	Availability Availability `json:"availability"`
	RunState     RunState     `json:"run_state"`
	DeviceState  string       `json:"device_state,omitempty"`

	// FirstSeenAt and LastSeenAt are the registry's receive times, not the
	// adapter's claims.
	FirstSeenAt time.Time `json:"first_seen_at"`
	LastSeenAt  time.Time `json:"last_seen_at"`
	// DeadlineAt is when LastSeenAt goes stale.
	DeadlineAt time.Time `json:"deadline_at"`
	// StaleSince is set while Availability is stale or offline.
	StaleSince *time.Time `json:"stale_since,omitempty"`

	HeartbeatIntervalS int `json:"heartbeat_interval_s"`

	// ObservedPublishHz is derived from the PublishedTotal delta over the
	// receive-time delta, so it is correct even when the adapter's clock is
	// wrong. It answers "is the gateway falling behind".
	ObservedPublishHz float64 `json:"observed_publish_hz"`
	// ClockSkewS is display-only: SentAt minus the receive time.
	ClockSkewS float64 `json:"clock_skew_s"`

	AssetIDs     []string             `json:"asset_ids,omitempty"`
	Assets       []AdapterAssetStatus `json:"assets,omitempty"`
	DeviceCounts map[string]int       `json:"device_counts,omitempty"`
	Counters     AdapterCounters      `json:"counters"`

	SDK            string   `json:"sdk,omitempty"`
	AdapterVersion string   `json:"adapter_version,omitempty"`
	Host           string   `json:"host,omitempty"`
	PID            int      `json:"pid,omitempty"`
	Capabilities   []string `json:"capabilities,omitempty"`
	ConfigVersion  int      `json:"config_version"`
}

// AdapterChangeEvent is published on SubjectAdapterChanged, and only on an
// actual transition. Heartbeats that change nothing observable produce no
// event, so a fleet at steady state generates approximately zero event traffic
// regardless of heartbeat rate.
type AdapterChangeEvent struct {
	SchemaVersion int           `json:"schema_version"`
	Change        string        `json:"change"`
	Reason        string        `json:"reason,omitempty"`
	Timestamp     time.Time     `json:"timestamp"`
	Adapter       *AdapterEntry `json:"adapter,omitempty"`
	// Previous carries the prior availability/run/device state for a change of
	// kind "updated", so a consumer can render a transition without keeping
	// its own copy.
	Previous *AdapterEntrySummary `json:"previous,omitempty"`
}

// AdapterEntrySummary is the before-image in a change event.
type AdapterEntrySummary struct {
	Availability Availability `json:"availability"`
	RunState     RunState     `json:"run_state"`
	DeviceState  string       `json:"device_state,omitempty"`
}

// Clock is injected so expiry can be tested without sleeping. The repository
// has no clock abstraction elsewhere; this is the first, and it exists because
// the reaper is otherwise untestable without real time.
type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

// SystemClock is the production Clock.
var SystemClock Clock = realClock{}
