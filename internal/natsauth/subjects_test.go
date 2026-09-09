package natsauth

import (
	"strings"
	"testing"

	"github.com/nats-io/nats-server/v2/server"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e7217/edg/internal/core"
)

// This file is the only place internal/natsauth depends on internal/core, and
// it is a test-only dependency: production code in this package must stay
// importable without pulling in the core (no cycle, no sqlite).

// TestEveryMetaSubjectIsClassified is the corrosion guard for the ADR 0007
// matrix.
//
// roles.go duplicates the subject strings so that the authorization table can
// be reviewed on its own. That duplication is only safe if adding a subject to
// the metadata plane without classifying it here fails the build. It does:
// core.MetaRequestSubjects() derives from the same map MetaHandler subscribes
// with, so a new subject shows up here the moment it is served.
func TestEveryMetaSubjectIsClassified(t *testing.T) {
	classified := map[string]string{}
	for _, s := range metaReadPublish {
		classified[s] = "read"
	}
	for _, s := range metaWritePublish {
		classified[s] = "write"
	}

	served := core.MetaRequestSubjects()
	require.NotEmpty(t, served, "MetaRequestSubjects must not be empty")

	for _, subject := range served {
		bucket, ok := classified[subject]
		assert.True(t, ok,
			"subject %q is served by MetaHandler but is not classified in roles.go; "+
				"add it to metaReadPublish (adapters may call it) or metaWritePublish (operator only)",
			subject)
		if ok {
			assert.Contains(t, []string{"read", "write"}, bucket)
		}
	}
}

// TestNoStaleSubjectsInMatrix catches the opposite drift: a subject removed
// from the core but left behind in the permission table, which would grant
// access to something that no longer exists and mislead the next reader.
func TestNoStaleSubjectsInMatrix(t *testing.T) {
	served := map[string]bool{}
	for _, s := range core.MetaRequestSubjects() {
		served[s] = true
	}

	for _, s := range concat(metaReadPublish, metaWritePublish) {
		assert.True(t, served[s],
			"roles.go classifies %q but MetaHandler no longer serves it; remove it from the matrix", s)
	}
}

// TestCoreOnlySubjectsMatchEventConstants pins the publish-side subjects that
// only edg-core may emit against the constants it actually publishes with.
func TestCoreOnlySubjectsMatchEventConstants(t *testing.T) {
	mustBeCoreOnly := []string{
		core.SubjectAssetChanged,
		core.SubjectRelationChanged,
		core.SubjectConstraintsViolation,
		core.SubjectAlarmImpactComputed,
		core.SubjectAlarmGrouped,
		core.DefaultValidatedDataSubject,
		core.DefaultDeadLetterSubject,
	}

	inMatrix := map[string]bool{}
	for _, s := range coreOnlyPublish {
		inMatrix[s] = true
	}

	for _, s := range mustBeCoreOnly {
		assert.True(t, inMatrix[s],
			"%q is published by edg-core and must be denied to every other role", s)
	}
}

// TestAdapterInboundSubjectIsPublishable guards the one data-plane subject
// adapters must always be able to reach (ADR 0001's adapter-to-core hop).
func TestAdapterInboundSubjectIsPublishable(t *testing.T) {
	allowed := map[string]bool{}
	for _, s := range telemetryPublish {
		allowed[s] = true
	}
	assert.True(t, allowed["platform.data.asset"],
		"the adapter-to-core hop must stay open or every adapter breaks")
	assert.True(t, allowed[core.SubjectAlarmRaised],
		"USER_GUIDE documents adapters raising alarms on this subject")
}

// TestAdapterPlaneIsGranted is the regression guard for a real outage: the
// adapter runtime-status plane (ADR 0008) shipped while every role's Allow
// list omitted it, so under the default compat mode every status frame was a
// permissions violation and the whole feature was dead on arrival. A non-empty
// Allow list in nats-server is exclusive, so "we forgot to add it" and "we
// deliberately denied it" look identical from inside the matrix.
func TestAdapterPlaneIsGranted(t *testing.T) {
	adapterMustPublish := []string{
		core.SubjectAdapterStatusPrefix + "modbus-1",
		core.SubjectAdapterPongPrefix + "modbus-1.abc123",
		core.SubjectAdapterList,
	}

	for _, role := range []string{RoleAdapter, RoleLegacy} {
		t.Run(role, func(t *testing.T) {
			perms := Permissions(role, testStream)
			require.NotNil(t, perms)
			for _, subject := range adapterMustPublish {
				assert.True(t, subjectAllowed(perms.Publish, subject),
					"%s must be able to publish %s or the runtime-status plane is dead", role, subject)
			}
		})
	}
}

// TestAdapterVerdictSubjectsAreCoreOnly: an adapter must not be able to
// announce its own availability, and a forged hello would let anyone trigger a
// fleet-wide announce storm.
func TestAdapterVerdictSubjectsAreCoreOnly(t *testing.T) {
	for _, subject := range []string{core.SubjectAdapterChanged, core.SubjectAdapterHello} {
		inMatrix := false
		for _, s := range coreOnlyPublish {
			if s == subject {
				inMatrix = true
			}
		}
		assert.True(t, inMatrix, "%q must be denied to every role but core", subject)
	}
}

// subjectAllowed mirrors nats-server's matching closely enough for these
// assertions: an explicit entry, or a `>` wildcard whose prefix matches.
func subjectAllowed(p *server.SubjectPermission, subject string) bool {
	if p == nil {
		return true
	}
	for _, deny := range p.Deny {
		if subjectMatches(deny, subject) {
			return false
		}
	}
	if len(p.Allow) == 0 {
		return true
	}
	for _, allow := range p.Allow {
		if subjectMatches(allow, subject) {
			return true
		}
	}
	return false
}

func subjectMatches(pattern, subject string) bool {
	if pattern == subject {
		return true
	}
	if strings.HasSuffix(pattern, ">") {
		return strings.HasPrefix(subject, strings.TrimSuffix(pattern, ">"))
	}
	return false
}
