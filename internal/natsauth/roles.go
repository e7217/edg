// Package natsauth builds the authorization tables for EDG's embedded NATS
// server: one account, a fixed set of roles, and a per-role subject permission
// matrix.
//
// The package deliberately does not import internal/core. Subject strings are
// duplicated here so that the authorization table is reviewable on its own;
// internal/natsauth/subjects_test.go imports internal/core and asserts that
// every subject the core actually serves is classified in exactly one bucket,
// so the duplication cannot silently rot.
package natsauth

import (
	"strings"

	"github.com/nats-io/nats-server/v2/server"
)

// Roles. Every connection to the embedded server authenticates as exactly one
// of these.
const (
	// RoleCore is edg-core itself. Allow-all; the credential never touches disk.
	RoleCore = "core"
	// RoleOperator may mutate master data. This is the human/administrative role.
	RoleOperator = "operator"
	// RoleAdapter may publish telemetry and read master data, but may not
	// mutate it. See ADR 0007 and issue #100 (auto-registration removal).
	RoleAdapter = "adapter"
	// RoleFanout may attach a durable JetStream consumer to PLATFORM_DATA and
	// nothing else. This is the ADR 0006 Option A integration path.
	RoleFanout = "fanout"
	// RoleLegacy is the compat-mode anonymous identity: adapter ∪ fanout.
	// It exists so that pre-auth deployments keep working for one release.
	RoleLegacy = "legacy"
)

// Authorization modes.
const (
	// ModeCompat defines Users and maps anonymous connections to RoleLegacy.
	// Authorization is enforced; authentication is not yet required.
	ModeCompat = "compat"
	// ModeStrict defines Users and rejects anonymous connections.
	ModeStrict = "strict"
	// ModeOff defines no Users at all. Deprecated; permitted only on a
	// loopback binding.
	ModeOff = "off"
)

// Modes returns the valid authorization modes, for config validation and error
// messages.
func Modes() []string { return []string{ModeCompat, ModeStrict, ModeOff} }

// Subject groups. Allow lists never use wildcards: `platform.meta.asset.*`
// would silently include create/update/delete. Subjects are enumerated one by
// one, and the dangerous ones are additionally listed in Deny — deny wins over
// allow, so widening an allow list later cannot re-open them by accident.
var (
	// telemetryPublish is the adapter-to-core data plane hop (ADR 0001).
	telemetryPublish = []string{
		"platform.data.asset",
		"platform.alarm.raised",
	}

	// metaReadPublish are the request/reply subjects that only read.
	metaReadPublish = []string{
		"platform.meta.asset.get",
		"platform.meta.asset.list",
		"platform.meta.asset.ancestors",
		"platform.meta.asset.descendants",
		"platform.meta.asset.subtree",
		"platform.meta.asset.connected",
		"platform.meta.relation.get",
		"platform.meta.relation.list",
		"platform.meta.template.list",
		"platform.meta.constraints.check",
	}

	// metaWritePublish are the request/reply subjects that mutate master data.
	metaWritePublish = []string{
		"platform.meta.asset.create",
		"platform.meta.asset.update",
		"platform.meta.asset.delete",
		"platform.meta.relation.create",
		"platform.meta.relation.delete",
	}

	// coreOnlyPublish are subjects only edg-core may ever publish. Publishing
	// these from outside forges core-accepted data (validated), poisons the
	// enrichment cache (changed), or fakes analysis output.
	coreOnlyPublish = []string{
		"platform.data.validated",
		"platform.data.deadletter",
		"platform.meta.asset.changed",
		"platform.meta.relation.changed",
		"platform.meta.constraints.violation",
		"platform.alarm.impact.computed",
		"platform.alarm.grouped",
	}

	// destructiveJetStreamDeny are stream/consumer operations that can destroy
	// the data plane. Denied to everyone except core and operator.
	destructiveJetStreamDeny = []string{
		"$JS.API.STREAM.DELETE.>",
		"$JS.API.STREAM.PURGE.>",
		"$JS.API.STREAM.UPDATE.>",
		"$JS.API.CONSUMER.DELETE.>",
		"$JS.API.ACCOUNT.>",
	}

	// systemDeny is never allowed to any EDG role.
	systemDeny = []string{"$SYS.>"}
)

// DefaultStreamName mirrors internal/core.DefaultCoreConfig's
// jetstream.stream.name. It is only a fallback: callers pass the configured
// name so that renaming the stream does not silently strip the fanout role of
// its permissions.
const DefaultStreamName = "PLATFORM_DATA"

