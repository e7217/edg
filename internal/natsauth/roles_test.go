package natsauth

import (
	"fmt"
	"regexp"
	"sort"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testStream = "PLATFORM_DATA"

// permSubject extracts the subject out of a nats-server permission violation,
// e.g. `nats: Permissions Violation for Publish to "platform.meta.asset.delete"`.
var permSubject = regexp.MustCompile(`Permissions Violation for \w+ to "([^"]+)"`)

// startAuthServer boots an in-process nats-server with the role table applied.
// It mirrors internal/core/handler_jetstream_test.go's startTestNATSServer
// (random port, t.TempDir store, t.Cleanup shutdown) but adds authorization.
func startAuthServer(t *testing.T, mode, stream string) (*natsserver.Server, Credentials) {
	t.Helper()

	creds := Credentials{Operator: "op-secret", Adapter: "ad-secret", Fanout: "fo-secret"}
	core, err := NewEphemeralCore()
	require.NoError(t, err)

	opts := &natsserver.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()}
	require.NoError(t, Apply(opts, Config{Mode: mode, Stream: stream, Creds: creds, Core: core}))

	ns, err := natsserver.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second), "nats server did not start")
	t.Cleanup(ns.Shutdown)

	return ns, creds
}

// roleConn is a client connection plus the async error channel nats-server uses
// to report permission violations. Publish violations are transient: the
// connection stays up and only an async error is delivered, which is why the
// errors must be collected out of band rather than from the Publish return.
type roleConn struct {
	nc   *nats.Conn
	errs chan error
}

func connectAs(t *testing.T, ns *natsserver.Server, user, pass string) *roleConn {
	t.Helper()

	rc := &roleConn{errs: make(chan error, 256)}
	opts := []nats.Option{
		nats.ErrorHandler(func(_ *nats.Conn, _ *nats.Subscription, err error) {
			select {
			case rc.errs <- err:
			default:
			}
		}),
		nats.PermissionErrOnSubscribe(true),
	}
	if user != "" {
		opts = append(opts, nats.UserInfo(user, pass))
	}

	nc, err := nats.Connect(ns.ClientURL(), opts...)
	require.NoError(t, err, "connect as %q", user)
	t.Cleanup(nc.Close)
	rc.nc = nc
	return rc
}

// deniedPublishes publishes every subject once, then collects the permission
// violations that come back. Batching keeps the whole matrix under a second
// instead of paying a per-subject timeout.
func (rc *roleConn) deniedPublishes(t *testing.T, subjects []string) map[string]bool {
	t.Helper()

	for len(rc.errs) > 0 {
		<-rc.errs
	}
	for _, s := range subjects {
		require.NoError(t, rc.nc.Publish(s, []byte("x")))
	}
	require.NoError(t, rc.nc.Flush())

	denied := map[string]bool{}
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case err := <-rc.errs:
			if m := permSubject.FindStringSubmatch(err.Error()); m != nil {
				denied[m[1]] = true
			}
		case <-deadline:
			return denied
		}
	}
}

// allMatrixSubjects is every subject the matrix classifies, in a stable order.
func allMatrixSubjects() []string {
	subjects := concat(
		telemetryPublish,
		metaReadPublish,
		metaWritePublish,
		coreOnlyPublish,
		[]string{
			"$JS.API.STREAM.DELETE." + testStream,
			"$JS.API.CONSUMER.MSG.NEXT." + testStream + ".my-fanout",
			"$SYS.REQ.SERVER.PING",
			"_INBOX.forged.reply",
		},
	)
	sort.Strings(subjects)
	return subjects
}

// expectedAllowed returns the subjects a role may publish, per the ADR 0007
// matrix. Anything not listed must be denied.
func expectedAllowed(role string) map[string]bool {
	allow := map[string]bool{}
	add := func(lists ...[]string) {
		for _, l := range lists {
			for _, s := range l {
				allow[s] = true
			}
		}
	}
	switch role {
	case RoleOperator:
		add(telemetryPublish, metaReadPublish, metaWritePublish, []string{
			"$JS.API.STREAM.DELETE." + testStream,
			"$JS.API.CONSUMER.MSG.NEXT." + testStream + ".my-fanout",
		})
	case RoleAdapter:
		add(telemetryPublish, metaReadPublish)
	case RoleFanout:
		add([]string{"$JS.API.CONSUMER.MSG.NEXT." + testStream + ".my-fanout"})
	case RoleLegacy:
		add(telemetryPublish, metaReadPublish, []string{
			"$JS.API.CONSUMER.MSG.NEXT." + testStream + ".my-fanout",
		})
	}
	return allow
}

