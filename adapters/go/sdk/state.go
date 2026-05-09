package sdk

// DeviceState describes the lifecycle state of an external device the adapter
// is collecting from. The underlying NATS connection state is independent.
type DeviceState string

const (
	DeviceDisconnected DeviceState = "disconnected"
	DeviceConnecting   DeviceState = "connecting"
	DeviceConnected    DeviceState = "connected"
	DeviceReconnecting DeviceState = "reconnecting"
	DeviceError        DeviceState = "error"
)

// Quality values for TagValue.Quality. EDG Core accepts free-form strings; the
// constants below match the tokens used by the Python SDK and the reference
// adapters.
const (
	QualityGood      = "GOOD"
	QualityBad       = "BAD"
	QualityUncertain = "UNCERTAIN"
)

// Source values for asset registration. Matches internal/core/metadata.go.
const (
	SourceManual = "manual"
	SourceAuto   = "auto"
)
