package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e7217/edg/internal/core"
	"github.com/e7217/edg/internal/natsauth"
)

// startStrictCore runs a NATS server under strict authorization with the
// credentials file a real core would have written, plus a stand-in for core's
// import handler that records what it received.
func startStrictCore(t *testing.T) (core.CoreConfig, chan core.ImportAppliedRequest) {
	t.Helper()
	credsPath := filepath.Join(t.TempDir(), "nats-credentials.json")
	creds, _, err := natsauth.LoadOrCreate(credsPath)
	require.NoError(t, err)
	identity, err := natsauth.NewEphemeralCore()
	require.NoError(t, err)

	opts := &server.Options{Host: "127.0.0.1", Port: -1}
	require.NoError(t, natsauth.Apply(opts, natsauth.Config{
		Mode: core.NATSAuthModeStrict, Creds: creds, Core: identity,
	}))
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(ns.Shutdown)

	coreConn, err := nats.Connect(ns.ClientURL(), nats.UserInfo(identity.Username, identity.Secret))
	require.NoError(t, err)
	t.Cleanup(coreConn.Close)
	got := make(chan core.ImportAppliedRequest, 4)
	_, err = coreConn.Subscribe(core.SubjectImportApplied, func(msg *nats.Msg) {
		var req core.ImportAppliedRequest
		_ = json.Unmarshal(msg.Data, &req)
		got <- req
		resp, _ := json.Marshal(core.Response{Success: true, Data: core.ImportAppliedResult{Announced: len(req.Points)}})
		_ = msg.Respond(resp)
	})
	require.NoError(t, err)
	require.NoError(t, coreConn.Flush())

	var cfg core.CoreConfig
	cfg.NATS.Host = "0.0.0.0" // the default bind; the CLI must dial loopback
	cfg.NATS.Port = ns.Addr().(*net.TCPAddr).Port
	cfg.NATS.Auth.Mode = core.NATSAuthModeStrict
	cfg.NATS.Auth.CredentialsFile = credsPath
	return cfg, got
}

func pointsBatch() []core.ImportAppliedRequest {
	return []core.ImportAppliedRequest{{
		SchemaVersion: core.EventSchemaVersion,
		Source:        "cli:import-points",
		Points:        []core.ImportedEntity{{ID: "pump-a", EventType: core.EventUpdated}},
	}}
}

// The CLI authenticates as operator from the credentials file core wrote,
// which is the only role strict mode lets send the notification.
func TestNotifyCoreAuthenticatesAsOperator(t *testing.T) {
	cfg, got := startStrictCore(t)

	announced, err := notifyCore(cfg, pointsBatch())
	require.NoError(t, err)
	assert.Equal(t, 1, announced)
	req := <-got
	assert.Equal(t, "cli:import-points", req.Source)
}

func TestNotifyCoreWithoutCredentialsFails(t *testing.T) {
	cfg, _ := startStrictCore(t)
	cfg.NATS.Auth.CredentialsFile = filepath.Join(t.TempDir(), "absent.json")

	_, err := notifyCore(cfg, pointsBatch())
	require.Error(t, err)
	_, statErr := natsauth.Load(cfg.NATS.Auth.CredentialsFile)
	require.Error(t, statErr, "notifying must not create a credentials file")
}

func TestNotifyCoreWithNoCoreRunning(t *testing.T) {
	var cfg core.CoreConfig
	cfg.NATS.Host = "127.0.0.1"
	cfg.NATS.Port = 1 // nothing listens here
	cfg.NATS.Auth.Mode = core.NATSAuthModeOff

	start := time.Now()
	_, err := notifyCore(cfg, pointsBatch())
	require.Error(t, err)
	assert.Less(t, time.Since(start), 5*time.Second, "an absent core must not stall the import")
}
