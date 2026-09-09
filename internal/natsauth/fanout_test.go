package natsauth

import (
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsAPITimeout bounds JetStream API round trips in these tests. Denied calls
// have no response at all, so without this each one would burn the 5s default
// and the file would take ~30s under -race.
const jsAPITimeout = 400 * time.Millisecond

// seedStream creates PLATFORM_DATA as core and publishes one validated message,
// mirroring the stream config in internal/core/config.go DefaultCoreConfig.
func seedStream(t *testing.T, ns *natsserver.Server, core Ephemeral, stream string) {
	t.Helper()

	nc, err := nats.Connect(ns.ClientURL(), nats.UserInfo(core.Username, core.Secret))
	require.NoError(t, err)
	defer nc.Close()

	js, err := nc.JetStream()
	require.NoError(t, err)
	_, err = js.AddStream(&nats.StreamConfig{
		Name:     stream,
		Subjects: []string{"platform.data.>"},
		Storage:  nats.MemoryStorage,
	})
	require.NoError(t, err)

	_, err = js.Publish("platform.data.validated", []byte(`{"asset_id":"sensor-1"}`))
	require.NoError(t, err)
}

// startFanoutServer boots a server and returns the core identity too, which the
// matrix harness does not expose.
func startFanoutServer(t *testing.T, mode, stream string) (*natsserver.Server, Credentials, Ephemeral) {
	t.Helper()

	creds := Credentials{Operator: "op-secret", Adapter: "ad-secret", Fanout: "fo-secret"}
	core, err := NewEphemeralCore()
	require.NoError(t, err)

	opts := &natsserver.Options{Port: -1, JetStream: true, StoreDir: t.TempDir()}
	require.NoError(t, Apply(opts, Config{Mode: mode, Stream: stream, Creds: creds, Core: core}))

	ns, err := natsserver.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(ns.Shutdown)

	return ns, creds, core
}

// TestFanoutRoleCanDrainDurableConsumer is the ADR 0006 lock test.
//
// ADR 0006 Option A ("Recommended now") tells operators to attach an external
// routing engine with its own durable JetStream consumer. If the fanout
// allowlist is ever narrowed below the verified minimum, that documented
// integration path silently breaks. This test fails first.
func TestFanoutRoleCanDrainDurableConsumer(t *testing.T) {
	ns, creds, core := startFanoutServer(t, ModeStrict, testStream)
	seedStream(t, ns, core, testStream)

	nc, err := nats.Connect(ns.ClientURL(), nats.UserInfo(RoleFanout, creds.Fanout))
	require.NoError(t, err)
	defer nc.Close()

	js, err := nc.JetStream(nats.MaxWait(jsAPITimeout))
	require.NoError(t, err)

	sub, err := js.PullSubscribe("platform.data.validated", "my-fanout", nats.BindStream(testStream))
	require.NoError(t, err, "fanout must be able to create a durable pull consumer")
	// No Unsubscribe: deleting a durable consumer needs $JS.API.CONSUMER.DELETE,
	// which fanout is deliberately denied (see TestFanoutCannotDeleteConsumer).

	msgs, err := sub.Fetch(1, nats.MaxWait(3*time.Second))
	require.NoError(t, err, "fanout must be able to fetch")
	require.Len(t, msgs, 1)
	require.NoError(t, msgs[0].Ack(), "fanout must be able to ack")
}

// TestLegacyRoleCanDrainDurableConsumer proves the compat promise for existing
// anonymous fan-out consumers. Mapping NoAuthUser to RoleAdapter instead of
// RoleLegacy would disconnect them on upgrade — the exact "flag day" this
// design exists to avoid.
func TestLegacyRoleCanDrainDurableConsumer(t *testing.T) {
	ns, _, core := startFanoutServer(t, ModeCompat, testStream)
	seedStream(t, ns, core, testStream)

	nc, err := nats.Connect(ns.ClientURL()) // anonymous, as a pre-auth deployment connects
	require.NoError(t, err)
	defer nc.Close()

	js, err := nc.JetStream(nats.MaxWait(jsAPITimeout))
	require.NoError(t, err)

	sub, err := js.PullSubscribe("platform.data.validated", "legacy-fanout", nats.BindStream(testStream))
	require.NoError(t, err, "compat mode must keep existing anonymous durable consumers working")

	msgs, err := sub.Fetch(1, nats.MaxWait(3*time.Second))
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.NoError(t, msgs[0].Ack())
}

// TestFanoutCannotDestroyStream pins the least-privilege half: a fan-out
// consumer must not be able to delete or purge the data plane it reads.
func TestFanoutCannotDestroyStream(t *testing.T) {
	ns, creds, core := startFanoutServer(t, ModeStrict, testStream)
	seedStream(t, ns, core, testStream)

	nc, err := nats.Connect(ns.ClientURL(), nats.UserInfo(RoleFanout, creds.Fanout))
	require.NoError(t, err)
	defer nc.Close()

	js, err := nc.JetStream(nats.MaxWait(jsAPITimeout))
	require.NoError(t, err)

	assert.Error(t, js.DeleteStream(testStream), "fanout must not delete the stream")
	assert.Error(t, js.PurgeStream(testStream), "fanout must not purge the stream")

	// The stream is still intact and still holds its message.
	info, err := js.StreamInfo(testStream)
	require.NoError(t, err, "fanout may read stream info")
	assert.Equal(t, uint64(1), info.State.Msgs)
}

// TestFanoutCannotDeleteConsumer documents a deliberate sharp edge: a fan-out
// client cannot remove its own durable consumer, because $JS.API.CONSUMER.DELETE
// is not scoped to the caller's consumer — granting it would also let a fan-out
// delete edg-core-vm-sink's durable consumer (ADR 0005), resetting the built-in
// sink's ack position. Consumer lifecycle is an operator task.
func TestFanoutCannotDeleteConsumer(t *testing.T) {
	ns, creds, core := startFanoutServer(t, ModeStrict, testStream)
	seedStream(t, ns, core, testStream)

	nc, err := nats.Connect(ns.ClientURL(), nats.UserInfo(RoleFanout, creds.Fanout))
	require.NoError(t, err)
	defer nc.Close()

	js, err := nc.JetStream(nats.MaxWait(jsAPITimeout))
	require.NoError(t, err)

	_, err = js.PullSubscribe("platform.data.validated", "cleanup-me", nats.BindStream(testStream))
	require.NoError(t, err)

	assert.Error(t, js.DeleteConsumer(testStream, "cleanup-me"),
		"fanout must not delete consumers; that would also reach edg-core-vm-sink")
}

// TestAdapterCannotUseJetStreamAPI proves the adapter role is confined to the
// plain-NATS data plane hop defined by ADR 0001.
func TestAdapterCannotUseJetStreamAPI(t *testing.T) {
	ns, creds, core := startFanoutServer(t, ModeStrict, testStream)
	seedStream(t, ns, core, testStream)

	nc, err := nats.Connect(ns.ClientURL(), nats.UserInfo(RoleAdapter, creds.Adapter))
	require.NoError(t, err)
	defer nc.Close()

	js, err := nc.JetStream(nats.MaxWait(jsAPITimeout))
	require.NoError(t, err)

	_, err = js.StreamInfo(testStream)
	assert.Error(t, err, "adapter must not reach the JetStream API")

	_, err = js.PullSubscribe("platform.data.validated", "adapter-fanout", nats.BindStream(testStream))
	assert.Error(t, err, "adapter must not create a durable consumer")
}
