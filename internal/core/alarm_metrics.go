package core

import "github.com/e7217/edg/internal/metrics"

// Alarm-path instrumentation.
//
// The aggregator is where this system degrades non-linearly: Add holds a.mu
// for its whole duration and findGroupForAssetLocked walks every open group,
// running a recursive lowest-common-ancestor query against SQLite for each
// one. The cost is quadratic-ish in the number of open groups, and until now
// the only symptom was that alarm handling got slow.
var (
	alarmsReceived = metrics.Default.NewCounterVec(metrics.Desc{
		Name: "edg_core_alarm_received_total",
		Help: "Alarms accepted on the raised subject, by severity.",
	}, "severity", []string{
		string(SeverityInfo), string(SeverityWarning), string(SeverityCritical),
	})

	alarmsInvalid = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_alarm_invalid_total",
		Help: "Alarm payloads rejected as malformed or failing validation. They are dropped.",
	})

	alarmImpactFailures = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_alarm_impact_failures_total",
		Help: "Impact computations that failed, usually because the alarm names an asset with no master-data record.",
	})

	alarmImpactAffected = metrics.Default.NewHistogram(metrics.Desc{
		Name: "edg_core_alarm_impact_affected_assets",
		Help: "Assets in one alarm's computed impact set. A long tail means the traversal depth limit is doing real work. Buckets are provisional.",
	}, []float64{1, 2, 5, 10, 25, 50, 100, 250, 500, 1000})

	alarmGroupsPending = metrics.Default.NewGauge(metrics.Desc{
		Name: "edg_core_alarm_groups_pending",
		Help: "Alarm groups open in the aggregation window. Every Add runs one ancestor query per open group while holding the aggregator lock, so a climbing value is the early warning for an alarm storm stalling the process.",
	})

	alarmLCAQueries = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_alarm_lca_queries_total",
		Help: "Lowest-common-ancestor queries run while holding the aggregator lock. Divide by edg_core_alarm_received_total to see how quickly the per-alarm cost is growing.",
	})

	alarmMerges = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_alarm_merges_total",
		Help: "Times two pending groups were merged because their alarms turned out to share an ancestor.",
	})

	alarmsFlushed = metrics.Default.NewCounter(metrics.Desc{
		Name: "edg_core_alarm_groups_flushed_total",
		Help: "Alarm groups published at the end of their window.",
	})
)