func TestRoleMatrixPublish(t *testing.T) {
	ns, creds := startAuthServer(t, ModeCompat, testStream)
	subjects := allMatrixSubjects()

	cases := []struct {
		role string
		user string
		pass string
	}{
		{RoleOperator, RoleOperator, creds.Operator},
		{RoleAdapter, RoleAdapter, creds.Adapter},
		{RoleFanout, RoleFanout, creds.Fanout},
		{RoleLegacy, "", ""}, // anonymous -> NoAuthUser
	}

	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			rc := connectAs(t, ns, tc.user, tc.pass)
			denied := rc.deniedPublishes(t, subjects)
			allowed := expectedAllowed(tc.role)

			for _, s := range subjects {
				if allowed[s] {
					assert.False(t, denied[s], "%s must be allowed to publish %s", tc.role, s)
				} else {
					assert.True(t, denied[s], "%s must be denied publishing %s", tc.role, s)
				}
			}

			// A permission violation must never drop the connection: adapters
			// stay alive and keep sending data while only the forbidden subject
			// is refused.
			assert.True(t, rc.nc.IsConnected(), "%s connection dropped by permission violation", tc.role)
		})
	}
}

// TestDenyWinsOverWildcardAllow pins the property the defensive Deny lists rely
// on. The Allow lists deliberately enumerate subjects one by one, which already
// excludes the dangerous ones; the Deny entries are belt-and-braces so that
// widening an Allow list to a wildcard later cannot silently re-open them.
// Without this test that redundancy is untested by construction — the matrix
// test cannot see it, because the narrow Allow list is what does the work.
func TestDenyWinsOverWildcardAllow(t *testing.T) {
	creds := Credentials{Operator: "op", Adapter: "ad", Fanout: "fo"}
	core, err := NewEphemeralCore()
	require.NoError(t, err)

	opts := &natsserver.Options{Port: -1}
	require.NoError(t, Apply(opts, Config{Mode: ModeStrict, Stream: testStream, Creds: creds, Core: core}))

	// Simulate a future maintainer widening the adapter role to a wildcard
	// while leaving the Deny list in place.
	for _, u := range opts.Users {
		if u.Username == RoleAdapter {
			u.Permissions.Publish.Allow = []string{"platform.>", "_INBOX.>", "$JS.API.>"}
		}
	}

	ns, err := natsserver.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(ns.Shutdown)

	rc := connectAs(t, ns, RoleAdapter, creds.Adapter)
	subjects := concat(metaWritePublish, coreOnlyPublish, []string{"_INBOX.forged.reply"})
	denied := rc.deniedPublishes(t, subjects)

	for _, s := range subjects {
		assert.True(t, denied[s], "Deny must still block %s despite a wildcard Allow", s)
	}
}

func TestCoreRoleIsAllowAll(t *testing.T) {
	assert.Nil(t, Permissions(RoleCore, testStream), "core must be allow-all (nil permissions)")
}

func TestUnknownRoleFailsClosed(t *testing.T) {
	p := Permissions("typo", testStream)
	require.NotNil(t, p)
	require.NotNil(t, p.Publish)
	assert.Equal(t, []string{">"}, p.Publish.Deny)
	assert.Empty(t, p.Publish.Allow, "unknown role must not allow anything")
}

func TestFanoutStreamNameIsDynamic(t *testing.T) {
	p := Permissions(RoleFanout, "OTHER_STREAM")
	require.NotNil(t, p)
	assert.Contains(t, p.Publish.Allow, "$JS.API.CONSUMER.MSG.NEXT.OTHER_STREAM.>")
	assert.NotContains(t, p.Publish.Allow, "$JS.API.CONSUMER.MSG.NEXT."+DefaultStreamName+".>")
}

func TestInvalidStreamNameFallsBackToDefault(t *testing.T) {
	for _, bad := range []string{"", "has.dot", "has space", "wild*", "gt>"} {
		t.Run(fmt.Sprintf("%q", bad), func(t *testing.T) {
			assert.False(t, ValidStreamName(bad))
			p := Permissions(RoleFanout, bad)
			assert.Contains(t, p.Publish.Allow, "$JS.API.STREAM.INFO."+DefaultStreamName)
		})
	}
}

func TestStrictModeRejectsAnonymous(t *testing.T) {
	ns, _ := startAuthServer(t, ModeStrict, testStream)
	_, err := nats.Connect(ns.ClientURL(), nats.Timeout(2*time.Second))
	require.Error(t, err, "strict mode must reject anonymous connections")
	assert.Contains(t, err.Error(), "Authorization Violation")
}

func TestCompatModeAcceptsAnonymous(t *testing.T) {
	ns, _ := startAuthServer(t, ModeCompat, testStream)
	nc, err := nats.Connect(ns.ClientURL(), nats.Timeout(2*time.Second))
	require.NoError(t, err, "compat mode must keep pre-auth clients connecting")
	defer nc.Close()
	assert.True(t, nc.IsConnected())
}
