package sdk

import (
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// startTestNATSServer launches an in-process NATS server on a random port and
// returns its client URL. The server is shut down via t.Cleanup.
func startTestNATSServer(t *testing.T) string {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{Port: -1})
	if err != nil {
		t.Fatalf("new nats server: %v", err)
	}
	go ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("nats server not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns.ClientURL()
}

// connectControl returns a separate "control" connection used by tests to
// stand in for the EDG Core (publishing replies, sending events).
func connectControl(t *testing.T, url string) *nats.Conn {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("control connect: %v", err)
	}
	t.Cleanup(nc.Close)
	return nc
}
