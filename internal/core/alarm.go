package core

import "time"

// Severity describes alarm urgency. Higher severities dominate grouped alarms.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Alarm is published to SubjectAlarmRaised by adapters or core internals.
type Alarm struct {
	ID        string            `json:"id"`
	AssetID   string            `json:"asset_id"`
	Severity  Severity          `json:"severity"`
	Code      string            `json:"code,omitempty"`
	Message   string            `json:"message,omitempty"`
	Source    string            `json:"source,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// AlarmImpact is published immediately after an alarm is accepted.
type AlarmImpact struct {
	Alarm             Alarm        `json:"alarm"`
	AffectedAssets    []*AssetNode `json:"affected_assets,omitempty"`
	AffectedAssetIDs  []string     `json:"affected_asset_ids,omitempty"`
	ConnectedAssets   []*AssetNode `json:"connected_assets,omitempty"`
	ConnectedAssetIDs []string     `json:"connected_asset_ids,omitempty"`
	ComputedAt        time.Time    `json:"computed_at"`
	MaxDepth          int          `json:"max_depth"`
}

// AlarmGroup is published when the flooding suppression window closes.
type AlarmGroup struct {
	ID                string    `json:"id"`
	GroupAssetID      string    `json:"group_asset_id"`
	GroupAssetName    string    `json:"group_asset_name,omitempty"`
	GroupTemplateName string    `json:"group_template_name,omitempty"`
	Severity          Severity  `json:"severity"`
	AlarmCount        int       `json:"alarm_count"`
	AlarmIDs          []string  `json:"alarm_ids"`
	AssetIDs          []string  `json:"asset_ids"`
	Alarms            []Alarm   `json:"alarms"`
	WindowStartedAt   time.Time `json:"window_started_at"`
	WindowEndedAt     time.Time `json:"window_ended_at"`
	CreatedAt         time.Time `json:"created_at"`
}

func (s Severity) IsValid() bool {
	switch s {
	case SeverityInfo, SeverityWarning, SeverityCritical:
		return true
	default:
		return false
	}
}

func (s Severity) rank() int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

func maxAlarmSeverity(alarms []Alarm) Severity {
	max := SeverityInfo
	for _, alarm := range alarms {
		if alarm.Severity.rank() > max.rank() {
			max = alarm.Severity
		}
	}
	return max
}