// ValidStreamName reports whether a JetStream stream name can be embedded in a
// subject token. nats-server already rejects dots and wildcards in stream
// names; we re-check because a bad name here would produce a malformed
// permission subject rather than an obvious error.
func ValidStreamName(name string) bool {
	if name == "" {
		return false
	}
	return !strings.ContainsAny(name, ". \t*>")
}

// fanoutJetStreamPublish is the minimal JetStream API set required to create
// and drain a durable pull consumer on one stream. Verified by experiment:
// PullSubscribe -> Fetch -> Ack all succeed with exactly this set;
// STREAM.DELETE/PURGE are not needed.
func fanoutJetStreamPublish(stream string) []string {
	return []string{
		"$JS.API.INFO",
		"$JS.API.STREAM.INFO." + stream,
		"$JS.API.CONSUMER.CREATE." + stream + ".>",
		"$JS.API.CONSUMER.DURABLE.CREATE." + stream + ".>", // legacy CLI / Benthos
		"$JS.API.CONSUMER.INFO." + stream + ".>",
		"$JS.API.CONSUMER.MSG.NEXT." + stream + ".>",
		"$JS.ACK.>",
	}
}

// Permissions returns the subject permissions for a role, or nil for RoleCore
// (nil means allow-all in nats-server). An unknown role returns a
// deny-everything permission set rather than nil, so a typo fails closed.
//
// stream is the JetStream stream the fanout and legacy roles may consume; an
// empty or invalid value falls back to DefaultStreamName.
func Permissions(role, stream string) *server.Permissions {
	if !ValidStreamName(stream) {
		stream = DefaultStreamName
	}
	switch role {
	case RoleCore:
		return nil

	case RoleOperator:
		return &server.Permissions{
			Publish: &server.SubjectPermission{
				Allow: concat(telemetryPublish, metaReadPublish, metaWritePublish, []string{"$JS.API.>"}),
				Deny:  concat(coreOnlyPublish, systemDeny, []string{"_INBOX.>"}),
			},
			Subscribe: &server.SubjectPermission{
				Allow: []string{"platform.>", "_INBOX.>"},
				Deny:  systemDeny,
			},
		}

	case RoleAdapter:
		return &server.Permissions{
			Publish: &server.SubjectPermission{
				Allow: concat(telemetryPublish, metaReadPublish),
				Deny: concat(metaWritePublish, coreOnlyPublish, destructiveJetStreamDeny, systemDeny,
					[]string{"$JS.API.>", "_INBOX.>"}),
			},
			Subscribe: &server.SubjectPermission{
				Allow: []string{"platform.>", "_INBOX.>"},
				Deny:  systemDeny,
			},
		}

	case RoleFanout:
		return &server.Permissions{
			Publish: &server.SubjectPermission{
				Allow: fanoutJetStreamPublish(stream),
				Deny: concat(destructiveJetStreamDeny, systemDeny,
					// A fan-out consumer is a pure reader; it must never write
					// anything back into the platform namespace.
					[]string{"platform.>"}),
			},
			Subscribe: &server.SubjectPermission{
				Allow: []string{"platform.data.>", "_INBOX.>"},
				Deny:  systemDeny,
			},
		}

	case RoleLegacy:
		// adapter ∪ fanout. Without the fanout half, enabling compat mode would
		// disconnect any existing ADR 0006 durable consumer that connects
		// anonymously today — breaking the "no flag day" promise.
		return &server.Permissions{
			Publish: &server.SubjectPermission{
				Allow: concat(telemetryPublish, metaReadPublish, fanoutJetStreamPublish(stream)),
				Deny: concat(metaWritePublish, coreOnlyPublish, destructiveJetStreamDeny, systemDeny,
					[]string{"_INBOX.>"}),
			},
			Subscribe: &server.SubjectPermission{
				Allow: []string{"platform.>", "_INBOX.>"},
				Deny:  systemDeny,
			},
		}

	default:
		return &server.Permissions{
			Publish:   &server.SubjectPermission{Deny: []string{">"}},
			Subscribe: &server.SubjectPermission{Deny: []string{">"}},
		}
	}
}

func concat(lists ...[]string) []string {
	n := 0
	for _, l := range lists {
		n += len(l)
	}
	out := make([]string, 0, n)
	for _, l := range lists {
		out = append(out, l...)
	}
	return out
}
