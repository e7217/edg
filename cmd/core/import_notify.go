package main

import (
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/e7217/edg/internal/core"
	"github.com/e7217/edg/internal/natsauth"
)

// importNotifyTimeout bounds connecting to a running core and each request.
// An import must not hang on a core that is not there.
const importNotifyTimeout = 2 * time.Second

// notifyRunningCore tells a running edg-core what an import just wrote, so its
// caches follow and adapters are told at once rather than after a restart
// (#150). The import has committed either way; failing to reach core is
// reported, not an error.
func notifyRunningCore(cfg core.CoreConfig, source string, rec *core.EventPublisher, templatesChanged bool) {
	batches := core.BuildImportApplied(source, rec.Recorded(), templatesChanged)
	if len(batches) == 0 {
		return
	}
	announced, err := notifyCore(cfg, batches)
	if err != nil {
		fmt.Printf("Could not tell a running edg-core about these changes (%v).\n"+
			"If edg-core is running, restart it; adapters provisioned from changed point lists "+
			"also pick them up at their next reconcile (5 min).\n", err)
		return
	}
	fmt.Printf("Running edg-core updated: %d change event(s) announced", announced)
	if templatesChanged {
		fmt.Print(", templates reloaded")
	}
	fmt.Println(".")
}

// notifyCore dials the core's NATS port as the operator role and sends the
// batches.
func notifyCore(cfg core.CoreConfig, batches []core.ImportAppliedRequest) (int, error) {
	opts := []nats.Option{
		nats.Name("edg-core import"),
		nats.Timeout(importNotifyTimeout),
		nats.NoReconnect(),
	}
	if cfg.NATS.Auth.Mode != core.NATSAuthModeOff {
		creds, err := natsauth.Load(cfg.NATSCredentialsFile())
		if err != nil {
			return 0, fmt.Errorf("NATS credentials: %w", err)
		}
		opts = append(opts, nats.UserInfo(natsauth.RoleOperator, creds.Operator))
	}
	nc, err := nats.Connect(coreNATSURL(cfg), opts...)
	if err != nil {
		return 0, err
	}
	defer nc.Close()
	return core.NotifyImportApplied(nc, batches, importNotifyTimeout)
}

// coreNATSURL is where this host's core listens. A wildcard bind address is
// not something to dial, so it becomes loopback.
func coreNATSURL(cfg core.CoreConfig) string {
	host := cfg.NATS.Host
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1"
	}
	return "nats://" + net.JoinHostPort(host, strconv.Itoa(cfg.NATS.Port))
}
